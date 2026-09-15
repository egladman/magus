package agent

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/json"
)

const (
	harnessSchemaVersion = 2
	harnessDirName       = "harnesses"
)

// HarnessStatus is the explicit verification outcome for a harness config.
type HarnessStatus string

const (
	HarnessVerified  HarnessStatus = "verified"
	HarnessUncovered HarnessStatus = "uncovered"
	HarnessInvalid   HarnessStatus = "invalid"
)

var (
	harnessIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	// magusSessionInvocation requires magus as a program token before session,
	// so `echo session hook` and similar do not count as coverage.
	magusSessionInvocation = regexp.MustCompile(`(?:^|[^\w.-])magus(?:\s+|$)[^;\n]*\bsession(?:\s+hook\b|\b)`)
)

// HarnessDescriptor is a user-owned collaborator contract. Magus merges the
// opaque config fragments a descriptor declares; it does not inject a command
// or a reply codec. Transport lives in host-native glue (shipped scripts or a
// plugin) that already names session hook. Optional MCP client wiring is a
// separate document (or register hint), always bound to a secret ref.
type HarnessDescriptor struct {
	SchemaVersion  int              `json:"schema_version"`
	ID             string           `json:"id"`
	Display        HarnessDisplay   `json:"display"`
	Config         HarnessConfig    `json:"config"`
	ConfigDefaults map[string]any   `json:"config_defaults,omitempty"`
	Skills         HarnessSkills    `json:"skills"`
	ManagedEntries []HarnessEntries `json:"managed_entries,omitempty"`
	MCP            *HarnessMCP      `json:"mcp,omitempty"`
}

// HarnessDisplay is opaque metadata for host UIs. The core validates no
// provider identity or presentation convention.
type HarnessDisplay struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
	Icon  string `json:"icon,omitempty"`
}

// HarnessConfig identifies the workspace-local JSON document maintained by a
// descriptor. Path must be relative to the workspace when set. Empty Path is
// skills-only: apply has nothing to write, and verify does not claim guard
// coverage from a missing config.
type HarnessConfig struct {
	Path string `json:"path"`
}

// HarnessSkills lists workspace-relative locations this collaborator reads.
type HarnessSkills struct {
	Paths []string `json:"paths"`
	Form  Form     `json:"form"`
}

// HarnessEntries declares exact host-config fragments to merge. Each entry is
// opaque host JSON: Magus does not rewrite a command field.
type HarnessEntries struct {
	Path    []string         `json:"path"`
	Entries []map[string]any `json:"entries"`
}

// HarnessUpdate records an explicit, narrow harness mutation.
type HarnessUpdate struct {
	ID         string `json:"id"`
	Path       string `json:"path"`
	Changed    bool   `json:"changed"`
	Planned    bool   `json:"planned,omitempty"`
	MCPHint string `json:"mcp_hint,omitempty"`
}

// HarnessApplyOptions contains the sole authority needed to mutate a harness.
// ActingLease must come from trusted command ingress, never a late environment
// read in a nested process.
type HarnessApplyOptions struct {
	Root        string
	ID          string
	DryRun      bool
	ActingLease string
}

// HarnessVerification makes coverage gaps explicit. A missing or invalid
// descriptor is not coverage; neither is a configuration that merely contains
// a string resembling Magus.
type HarnessVerification struct {
	ID         string        `json:"id"`
	Descriptor string        `json:"descriptor"`
	Path       string        `json:"path"`
	Status     HarnessStatus `json:"status"`
	Reason     string        `json:"reason,omitempty"`
	Guarded   bool          `json:"guarded,omitempty"`
	MCPStatus HarnessStatus `json:"mcp_status,omitempty"`
	MCPReason string        `json:"mcp_reason,omitempty"`
}

// HarnessSpellLoader resolves a harness descriptor from a magusfile-selected
// harness spell (magus\harness.provider). Registered by the bindings layer so agent
// stays free of the Buzz VM. A miss (false) falls through to JSON descriptors.
type HarnessSpellLoader func(ctx context.Context, id string) (HarnessDescriptor, string, bool, error)

var harnessSpellLoader HarnessSpellLoader

