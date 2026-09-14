package agent

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/json"
)

const (
	harnessSchemaVersion = 2
	harnessDirName       = "harnesses"
)

var harnessIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// HarnessDescriptor is a user-owned contract between an agent host and
// the provider-neutral Magus guard. Descriptors are loaded from
// $XDG_CONFIG_HOME/magus/harnesses, harnesses, and .magus/harnesses; later
// workspace-local sources override a user descriptor with the same ID.
//
// Magus owns the command injected into every declared hook. A descriptor can
// choose host storage and event shape, but it cannot replace the adapter with
// an arbitrary command and claim guard coverage.
type HarnessDescriptor struct {
	SchemaVersion  int               `json:"schema_version"`
	ID             string            `json:"id"`
	Display        HarnessDisplay    `json:"display"`
	Config         HarnessConfig     `json:"config"`
	Skills         HarnessSkills     `json:"skills"`
	PreToolUse     HarnessPreToolUse `json:"pre_tool_use"`
	ManagedEntries []HarnessEntries  `json:"managed_entries,omitempty"`
}

// HarnessDisplay is opaque metadata for host UIs. The core validates no
// provider identity or presentation convention.
type HarnessDisplay struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
	Icon  string `json:"icon,omitempty"`
}

// HarnessConfig identifies the workspace-local JSON document maintained by a
// descriptor. Path must be relative to the workspace, so an imported
// descriptor cannot silently redirect an apply to another checkout or a user
// configuration file.
type HarnessConfig struct {
	Path string `json:"path"`
}

// HarnessSkills lists workspace-relative locations this collaborator reads.
type HarnessSkills struct {
	Paths []string `json:"paths"`
	Form  Form     `json:"form"`
}

// HarnessPreToolUse describes how a host persists pre-tool adapters. Path
// selects the array inside Config.Path; MatcherKey and HooksKey name the host
// entry fields rather than assuming a particular provider's JSON shape.
type HarnessPreToolUse struct {
	Path             []string           `json:"path"`
	MatcherKey       string             `json:"matcher_key"`
	HooksKey         string             `json:"hooks_key"`
	Entries          []HarnessHookEntry `json:"entries"`
	ResponseTemplate string             `json:"response_template"`
}

// HarnessHookEntry is one host tool matcher. Hook carries only host-specific
// fields such as timeouts or labels; command is deliberately reserved for the
// generic Magus adapter.
type HarnessHookEntry struct {
	Matcher string         `json:"matcher"`
	Hook    map[string]any `json:"hook"`
	Observe bool           `json:"observe,omitempty"`
}

// HarnessEntries declares exact entries in another host hook event. These
// entries are user-owned configuration; unlike PreToolUse, they carry no guard
// response contract.
type HarnessEntries struct {
	Path    []string         `json:"path"`
	Entries []map[string]any `json:"entries"`
}

// HarnessUpdate records an explicit, narrow harness mutation.
type HarnessUpdate struct {
	Host    string `json:"host"`
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
	Planned bool   `json:"planned,omitempty"`
}

// HarnessApplyOptions contains the sole authority needed to mutate a harness.
// ActingLease must come from trusted command ingress, never a late environment
// read in a nested process.
type HarnessApplyOptions struct {
	Root        string
	Host        string
	DryRun      bool
	ActingLease string
}

