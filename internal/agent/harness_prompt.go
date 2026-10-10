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
	// Hint is the person-owned setting that picks this agent's model, printed with the plan
	// when the spell could not pick one itself. Editing the file instead would be undone by
	// the next plan, which rewrites any byte that differs.
	Hint string `json:"hint,omitempty"`
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
	return planJSONKey(root, p.Path, p.Key, p.Value, func(got any) error {
		return fmt.Errorf("%s holds %v, not %v, so the host never asks the person there; magus leaves a value someone chose alone. Change or remove it, then describe the harness again",
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

// HarnessSetting is one plain value a harness keeps in a host's JSON config, read by the host
// as configuration rather than run as a hook: a plugin to enable, a marketplace to know. Key
// and Value set it inside a document the person also edits.
type HarnessSetting struct {
	Path  string   `json:"path"`
	Key   []string `json:"key"`
	Value any      `json:"value"`
}

func validateHarnessSetting(s HarnessSetting) error {
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
func settingLocation(s HarnessSetting) string {
	return s.Path + " " + strings.Join(s.Key, ".")
}

// planHarnessSetting reports what s's file needs, with no Changes when it is in place.
func planHarnessSetting(root string, s HarnessSetting) (types.HarnessFile, error) {
	return planJSONKey(root, s.Path, s.Key, s.Value, func(got any) error {
		return fmt.Errorf("%s holds %v, not %v, and magus leaves a value someone chose alone: change or remove it, then describe the harness again",
			settingLocation(s), got, s.Value)
	})
}

// verifyHarnessSettings reports whether every setting a descriptor keeps is in place. A
// descriptor with none leaves the status empty.
func verifyHarnessSettings(root string, d HarnessDescriptor, result *HarnessVerification) {
	if len(d.Settings) == 0 {
		return
	}
	for _, s := range d.Settings {
		file, err := planHarnessSetting(root, s)
		switch {
		case err != nil:
			result.SettingStatus, result.SettingReason = HarnessUncovered, err.Error()
			return
		case len(file.Changes) > 0:
			result.SettingStatus = HarnessUncovered
			result.SettingReason = "missing " + settingLocation(s) + "; merge what `magus describe harness " + d.ID + "` prints"
			return
		}
	}
	result.SettingStatus = HarnessVerified
}