// RegisterHarnessSpellLoader installs the spell-backed harness resolver. Called
// once from bindings init.
func RegisterHarnessSpellLoader(fn HarnessSpellLoader) {
	harnessSpellLoader = fn
}

type wiredHarnessesKey struct{}

// ContextWithWiredHarnesses attaches magusfile-selected harness IDs so
// LoadHarness prefers a harness spell only when that id was wired via
// magus\harness.provider. Callers that omit this keep the prior behavior
// (any registered harness spell wins), which unit tests that load JSON only rely on.
func ContextWithWiredHarnesses(ctx context.Context, ids []string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, wiredHarnessesKey{}, slices.Clone(ids))
}

func wiredHarnessesFromContext(ctx context.Context) (ids []string, ok bool) {
	ids, ok = ctx.Value(wiredHarnessesKey{}).([]string)
	return ids, ok
}

// LoadHarness resolves one validated descriptor. When the context carries
// ContextWithWiredHarnesses, a harness spell wins only if that id is wired;
// otherwise any registered harness spell that exports harness_config still
// wins over JSON (tests and callers that have not inspected the magusfile).
// Workspace-owned JSON still outranks user-global JSON when no spell applies.
func LoadHarness(ctx context.Context, root, id string) (descriptor HarnessDescriptor, source string, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	if root == "" {
		err = fmt.Errorf("workspace root is required to load a harness")
		return
	}
	if !harnessIDPattern.MatchString(id) {
		err = fmt.Errorf("invalid harness ID %q", id)
		return
	}
	trySpell := true
	if wired, set := wiredHarnessesFromContext(ctx); set {
		trySpell = slices.Contains(wired, id)
	}
	if trySpell && harnessSpellLoader != nil {
		d, src, ok, loadErr := harnessSpellLoader(ctx, id)
		if loadErr != nil {
			err = loadErr
			return
		}
		if ok {
			if verr := validateHarnessDescriptor(d); verr != nil {
				err = fmt.Errorf("agent: invalid harness spell %q: %w", id, verr)
				return
			}
			descriptor, source = d, src
			return
		}
	}
	descriptors, loadErr := loadHarnesses(root)
	if loadErr != nil {
		err = loadErr
		return
	}
	d, ok := descriptors[id]
	if !ok {
		err = fmt.Errorf("no harness named %q (wire magus\\harness.provider(<spell>), or install a descriptor in harnesses/, .magus/harnesses/, or $XDG_CONFIG_HOME/magus/harnesses)", id)
		return
	}
	descriptor, source = d.descriptor, d.source
	return
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
			return nil, fmt.Errorf("agent: read harness descriptors in %s: %w", dir, err)
		}
		seen := map[string]bool{}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			source := filepath.Join(dir, entry.Name())
			body, err := os.ReadFile(source)
			if err != nil {
				return nil, fmt.Errorf("agent: read harness descriptor %s: %w", source, err)
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
	if d.Config.Path != "" {
		if filepath.IsAbs(d.Config.Path) || !isSafeRelativePath(d.Config.Path) {
			return fmt.Errorf("config.path must be a workspace-relative path")
		}
		if strings.EqualFold(filepath.Base(d.Config.Path), "mcp.json") {
			return fmt.Errorf("config.path %q looks like host MCP client config; Magus does not write MCP registration (use harness_mcp setup guidance instead)", d.Config.Path)
		}
	} else if len(d.ManagedEntries) > 0 {
		return fmt.Errorf("config.path is required when managed_entries is set")
	}
	if _, err := ParseForm(string(d.Skills.Form)); err != nil {
		return fmt.Errorf("skills.form: %w", err)
	}
	// Empty skills.paths is allowed when the collaborator reads only AGENTS.md.
	for _, path := range d.Skills.Paths {
		if path == "" || filepath.IsAbs(path) || !isSafeRelativePath(path) {
			return fmt.Errorf("skills.paths must contain workspace-relative paths")
		}
	}
	for i, group := range d.ManagedEntries {
		if err := validateHarnessEntries(group); err != nil {
			return fmt.Errorf("managed_entries[%d]: %w", i, err)
		}
	}
	if err := validateHarnessMCP(d.MCP); err != nil {
		return err
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
	var commands []string
	for i, entry := range group.Entries {
		if len(entry) == 0 {
			return fmt.Errorf("entries[%d] must be an object", i)
		}
		collectCommands(entry, &commands)
	}
	if len(commands) == 0 {
		return fmt.Errorf("entries must include at least one command that invokes magus")
	}
	for _, command := range commands {
		if !invokesMagus(command) {
			return fmt.Errorf("command %q does not invoke magus (want a shipped guard script, session hook, or magus session)", command)
		}
	}
	return nil
}

func isSafeRelativePath(path string) bool {
	clean := filepath.Clean(path)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// ApplyHarness atomically merges the descriptor-declared opaque fragments.
// User entries that are not an exact match remain untouched beside them.
// When the spell declares MCP setup guidance, apply records a hint for the
// CLI to print. Magus never writes host MCP client config and never resolves
// the MCP secret ref.
func ApplyHarness(ctx context.Context, opts HarnessApplyOptions) (HarnessUpdate, error) {
	if err := ctx.Err(); err != nil {
		return HarnessUpdate{}, err
	}
	if opts.Root == "" {
		return HarnessUpdate{}, fmt.Errorf("workspace root is required")
	}
	if opts.ActingLease != "" {
		return HarnessUpdate{}, fmt.Errorf("a bound job (%s) cannot rewire a host harness; have its unbound orchestrator run the explicit apply", opts.ActingLease)
	}
	d, _, err := LoadHarness(ctx, opts.Root, opts.ID)
	if err != nil {
		return HarnessUpdate{}, err
	}
	update := HarnessUpdate{ID: d.ID}

	if d.Config.Path != "" {
		path, err := harnessConfigPath(opts.Root, d.Config.Path)
		if err != nil {
			return HarnessUpdate{}, err
		}
		update.Path = path
		config := map[string]any{}
		existing := false
		if body, err := os.ReadFile(path); err == nil {
			existing = true
			if err := decodeHarnessJSON(body, &config); err != nil {
				return update, fmt.Errorf("parse existing JSON: %w", err)
			}
		} else if !os.IsNotExist(err) {
			return update, fmt.Errorf("agent: read harness config %s: %w", path, err)
		}
		changed := false
		for key, value := range d.ConfigDefaults {
			if _, present := config[key]; present {
				continue
			}
			config[key] = value
			changed = true
		}
		managedChanged, err := ensureManagedEntries(config, d.ManagedEntries)
		if err != nil {
			return update, err
		}
		changed = changed || managedChanged
		if changed {
			if opts.DryRun {
				update.Changed = true
				update.Planned = true
			} else {
				encoded, err := json.MarshalIndent(config, "", "  ")
				if err != nil {
					return update, fmt.Errorf("encode merged JSON: %w", err)
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return update, fmt.Errorf("agent: mkdir harness config dir: %w", err)
				}
				if _, err := harnessConfigPath(opts.Root, d.Config.Path); err != nil {
					return update, err
				}
				if err := writeHarnessAtomically(path, append(encoded, '\n')); err != nil {
					return update, fmt.Errorf("agent: write harness config %s: %w", path, err)
				}
				update.Changed = true
			}
		} else if !existing {
			if len(d.ManagedEntries) > 0 || len(d.ConfigDefaults) > 0 {
				return update, fmt.Errorf("descriptor %q declares no harness fragments to write", d.ID)
			}
		} else if len(d.ManagedEntries) > 0 && !configInvokesMagus(config) {
			return update, fmt.Errorf("config does not invoke magus")
		}
	}

	if err := applyHarnessMCP(d, &update); err != nil {
		return update, err
	}
	return update, nil
}

// VerifyHarness validates both the descriptor and the concrete harness config.
// It reports an explicit status so callers cannot mistake an absent hook for a
// healthy one. Coverage is a config that still carries the declared fragments
// and invokes magus somehow (a shipped script basename, session hook, or
// magus session). A skills-only descriptor has nothing to wire.
func VerifyHarness(ctx context.Context, root, id string) (HarnessVerification, error) {
	if err := ctx.Err(); err != nil {
		return HarnessVerification{}, err
	}
	d, source, loadErr := LoadHarness(ctx, root, id)
	if loadErr != nil {
		return HarnessVerification{ID: id, Status: HarnessUncovered, Reason: loadErr.Error()}, nil
	}
	result := HarnessVerification{ID: d.ID, Descriptor: source}
	if d.Config.Path == "" {
		result.Status = HarnessVerified
		verifyHarnessMCP(d, &result)
		return result, nil
	}
	path, pathErr := harnessConfigPath(root, d.Config.Path)
	if pathErr != nil {
		return HarnessVerification{}, pathErr
	}
	result.Path = path
	body, readErr := os.ReadFile(path)
	if os.IsNotExist(readErr) {
		result.Status, result.Reason = HarnessUncovered, "harness config does not exist"
		verifyHarnessMCP(d, &result)
		return result, nil
	}
	if readErr != nil {
		return HarnessVerification{}, fmt.Errorf("agent: read harness config %s: %w", path, readErr)
	}
	config := map[string]any{}
	if decodeErr := decodeHarnessJSON(body, &config); decodeErr != nil {
		result.Status, result.Reason = HarnessInvalid, "parse existing JSON: "+decodeErr.Error()
		verifyHarnessMCP(d, &result)
		return result, nil
	}
	for _, group := range d.ManagedEntries {
		entries, err := pathEntries(config, group.Path)
		if err != nil {
			result.Status, result.Reason = HarnessInvalid, err.Error()
			verifyHarnessMCP(d, &result)
			return result, nil
		}
		for _, wanted := range group.Entries {
			found, err := containsExactEntry(entries, wanted)
			if err != nil {
				return HarnessVerification{}, err
			}
			if !found {
				result.Status, result.Reason = HarnessUncovered, fmt.Sprintf("missing managed entry at %q", strings.Join(group.Path, "."))
				verifyHarnessMCP(d, &result)
				return result, nil
			}
		}
	}
	if !configInvokesMagus(config) {
		result.Status, result.Reason = HarnessUncovered, "config does not invoke magus"
		verifyHarnessMCP(d, &result)
		return result, nil
	}
	result.Guarded = true
	result.Status = HarnessVerified
	verifyHarnessMCP(d, &result)
	return result, nil
}

func HarnessSkillsFor(ctx context.Context, root, id string) (HarnessSkills, error) {
	if err := ctx.Err(); err != nil {
		return HarnessSkills{}, err
	}
	d, _, err := LoadHarness(ctx, root, id)
	if err != nil {
		return HarnessSkills{}, err
	}
	return d.Skills, nil
}

func pathEntries(config map[string]any, path []string) ([]any, error) {
	current := config
	for i, key := range path {
		last := i == len(path)-1
		value, present := current[key]
		if last {
			if !present || value == nil {
				return []any{}, nil
			}
			entries, ok := value.([]any)
			if !ok {
				return nil, fmt.Errorf("existing path %q is not an array", strings.Join(path, "."))
			}
			return entries, nil
		}
		if !present || value == nil {
			return []any{}, nil
		}
		next, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("existing path %q is not an object", strings.Join(path[:i+1], "."))
		}
		current = next
	}
	return nil, fmt.Errorf("empty path")
}

func ensureManagedEntries(config map[string]any, groups []HarnessEntries) (bool, error) {
	changed := false
	for _, group := range groups {
		entries, err := pathEntries(config, group.Path)
		if err != nil {
			return false, err
		}
		groupChanged := false
		for _, wanted := range group.Entries {
			exact, err := containsExactEntry(entries, wanted)
			if err != nil {
				return false, err
			}
			if exact {
				continue
			}
			replaced := false
			for i, raw := range entries {
				entry, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if sameManagedIdentity(entry, wanted) {
					entries[i] = wanted
					groupChanged = true
					replaced = true
					break
				}
			}
			if !replaced {
				entries = append(entries, wanted)
				groupChanged = true
			}
		}
		if groupChanged {
			setDescriptorEntries(config, group.Path, entries)
			changed = true
		}
	}
	return changed, nil
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

func containsExactEntry(entries []any, wanted map[string]any) (bool, error) {
	want, err := json.Marshal(wanted)
	if err != nil {
		return false, fmt.Errorf("agent: marshal managed entry: %w", err)
	}
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		got, err := json.Marshal(entry)
		if err != nil {
			return false, fmt.Errorf("agent: marshal existing harness entry: %w", err)
		}
		if bytes.Equal(got, want) {
			return true, nil
		}
	}
	return false, nil
}

// sameManagedIdentity treats matcher/match (when present) plus collected command
// strings as the stable key so a statusMessage or timeout edit replaces in place
// instead of appending a duplicate hook.
func sameManagedIdentity(existing, wanted map[string]any) bool {
	return managedIdentityKey(existing) == managedIdentityKey(wanted)
}

func managedIdentityKey(entry map[string]any) string {
	var b strings.Builder
	switch {
	case stringField(entry, "matcher") != "":
		b.WriteString("matcher=")
		b.WriteString(stringField(entry, "matcher"))
	case stringField(entry, "match") != "":
		b.WriteString("match=")
		b.WriteString(stringField(entry, "match"))
	}
	var commands []string
	collectCommands(entry, &commands)
	sorted := slices.Clone(commands)
	slices.Sort(sorted)
	b.WriteByte('|')
	b.WriteString(strings.Join(sorted, "\x00"))
	return b.String()
}

func stringField(entry map[string]any, key string) string {
	v, _ := entry[key].(string)
	return v
}

func collectCommands(v any, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		if command, ok := t["command"].(string); ok && command != "" {
			*out = append(*out, command)
		}
		for _, child := range t {
			collectCommands(child, out)
		}
	case []any:
		for _, child := range t {
			collectCommands(child, out)
		}
	}
}