// HarnessVerification makes coverage gaps explicit. A missing or invalid
// descriptor is not coverage; neither is a configuration that merely contains
// a string resembling Magus.
type HarnessVerification struct {
	Host       string `json:"host"`
	Descriptor string `json:"descriptor"`
	Path       string `json:"path"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	Guarded    bool   `json:"guarded,omitempty"`
}

// LoadHarness resolves one validated descriptor. Workspace-owned descriptors
// take precedence over user-global ones, allowing a repository to pin its
// harness contract without recompiling Magus.
func LoadHarness(root, host string) (HarnessDescriptor, string, error) {
	if root == "" {
		return HarnessDescriptor{}, "", fmt.Errorf("workspace root is required to load a harness")
	}
	if !harnessIDPattern.MatchString(host) {
		return HarnessDescriptor{}, "", fmt.Errorf("invalid harness ID %q", host)
	}
	descriptors, err := loadHarnesses(root)
	if err != nil {
		return HarnessDescriptor{}, "", err
	}
	d, ok := descriptors[host]
	if !ok {
		return HarnessDescriptor{}, "", fmt.Errorf("no harness descriptor named %q (install one in harnesses, .magus/harnesses, or $XDG_CONFIG_HOME/magus/harnesses)", host)
	}
	return d.descriptor, d.source, nil
}

type loadedHarness struct {
	descriptor HarnessDescriptor
	source     string
}

func loadHarnesses(root string) (map[string]loadedHarness, error) {
	dirs := make([]string, 0, 2)
	if base, err := config.UserConfigDir(); err == nil {
		dirs = append(dirs, filepath.Join(base, "magus", harnessDirName))
	}
	dirs = append(dirs,
		filepath.Join(root, harnessDirName),
		filepath.Join(root, ".magus", harnessDirName),
	)
	loaded := map[string]loadedHarness{}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read harness descriptors in %s: %w", dir, err)
		}
		seen := map[string]bool{}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			source := filepath.Join(dir, entry.Name())
			body, err := os.ReadFile(source)
			if err != nil {
				return nil, err
			}
			var d HarnessDescriptor
			if err := decodeHarnessJSON(body, &d); err != nil {
				return nil, fmt.Errorf("parse harness descriptor %s: %w", source, err)
			}
			if err := validateHarnessDescriptor(d); err != nil {
				return nil, fmt.Errorf("invalid harness descriptor %s: %w", source, err)
			}
			if seen[d.ID] {
				return nil, fmt.Errorf("duplicate harness descriptor ID %q in %s", d.ID, dir)
			}
			seen[d.ID] = true
			loaded[d.ID] = loadedHarness{descriptor: d, source: source}
		}
	}
	return loaded, nil
}

func validateHarnessDescriptor(d HarnessDescriptor) error {
	if d.SchemaVersion != harnessSchemaVersion {
		return fmt.Errorf("schema_version must be %d", harnessSchemaVersion)
	}
	if !harnessIDPattern.MatchString(d.ID) {
		return fmt.Errorf("id must match %s", harnessIDPattern.String())
	}
	if d.Display.Name == "" {
		return fmt.Errorf("display.name is required")
	}
	if d.Config.Path == "" || filepath.IsAbs(d.Config.Path) || !isSafeRelativePath(d.Config.Path) {
		return fmt.Errorf("config.path must be a workspace-relative path")
	}
	if len(d.Skills.Paths) == 0 {
		return fmt.Errorf("skills.paths is required")
	}
	if _, err := ParseForm(string(d.Skills.Form)); err != nil {
		return fmt.Errorf("skills.form: %w", err)
	}
	for _, path := range d.Skills.Paths {
		if path == "" || filepath.IsAbs(path) || !isSafeRelativePath(path) {
			return fmt.Errorf("skills.paths must contain workspace-relative paths")
		}
	}
	p := d.PreToolUse
	if len(p.Path) == 0 || p.MatcherKey == "" || p.HooksKey == "" {
		return fmt.Errorf("pre_tool_use.path, matcher_key, and hooks_key are required")
	}
	for _, key := range append(append([]string{}, p.Path...), p.MatcherKey, p.HooksKey) {
		if key == "" || key == "." || key == ".." {
			return fmt.Errorf("pre_tool_use field names must be non-empty object keys")
		}
	}
	if p.ResponseTemplate == "" {
		return fmt.Errorf("pre_tool_use.response_template is required")
	}
	if _, err := template.New("harness-response").Funcs(template.FuncMap{
		"toJson": func(any) string { return "" },
	}).Option("missingkey=error").Parse(p.ResponseTemplate); err != nil {
		return fmt.Errorf("pre_tool_use.response_template: %w", err)
	}
	if len(p.Entries) == 0 {
		return fmt.Errorf("pre_tool_use.entries is required")
	}
	for i, entry := range p.Entries {
		if entry.Matcher == "" {
			return fmt.Errorf("pre_tool_use.entries[%d].matcher is required", i)
		}
		if entry.Hook == nil || entry.Hook["type"] != "command" {
			return fmt.Errorf("pre_tool_use.entries[%d].hook.type must be command", i)
		}
		if _, claimed := entry.Hook["command"]; claimed {
			return fmt.Errorf("pre_tool_use.entries[%d].hook.command is reserved for Magus", i)
		}
	}
	for i, group := range d.ManagedEntries {
		if err := validateHarnessEntries(group); err != nil {
			return fmt.Errorf("managed_entries[%d]: %w", i, err)
		}
	}
	return nil
}

func validateHarnessEntries(group HarnessEntries) error {
	if len(group.Path) == 0 {
		return fmt.Errorf("path is required")
	}
	for _, key := range group.Path {
		if key == "" || key == "." || key == ".." {
			return fmt.Errorf("path must contain non-empty object keys")
		}
	}
	if len(group.Entries) == 0 {
		return fmt.Errorf("entries are required")
	}
	for i, entry := range group.Entries {
		if len(entry) == 0 {
			return fmt.Errorf("entries[%d] must be an object", i)
		}
	}
	return nil
}

func isSafeRelativePath(path string) bool {
	clean := filepath.Clean(path)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// ApplyHarness atomically merges exactly the descriptor-declared Magus adapter
// entries. User hooks with the same matcher remain untouched beside Magus's
// entry, and duplicate JSON keys are refused before a document is re-encoded.
func ApplyHarness(opts HarnessApplyOptions) (HarnessUpdate, error) {
	if opts.Root == "" {
		return HarnessUpdate{}, fmt.Errorf("workspace root is required")
	}
	if opts.ActingLease != "" {
		return HarnessUpdate{}, fmt.Errorf("a bound job (%s) cannot rewire a host harness; have its unbound orchestrator run the explicit apply", opts.ActingLease)
	}
	d, _, err := LoadHarness(opts.Root, opts.Host)
	if err != nil {
		return HarnessUpdate{}, err
	}
	path, err := harnessConfigPath(opts.Root, d.Config.Path)
	if err != nil {
		return HarnessUpdate{}, err
	}
	update := HarnessUpdate{Host: d.ID, Path: path}
	config := map[string]any{}
	if body, err := os.ReadFile(path); err == nil {
		if err := decodeHarnessJSON(body, &config); err != nil {
			return update, fmt.Errorf("parse existing JSON: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return update, err
	}
	changed, err := ensureDescriptorPreToolUse(config, d)
	if err != nil {
		return update, err
	}
	managedChanged, err := ensureManagedEntries(config, d.ManagedEntries)
	if err != nil {
		return update, err
	}
	changed = changed || managedChanged
	if !changed {
		return update, nil
	}
	update.Changed = true
	if opts.DryRun {
		update.Planned = true
		return update, nil
	}
	encoded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return update, fmt.Errorf("encode merged JSON: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return update, err
	}
	if err := writeHarnessAtomically(path, append(encoded, '\n')); err != nil {
		return update, err
	}
	return update, nil
}

// VerifyHarness validates both the descriptor and the concrete harness config.
// It reports an explicit status so callers cannot mistake an absent hook for a
// healthy one.
func VerifyHarness(root, host string) (HarnessVerification, error) {
	d, source, loadErr := LoadHarness(root, host)
	if loadErr != nil {
		return HarnessVerification{Host: host, Status: "uncovered", Reason: loadErr.Error()}, nil
	}
	path, pathErr := harnessConfigPath(root, d.Config.Path)
	if pathErr != nil {
		return HarnessVerification{}, pathErr
	}
	result := HarnessVerification{Host: d.ID, Descriptor: source, Path: path}
	body, readErr := os.ReadFile(path)
	if os.IsNotExist(readErr) {
		result.Status, result.Reason = "uncovered", "harness config does not exist"
		return result, nil
	}
	if readErr != nil {
		return HarnessVerification{}, readErr
	}
	config := map[string]any{}
	if decodeErr := decodeHarnessJSON(body, &config); decodeErr != nil {
		result.Status, result.Reason = "invalid", "parse existing JSON: "+decodeErr.Error()
		return result, nil
	}
	entries, entriesErr := descriptorEntries(config, d.PreToolUse)
	if entriesErr != nil {
		result.Status, result.Reason = "invalid", entriesErr.Error()
		return result, nil
	}
	for _, wanted := range d.PreToolUse.Entries {
		wantedHook := descriptorHook(d.ID, wanted)
		found := false
		for _, entry := range matchingDescriptorEntries(entries, d.PreToolUse.MatcherKey, wanted.Matcher) {
			hooks, ok := entry[d.PreToolUse.HooksKey].([]any)
			if !ok && entry[d.PreToolUse.HooksKey] != nil {
				result.Status, result.Reason = "invalid", fmt.Sprintf("existing hook entry %q is not an array", d.PreToolUse.HooksKey)
				return result, nil
			}
			if containsExactEntry(hooks, wantedHook) {
				found = true
				break
			}
		}
		if !found {
			result.Status, result.Reason = "uncovered", fmt.Sprintf("missing Magus adapter for matcher %q", wanted.Matcher)
			return result, nil
		}
	}
	result.Guarded = true
	for _, group := range d.ManagedEntries {
		entries, err := managedEntries(config, group)
		if err != nil {
			result.Status, result.Reason = "invalid", err.Error()
			return result, nil
		}
		for _, wanted := range group.Entries {
			if !containsExactEntry(entries, wanted) {
				result.Status, result.Reason = "uncovered", fmt.Sprintf("missing managed entry at %q", strings.Join(group.Path, "."))
				return result, nil
			}
		}
	}
	result.Status = "verified"
	return result, nil
}

// HarnessResponseTemplate returns the host response template belonging to a
// loaded collaborator. The adapter still supplies the host label to the
// canonical session hook; only response encoding is delegated.
func HarnessResponseTemplate(root, host string) (string, error) {
	d, _, err := LoadHarness(root, host)
	if err != nil {
		return "", err
	}
	return d.PreToolUse.ResponseTemplate, nil
}

// HarnessSkillsFor returns the selected form and locations for one harness.
func HarnessSkillsFor(root, host string) (HarnessSkills, error) {
	d, _, err := LoadHarness(root, host)
	if err != nil {
		return HarnessSkills{}, err
	}
	return d.Skills, nil
}

func ensureDescriptorPreToolUse(config map[string]any, d HarnessDescriptor) (bool, error) {
	entries, err := descriptorEntries(config, d.PreToolUse)
	if err != nil {
		return false, err
	}
	changed := false
	for _, wanted := range d.PreToolUse.Entries {
		matches := matchingDescriptorEntries(entries, d.PreToolUse.MatcherKey, wanted.Matcher)
		wantedHook := descriptorHook(d.ID, wanted)
		found := false
		for _, match := range matches {
			hooks, _ := match[d.PreToolUse.HooksKey].([]any)
			filtered := make([]any, 0, len(hooks))
			for _, raw := range hooks {
				hook, ok := raw.(map[string]any)
				if !ok {
					filtered = append(filtered, raw)
					continue
				}
				if containsExactEntry([]any{hook}, wantedHook) {
					found = true
					filtered = append(filtered, raw)
					continue
				}
				if command, ok := hook["command"].(string); ok && isHarnessHookCommand(d.ID, command) {
					changed = true
					continue
				}
				filtered = append(filtered, raw)
			}
			match[d.PreToolUse.HooksKey] = filtered
		}
		if found {
			continue
		}
		if len(matches) == 0 {
			entries = append(entries, map[string]any{
				d.PreToolUse.MatcherKey: wanted.Matcher,
				d.PreToolUse.HooksKey:   []any{wantedHook},
			})
			changed = true
			continue
		}
		first := matches[0]
		hooks, _ := first[d.PreToolUse.HooksKey].([]any)
		first[d.PreToolUse.HooksKey] = append(hooks, wantedHook)
		changed = true
	}
	if changed {
		setDescriptorEntries(config, d.PreToolUse.Path, entries)
	}
	return changed, nil
}

func ensureManagedEntries(config map[string]any, groups []HarnessEntries) (bool, error) {
	changed := false
	for _, group := range groups {
		entries, err := managedEntries(config, group)
		if err != nil {
			return false, err
		}
		groupChanged := false
		for _, wanted := range group.Entries {
			if containsExactEntry(entries, wanted) {
				continue
			}
			entries = append(entries, wanted)
			groupChanged = true
		}
		if groupChanged {
			setDescriptorEntries(config, group.Path, entries)
			changed = true
		}
	}
	return changed, nil
}

func descriptorEntries(config map[string]any, p HarnessPreToolUse) ([]any, error) {
	current := config
	for i, key := range p.Path {
		last := i == len(p.Path)-1
		value, present := current[key]
		if last {
			if !present || value == nil {
				return []any{}, nil
			}
			entries, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("existing pre_tool_use path %q is not an array", strings.Join(p.Path, "."))
			}
			return entries, nil
		}
		if !present || value == nil {
			return []any{}, nil
		}
		next, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("existing pre_tool_use path %q is not an object", strings.Join(p.Path[:i+1], "."))
		}
		current = next
	}
	return nil, fmt.Errorf("empty pre_tool_use path")
}

func managedEntries(config map[string]any, group HarnessEntries) ([]any, error) {
	current := config
	for i, key := range group.Path {
		last := i == len(group.Path)-1
		value, present := current[key]
		if last {
			if !present || value == nil {
				return []any{}, nil
			}
			entries, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("existing managed path %q is not an array", strings.Join(group.Path, "."))
			}
			return entries, nil
		}
		if !present || value == nil {
			return []any{}, nil
		}
		next, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("existing managed path %q is not an object", strings.Join(group.Path[:i+1], "."))
		}
		current = next
	}
	return nil, fmt.Errorf("empty managed path")
}

func setDescriptorEntries(config map[string]any, path []string, entries []any) {
	current := config
	for i, key := range path {
		if i == len(path)-1 {
			current[key] = entries
			return
		}
		next, ok := current[key].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[key] = next
		}
		current = next
	}
}

func matchingDescriptorEntries(entries []any, matcherKey, matcher string) []map[string]any {
	var matches []map[string]any
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if ok && entry[matcherKey] == matcher {
			matches = append(matches, entry)
		}
	}
	return matches
}

func descriptorHook(host string, entry HarnessHookEntry) map[string]any {
	extra := entry.Hook
	hook := make(map[string]any, len(extra)+1)
	for key, value := range extra {
		hook[key] = value
	}
	hook["command"] = HarnessHookCommand(host, entry.Observe)
	return hook
}

// HarnessHookCommand is the Magus-owned adapter command injected into host hook
// configs. It prefers the workspace binary found by walking up to magusfile.buzz,
// then falls back to PATH for installs without a checked-out binary.
func HarnessHookCommand(host string, observe bool) string {
	args := "agent hook --host " + host
	if observe {
		args += " --observe"
	}
	return `guard_root=$PWD; while [ -n "$guard_root" ]; do if [ -f "$guard_root/magusfile.buzz" ]; then if [ -x "$guard_root/magus" ]; then exec "$guard_root/magus" ` + args + `; fi; break; fi; guard_root=${guard_root%/*}; done; exec magus ` + args
}

func isHarnessHookCommand(host, command string) bool {
	return command == "magus agent hook --host "+host ||
		command == "magus agent hook --host "+host+" --observe" ||
		command == HarnessHookCommand(host, false) ||
		command == HarnessHookCommand(host, true)
}

func containsExactEntry(entries []any, wanted map[string]any) bool {
	want, err := json.Marshal(wanted)
	if err != nil {
		return false
	}
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		got, err := json.Marshal(entry)
		if err == nil && bytes.Equal(got, want) {
			return true
		}
	}
	return false
}

func harnessConfigPath(root, rel string) (string, error) {
	workspace, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(workspace, rel)
	inside, err := filepath.Rel(workspace, path)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("harness config path escapes the workspace")
	}
	current := workspace
	for _, part := range strings.Split(inside, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("harness config path contains symlink %q", current)
		}
	}
	return path, nil
}

// decodeHarnessJSON keeps large numeric values exact and rejects duplicate keys
// before a merge could collapse an ambiguous user document on re-encode.
func decodeHarnessJSON(body []byte, dst any) error {
	return json.UnmarshalLossless(body, dst)
}

func writeHarnessAtomically(path string, body []byte) error {
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// KnownHarnesses is useful to generic UIs and tests without leaking a fixed
// provider list into the binary. It returns IDs in deterministic order.
func KnownHarnesses(root string) ([]string, error) {
	descriptors, err := loadHarnesses(root)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(descriptors))
	for id := range descriptors {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}
