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

// validateHarnessPrompt holds p to one body. A host whose hooks cannot prompt still asks
// where its own rules or config say to, and the hook then answers that prompt from the
// verdict; without the setting nothing asks, and the templates refuse the call instead.
func validateHarnessPrompt(p types.Prompt) error {
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

func validateHarnessAgentFile(f types.AgentFile) error {
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
func promptLocation(p types.Prompt) string {
	if len(p.Key) == 0 {
		return p.Path
	}
	return p.Path + " " + strings.Join(p.Key, ".")
}

// planHarnessPrompt reports what p's file needs, with no Changes when it is in place. A key
// already holding another value is an error: it was someone's choice, and no plan
// overwrites it.
func planHarnessPrompt(root string, p types.Prompt) (types.HarnessFile, error) {
	if p.Content != "" {
		return planWholeFile(root, p.Path, p.Content)
	}
	return planJSONKey(root, p.Path, p.Key, p.Value, func(got any) error {
		return fmt.Errorf("%s holds %v, not %v, so the host never asks the person there, and magus leaves a value someone chose alone: change or remove it, then describe the harness again",
			promptLocation(p), got, p.Value)
	})
}

// planJSONKey reports what the JSON document at rel needs for key to hold value, with no
// Changes when it already does. A key holding another value was someone's choice: the
// error is conflict's, and no plan overwrites it.
func planJSONKey(root, rel string, key []string, value any, conflict func(got any) error) (types.HarnessFile, error) {
	path, err := harnessConfigPath(root, rel)
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
	parent, found, err := keyParent(doc, rel, key)
	if err != nil {
		return file, err
	}
	last := key[len(key)-1]
	if got, present := parent[last]; found && present {
		if sameJSON(got, value) {
			return file, nil
		}
		return file, conflict(got)
	}
	fragment := map[string]any{}
	current := fragment
	for _, k := range key[:len(key)-1] {
		next := map[string]any{}
		current[k] = next
		current = next
	}
	current[last] = value
	file.Fragment = fragment
	file.Changes = []types.HarnessChange{{Op: types.HarnessSet, Key: strings.Join(key, "."), Value: value}}
	return file, nil
}

// keyParent walks to the object holding key's last element in the document read from rel,
// and refuses a path through a value that is not an object. found is false when an object
// on the way is absent.
func keyParent(doc map[string]any, rel string, key []string) (parent map[string]any, found bool, err error) {
	current := doc
	for i, k := range key[:len(key)-1] {
		next, present := current[k]
		if !present || next == nil {
			return nil, false, nil
		}
		obj, ok := next.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%s: %s is not an object", rel, strings.Join(key[:i+1], "."))
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

func validateHarnessSetting(s types.Setting) error {
	if s.Path == "" || filepath.IsAbs(s.Path) || !isSafeRelativePath(s.Path) {
		return fmt.Errorf("path must be a workspace-relative path")
	}
	if len(s.Key) == 0 {
		return fmt.Errorf("%s: key is required", s.Path)
	}
	for _, k := range s.Key {
		if k == "" {
			return fmt.Errorf("%s: key must contain non-empty object keys", s.Path)
		}
	}
	if s.Value == nil {
		return fmt.Errorf("%s: key needs a value", s.Path)
	}
	return nil
}

// settingLocation renders a setting for a message: the file, and the key inside it.
func settingLocation(s types.Setting) string {
	return s.Path + " " + strings.Join(s.Key, ".")
}

// planHarnessSetting reports what s's file needs, with no Changes when it is in place.
func planHarnessSetting(root string, s types.Setting) (types.HarnessFile, error) {
	return planJSONKey(root, s.Path, s.Key, s.Value, func(got any) error {
		return fmt.Errorf("%s holds %v, not %v, and magus leaves a value someone chose alone: change or remove it, then describe the harness again",
			settingLocation(s), got, s.Value)
	})
}
