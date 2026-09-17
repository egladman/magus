package agent

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/json"
)

// HarnessPrompt is one host-native approval setting a harness keeps in place, so a guard
// verdict of ask reaches the person through the host's own prompt.
//
// A host whose hooks cannot prompt still asks where its own rules or config say to. The
// hook then ANSWERS that prompt from the verdict. Without the setting nothing asks, and the
// templates refuse the call instead of letting it through unasked.
//
// Exactly one body: Content is a whole file magus owns, or Key and Value set one value
// inside a JSON document the person also edits.
type HarnessPrompt struct {
	Path    string   `json:"path"`
	Content string   `json:"content,omitempty"`
	Key     []string `json:"key,omitempty"`
	Value   any      `json:"value,omitempty"`
}

func validateHarnessPrompt(p HarnessPrompt) error {
	if p.Path == "" || filepath.IsAbs(p.Path) || !isSafeRelativePath(p.Path) {
		return fmt.Errorf("path must be a workspace-relative path")
	}
	hasKey := len(p.Key) > 0
	switch {
	case p.Content == "" && !hasKey:
		return fmt.Errorf("%s: set content, or key and value", p.Path)
	case p.Content != "" && hasKey:
		return fmt.Errorf("%s: set content or key, not both", p.Path)
	case hasKey && p.Value == nil:
		return fmt.Errorf("%s: key needs a value", p.Path)
	}
	for _, k := range p.Key {
		if k == "" {
			return fmt.Errorf("%s: key must contain non-empty object keys", p.Path)
		}
	}
	return nil
}

// promptLocation renders a prompt for a message: the file, and the key inside it.
func promptLocation(p HarnessPrompt) string {
	if len(p.Key) == 0 {
		return p.Path
	}
	return p.Path + " " + strings.Join(p.Key, ".")
}

// promptBody reads a prompt's file and reports the body with the prompt applied and whether
// that differs from what is on disk. A key already holding another value is an error: it
// was someone's choice, and overwriting it is not apply's call.
func promptBody(root string, p HarnessPrompt) (path string, body []byte, changed bool, err error) {
	path, err = harnessConfigPath(root, p.Path)
	if err != nil {
		return "", nil, false, err
	}
	existing, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return path, nil, false, fmt.Errorf("agent: read %s: %w", path, readErr)
	}
	if p.Content != "" {
		return path, []byte(p.Content), !bytes.Equal(existing, []byte(p.Content)), nil
	}
	doc := map[string]any{}
	if len(existing) > 0 {
		if err := decodeHarnessJSON(existing, &doc); err != nil {
			return path, nil, false, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	parent, _, err := promptParent(doc, p, true)
	if err != nil {
		return path, nil, false, err
	}
	last := p.Key[len(p.Key)-1]
	if got, present := parent[last]; present {
		if sameJSON(got, p.Value) {
			return path, existing, false, nil
		}
		return path, nil, false, fmt.Errorf("%s holds %v, not %v, so the host never asks the person there; magus leaves a value someone chose alone. Change or remove it, then apply again",
			promptLocation(p), got, p.Value)
	}
	parent[last] = p.Value
	encoded, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return path, nil, false, fmt.Errorf("encode %s: %w", path, err)
	}
	return path, append(encoded, '\n'), true, nil
}

// promptParent walks to the object holding a prompt's last key, creating the objects on the
// way when create is set, and refuses a path through a value that is not an object. found
// is false only when create is unset and an object on the way is absent.
func promptParent(doc map[string]any, p HarnessPrompt, create bool) (parent map[string]any, found bool, err error) {
	current := doc
	for i, key := range p.Key[:len(p.Key)-1] {
		next, present := current[key]
		if !present || next == nil {
			if !create {
				return nil, false, nil
			}
			made := map[string]any{}
			current[key] = made
			current = made
			continue
		}
		obj, ok := next.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%s: %s is not an object", p.Path, strings.Join(p.Key[:i+1], "."))
		}
		current = obj
	}
	return current, true, nil
}

