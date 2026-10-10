package agent

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// HarnessPrompt is one host-native approval setting a harness keeps in place, so a guard
// verdict of ask reaches the person through the host's own prompt.
//
// A host whose hooks cannot prompt still asks where its own rules or config say to. The
// hook then ANSWERS that prompt from the verdict. Without the setting nothing asks, and the
// templates refuse the call instead of letting it through unasked.
//
// Exactly one body: Content is a whole file the descriptor owns, or Key and Value set one
// value inside a JSON document the person also edits.
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

// HarnessAgentFile is one subagent file a harness spell rendered in its host's format. The
// descriptor owns the whole file, so a plan rewrites it whenever the bytes differ.
type HarnessAgentFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func validateHarnessAgentFile(f HarnessAgentFile) error {
	if f.Path == "" || filepath.IsAbs(f.Path) || !isSafeRelativePath(f.Path) {
		return fmt.Errorf("path must be a workspace-relative path")
	}
	if f.Content == "" {
		return fmt.Errorf("%s: content is required", f.Path)
	}
	return nil
}

// planWholeFile reports what the file at rel needs to hold content, with no Changes when it
// already does. The descriptor owns every byte of the file.
func planWholeFile(root, rel, content string) (types.HarnessFile, error) {
	path, err := harnessConfigPath(root, rel)
	if err != nil {
		return types.HarnessFile{}, err
	}
	existing, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return types.HarnessFile{}, fmt.Errorf("agent: read %s: %w", path, readErr)
	}
	file := types.HarnessFile{Exists: readErr == nil}
	if !bytes.Equal(existing, []byte(content)) {
		file.Content = content
		file.Changes = []types.HarnessChange{{Op: types.HarnessWrite}}
	}
	return file, nil
}

// promptLocation renders a prompt for a message: the file, and the key inside it.
func promptLocation(p HarnessPrompt) string {
	if len(p.Key) == 0 {
		return p.Path
	}
	return p.Path + " " + strings.Join(p.Key, ".")
}

// planHarnessPrompt reports what p's file needs, with no Changes when it is in place. A key
// already holding another value is an error: it was someone's choice, and no plan
// overwrites it.
func planHarnessPrompt(root string, p HarnessPrompt) (types.HarnessFile, error) {
	if p.Content != "" {
		return planWholeFile(root, p.Path, p.Content)
	}
	path, err := harnessConfigPath(root, p.Path)
	if err != nil {
		return types.HarnessFile{}, err
	}
	existing, readErr := os.ReadFile(path)
	if readErr != nil && !os.IsNotExist(readErr) {
		return types.HarnessFile{}, fmt.Errorf("agent: read %s: %w", path, readErr)
	}
	file := types.HarnessFile{Exists: readErr == nil}
	doc := map[string]any{}
	if len(existing) > 0 {
		if err := decodeHarnessJSON(existing, &doc); err != nil {
			return file, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	parent, found, err := promptParent(doc, p)
	if err != nil {
		return file, err
	}
	last := p.Key[len(p.Key)-1]
	if got, present := parent[last]; found && present {
		if sameJSON(got, p.Value) {
			return file, nil
		}
		return file, fmt.Errorf("%s holds %v, not %v, so the host never asks the person there; magus leaves a value someone chose alone. Change or remove it, then describe the harness again",
			promptLocation(p), got, p.Value)
	}
	fragment := map[string]any{}
	current := fragment
	for _, key := range p.Key[:len(p.Key)-1] {
		next := map[string]any{}
		current[key] = next
		current = next
	}
	current[last] = p.Value
	file.Fragment = fragment
	file.Changes = []types.HarnessChange{{Op: types.HarnessSet, Key: strings.Join(p.Key, "."), Value: p.Value}}
	return file, nil
}

// promptParent walks to the object holding a prompt's last key, and refuses a path through a
// value that is not an object. found is false when an object on the way is absent.
func promptParent(doc map[string]any, p HarnessPrompt) (parent map[string]any, found bool, err error) {
	current := doc
	for i, key := range p.Key[:len(p.Key)-1] {
		next, present := current[key]
		if !present || next == nil {
			return nil, false, nil
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

// verifyHarnessAgents reports whether every subagent file a descriptor renders holds the bytes
// the spell rendered. A descriptor with none leaves the status empty.
func verifyHarnessAgents(root string, d HarnessDescriptor, result *HarnessVerification) {
	for _, a := range d.Agents {
		file, err := planWholeFile(root, a.Path, a.Content)
		switch {
		case err != nil:
			result.AgentStatus, result.AgentReason = HarnessUncovered, err.Error()
			return
		case len(file.Changes) > 0:
			result.AgentStatus = HarnessUncovered
			result.AgentReason = a.Path + " is missing or differs from what the spell renders; merge what `magus describe harness " + d.ID + "` prints"
			return
		}
	}
	if len(d.Agents) > 0 {
		result.AgentStatus = HarnessVerified
	}
}

// verifyHarnessPrompts reports whether every prompt a descriptor keeps is in place. A
// descriptor with none leaves the status empty.
func verifyHarnessPrompts(root string, d HarnessDescriptor, result *HarnessVerification) {
	if len(d.Prompts) == 0 {
		return
	}
	for _, p := range d.Prompts {
		file, err := planHarnessPrompt(root, p)
		switch {
		case err != nil:
			result.PromptStatus, result.PromptReason = HarnessUncovered, err.Error()
			return
		case len(file.Changes) > 0:
			result.PromptStatus = HarnessUncovered
			result.PromptReason = "missing " + promptLocation(p) + ", so the host never asks the person before the call; merge what `magus describe harness " + d.ID + "` prints"
			return
		}
	}
	result.PromptStatus = HarnessVerified
}