func configInvokesMagus(config map[string]any) bool {
	var commands []string
	collectCommands(config, &commands)
	for _, command := range commands {
		if invokesMagus(command) {
			return true
		}
	}
	return false
}

// invokesMagus reports whether a host hook command actually calls Magus.
// Coverage is transport-shaped: shipped script basenames (aligned with
// doctor's guardTemplateBasenames plus magus-guard-observe), or a magus
// session/session-hook invocation. A generic *-guard.sh does not count.
func invokesMagus(command string) bool {
	switch {
	case strings.Contains(command, "magus-guard-"):
		return true
	case strings.Contains(command, "cursor-guard.sh"):
		return true
	case strings.Contains(command, "magus-checkpoint"):
		return true
	case strings.Contains(command, "magus-rehydrate"):
		return true
	case magusSessionInvocation.MatchString(command):
		return true
	default:
		return false
	}
}

func harnessConfigPath(root, rel string) (string, error) {
	workspace, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("agent: resolve workspace root: %w", err)
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
			return "", fmt.Errorf("agent: stat harness config path %s: %w", current, err)
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
		return fmt.Errorf("agent: stat harness config %s: %w", path, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return fmt.Errorf("agent: create temp harness config: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("agent: chmod temp harness config: %w", err)
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("agent: write temp harness config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("agent: sync temp harness config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("agent: close temp harness config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("agent: rename harness config into place: %w", err)
	}
	return nil
}

// KnownHarnesses is useful to generic UIs and tests without leaking a fixed
// provider list into the binary. It returns IDs in deterministic order.
//
// wired are magusfile-selected harness spell names (Magus.Harnesses()). They are
// unioned with JSON descriptors so a spell-only host still appears in
// doctor/improve without a harnesses/*.json file. A workspace may wire several
// hosts; bouncing between LLM providers is the intended case.
//
// Omitting wired (or passing a nil/empty slice) means no magusfile providers to
// union - that is fine. A blank entry inside wired is not: empty and whitespace-
// only names are rejected rather than skipped, so a bad AddHarness cannot
// disappear into the union.
func KnownHarnesses(ctx context.Context, root string, wired ...string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	descriptors, err := loadHarnesses(root)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(descriptors)+len(wired))
	ids := make([]string, 0, len(descriptors)+len(wired))
	for id := range descriptors {
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for _, id := range wired {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("agent: wired harness id is empty")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids, nil
}