func sameJSON(a, b any) bool {
	ea, errA := json.Marshal(a)
	eb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ea, eb)
}

// applyHarnessPrompts writes every prompt that is missing and records the paths it touched.
func applyHarnessPrompts(root string, d HarnessDescriptor, dryRun bool, update *HarnessUpdate) error {
	for _, p := range d.Prompts {
		path, body, changed, err := promptBody(root, p)
		if err != nil {
			return err
		}
		if !changed {
			continue
		}
		update.Prompts = append(update.Prompts, path)
		update.Changed = true
		if dryRun {
			update.Planned = true
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("agent: mkdir %s: %w", filepath.Dir(path), err)
		}
		if err := writeHarnessAtomically(path, body); err != nil {
			return fmt.Errorf("agent: write %s: %w", path, err)
		}
	}
	return nil
}

// removeHarnessPrompts undoes applyHarnessPrompts for whatever still holds exactly what apply
// wrote: a rules file someone edited, or a key someone changed, is theirs now.
func removeHarnessPrompts(root string, d HarnessDescriptor, dryRun bool, update *HarnessUpdate) error {
	for _, p := range d.Prompts {
		path, err := harnessConfigPath(root, p.Path)
		if err != nil {
			return err
		}
		existing, readErr := os.ReadFile(path)
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return fmt.Errorf("agent: read %s: %w", path, readErr)
		}
		var body []byte
		if p.Content != "" {
			if !bytes.Equal(existing, []byte(p.Content)) {
				continue
			}
		} else {
			doc := map[string]any{}
			if err := decodeHarnessJSON(existing, &doc); err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			parent, found, err := promptParent(doc, p, false)
			if err != nil {
				return err
			}
			last := p.Key[len(p.Key)-1]
			if !found || !sameJSON(parent[last], p.Value) {
				continue
			}
			delete(parent, last)
			pruneEmptyObjects(doc, p.Key[:len(p.Key)-1])
			encoded, err := json.MarshalIndent(doc, "", "  ")
			if err != nil {
				return fmt.Errorf("encode %s: %w", path, err)
			}
			body = append(encoded, '\n')
		}
		update.Prompts = append(update.Prompts, path)
		update.Changed = true
		if dryRun {
			update.Planned = true
			continue
		}
		if body == nil {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("agent: remove %s: %w", path, err)
			}
			continue
		}
		if err := writeHarnessAtomically(path, body); err != nil {
			return fmt.Errorf("agent: write %s: %w", path, err)
		}
	}
	return nil
}

// pruneEmptyObjects deletes the objects along keys that removal left empty, deepest first,
// so remove does not leave `"permission": {"bash": {}}` behind in a config apply created.
func pruneEmptyObjects(doc map[string]any, keys []string) {
	if len(keys) == 0 {
		return
	}
	child, ok := doc[keys[0]].(map[string]any)
	if !ok {
		return
	}
	pruneEmptyObjects(child, keys[1:])
	if len(child) == 0 {
		delete(doc, keys[0])
	}
}

// verifyHarnessPrompts reports whether every prompt a descriptor keeps is in place. A
// descriptor with none leaves the status empty.
func verifyHarnessPrompts(root string, d HarnessDescriptor, result *HarnessVerification) {
	if len(d.Prompts) == 0 {
		return
	}
	for _, p := range d.Prompts {
		_, _, changed, err := promptBody(root, p)
		switch {
		case err != nil:
			result.PromptStatus, result.PromptReason = HarnessUncovered, err.Error()
			return
		case changed:
			result.PromptStatus = HarnessUncovered
			result.PromptReason = "missing " + promptLocation(p) + ", so the host never asks the person before the call; run `magus agent harness apply --id " + d.ID + "`"
			return
		}
	}
	result.PromptStatus = HarnessVerified
}
