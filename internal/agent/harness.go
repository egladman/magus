package agent

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

const harnessSchemaVersion = 2

// HarnessStatus is the explicit verification outcome for a harness config.
type HarnessStatus string

const (
	HarnessVerified HarnessStatus = "verified"
	// HarnessUncovered means a config that DECLARES guard wiring is missing it, or the
	// wired command produced no evidence it runs. Never confuse this with
	// HarnessSkillsOnly, which is a descriptor that never wired a guard at all.
	HarnessUncovered HarnessStatus = "uncovered"
	HarnessInvalid   HarnessStatus = "invalid"
	// HarnessSkillsOnly is a descriptor with no config path at all: there is
	// nothing wired to invoke magus, so there is no guard to be covered or
	// uncovered. Reported distinct from HarnessVerified so a skills-only
	// descriptor can never read as "the guard runs here": the single most
	// misleading verdict this status could give.
	HarnessSkillsOnly HarnessStatus = "skills-only"
	// HarnessUnprobed means presence matched (the config carries the declared
	// fragments) but VerifyHarness could not confirm the wired command actually
	// answers: the interpreter, jq, or the magus binary the guard script would
	// resolve is missing from this environment, or the probe could not start or
	// finish within its deadline. Distinct from HarnessVerified, because presence
	// was never proof the guard runs, and distinct from HarnessUncovered, because
	// the gap is this machine's tooling or load, not the config.
	HarnessUnprobed HarnessStatus = "unprobed"
)

var (
	harnessIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	// magusGuardInvocation requires magus as a program token before the verb, so
	// `echo shell` and similar do not count as coverage.
	//
	// It admits `session` as well as `shell` because a descriptor may wire any magus
	// command a host should reach; the guard moved to `shell`, and the session verbs it
	// left behind are still legitimate wiring for a host that records rather than judges.
	magusGuardInvocation = regexp.MustCompile(`(?:^|[^\w.-])magus(?:\s+|$)[^;\n]*\b(?:shell|session)\b`)
)

// HarnessDescriptor is a user-owned collaborator contract. Magus prints the
// opaque config fragments a descriptor declares; it does not inject a command
// or a reply codec. Transport lives in host-native glue (shipped scripts or a
// plugin) that already names magus shell. Optional MCP client wiring is a
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
	Prompts        []HarnessPrompt  `json:"prompts,omitempty"`
}

// HarnessDisplay is opaque metadata for host UIs. The core validates no
// provider identity or presentation convention.
type HarnessDisplay struct {
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
	Icon  string `json:"icon,omitempty"`
}

// HarnessConfig identifies the workspace-local JSON document a descriptor's
// fragments merge into. Path must be relative to the workspace when set. Empty Path
// is skills-only: there is nothing to merge, and verify does not claim guard
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

// HarnessVerification makes coverage gaps explicit. A missing or invalid
// descriptor is not coverage; neither is a configuration that merely contains
// a string resembling Magus.
type HarnessVerification struct {
	ID         string        `json:"id"`
	Descriptor string        `json:"descriptor"`
	Path       string        `json:"path"`
	Status     HarnessStatus `json:"status"`
	Reason     string        `json:"reason,omitempty"`
	Guarded    bool          `json:"guarded,omitempty"`
	MCPStatus  HarnessStatus `json:"mcp_status,omitempty"`
	MCPReason  string        `json:"mcp_reason,omitempty"`
	// PromptStatus is whether the host-native approval prompts the descriptor keeps are in
	// place, empty when it keeps none. Uncovered means an ask verdict reaches nobody there,
	// so the templates refuse the call instead.
	PromptStatus HarnessStatus `json:"prompt_status,omitempty"`
	PromptReason string        `json:"prompt_reason,omitempty"`
}

// HarnessSpellLoader resolves a harness descriptor from a magusfile-selected
// harness spell (magus\harness.provider). Registered by the bindings layer so agent
// stays free of the Buzz VM. A miss (false) means no spell answers for that id;
// LoadHarness has no other source to fall back to.
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
// (any registered harness spell wins), which some unit tests rely on when they
// register a fake loader directly rather than going through a magusfile.
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

// LoadHarness resolves one validated descriptor, always from a harness spell:
// there is no other source. When the context carries ContextWithWiredHarnesses,
// a harness spell answers only if that id is wired; otherwise any registered
// harness spell that exports harness_config answers (tests and callers that
// have not inspected the magusfile). A miss is an error either way.
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
	err = harnessMissingError(id)
	return
}

// harnessMissingError explains a miss: no spell is wired for id (or none is
// registered at all). There is no compat JSON fallback left to check.
func harnessMissingError(id string) error {
	return fmt.Errorf("no harness named %q (wire magus\\harness.provider(<spell>) in the root magusfile)", id)
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
	for i, p := range d.Prompts {
		if err := validateHarnessPrompt(p); err != nil {
			return fmt.Errorf("prompts[%d]: %w", i, err)
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
	var commands []string
	for i, entry := range group.Entries {
		if len(entry) == 0 {
			return fmt.Errorf("entries[%d] must be an object", i)
		}
		if !ownedByMagus(entry) {
			matcher, cmds := EntryCommands(entry)
			return fmt.Errorf("entries[%d] (matcher %q, commands %q) runs no shipped template and carries no ownership marker, so a merge would take it for the person's own and never retire it; end its command with %q",
				i, matcher, cmds, " "+types.HarnessOwnedMarker)
		}
		collectCommands(entry, &commands)
	}
	// One invoking command per group, not every command: an entry may prepare the
	// environment a magus is found in, like a host's session-start entry that puts the
	// session root on PATH, and it can only do that without running one.
	if !slices.ContainsFunc(commands, invokesMagus) {
		return fmt.Errorf("command %q does not invoke magus (want a shipped guard script, magus shell, or magus session)", commands[0])
	}
	return nil
}

func isSafeRelativePath(path string) bool {
	clean := filepath.Clean(path)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// PlanHarness computes what the host files under root need for descriptor id to be current,
// and writes nothing. Entries a person added beside the managed ones stay in the plan's
// fragments where they are. An existing config that is not JSON, a prompt key someone set to
// another value, and a config that names no magus command are errors. MCP setup guidance is
// carried as a hint only: magus never plans host MCP client config and never resolves its
// secret ref.
func PlanHarness(ctx context.Context, root, id string) (types.HarnessPlan, error) {
	if err := ctx.Err(); err != nil {
		return types.HarnessPlan{}, err
	}
	if root == "" {
		return types.HarnessPlan{}, fmt.Errorf("workspace root is required")
	}
	d, _, err := LoadHarness(ctx, root, id)
	if err != nil {
		return types.HarnessPlan{}, err
	}
	plan := types.HarnessPlan{ID: d.ID, Wired: []types.HarnessWired{}}
	if d.Config.Path != "" {
		for _, group := range d.ManagedEntries {
			plan.Wired = append(plan.Wired, types.HarnessWired{File: d.Config.Path, Key: strings.Join(group.Path, "."), Entries: group.Entries})
		}
		file, err := planHarnessConfig(root, d)
		if err != nil {
			return plan, err
		}
		if err := addPlanFile(&plan, d.Config.Path, file); err != nil {
			return plan, err
		}
	}
	for _, p := range d.Prompts {
		file, err := planHarnessPrompt(root, p)
		if err != nil {
			return plan, err
		}
		if err := addPlanFile(&plan, p.Path, file); err != nil {
			return plan, err
		}
	}
	hint, err := harnessMCPHint(d)
	if err != nil {
		return plan, err
	}
	plan.MCPHint = hint
	plan.Merge = harnessMergeCommand(d.ID, plan.Files)
	return plan, nil
}

func planHarnessConfig(root string, d HarnessDescriptor) (types.HarnessFile, error) {
	path, err := harnessConfigPath(root, d.Config.Path)
	if err != nil {
		return types.HarnessFile{}, err
	}
	var file types.HarnessFile
	config := map[string]any{}
	if body, err := os.ReadFile(path); err == nil {
		file.Exists = true
		if err := decodeHarnessJSON(body, &config); err != nil {
			return file, fmt.Errorf("parse existing JSON: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return file, fmt.Errorf("agent: read harness config %s: %w", path, err)
	}
	fragment := map[string]any{}
	for _, key := range slices.Sorted(maps.Keys(d.ConfigDefaults)) {
		if _, present := config[key]; present {
			continue
		}
		fragment[key] = d.ConfigDefaults[key]
		file.Changes = append(file.Changes, types.HarnessChange{Op: types.HarnessSet, Key: key, Value: d.ConfigDefaults[key]})
	}
	for _, group := range d.ManagedEntries {
		entries, changes, err := mergeManagedGroup(config, group)
		if err != nil {
			return file, err
		}
		if len(changes) == 0 {
			continue
		}
		setDescriptorEntries(fragment, group.Path, entries)
		file.Changes = append(file.Changes, changes...)
	}
	if len(file.Changes) > 0 {
		file.Fragment = fragment
		return file, nil
	}
	if file.Exists && !configInvokesMagus(config) {
		// Naming a config path is the obligation, not declaring entries. A descriptor
		// that contributes no fragment still claims this file is the host's guard
		// wiring, and a config nothing in it calls magus from is coverage in name only.
		return file, fmt.Errorf("%s does not invoke magus", d.Config.Path)
	}
	return file, nil
}

// addPlanFile records in p what path needs, folding it into what another source of the
// same descriptor already asked of that file. A file needing nothing is left out.
func addPlanFile(p *types.HarnessPlan, path string, file types.HarnessFile) error {
	if len(file.Changes) == 0 {
		return nil
	}
	if p.Files == nil {
		p.Files = map[string]types.HarnessFile{}
	}
	have, ok := p.Files[path]
	if !ok {
		p.Files[path] = file
		return nil
	}
	if have.Content != "" || file.Content != "" {
		return fmt.Errorf("%s: a descriptor may own a whole file or keys inside it, not both", path)
	}
	have.Fragment = mergeFragment(have.Fragment, file.Fragment)
	have.Changes = append(have.Changes, file.Changes...)
	p.Files[path] = have
	return nil
}

// mergeFragment folds src into dst with merge\deep's rules, so two fragments for one file
// merge the way the printed command merges each into the file. Neither input is modified.
func mergeFragment(dst, src map[string]any) map[string]any {
	// MergeDeep never fails, and two objects always merge into an object.
	merged, _ := std.MergeDeep(context.Background(), dst, src)
	m, ok := merged.(map[string]any)
	if !ok {
		panic(fmt.Sprintf("agent: merging two objects gave %T", merged))
	}
	return m
}

// harnessMergeScript reads a `describe harness -o json` plan on stdin and writes every
// file it names. It merges text with merge\json, so a key outside the fragment keeps the
// digits of an integer past 2^53. The fragment itself arrives through json\parse, so an
// integer that large inside it, a person's own entry in a managed array included, does
// not. No single quotes: the command wraps this in one POSIX single-quoted word.
const harnessMergeScript = `import "encoding/json"; import "fs"; import "io"; import "merge"; fun main(args: [str]) > void !> str { final plan = json\parse(io\stdin.readAll()); foreach (path, file in plan["files"]) { final dir = fs\dirname(path); if (dir != "" and dir != ".") { fs\mkdirAll(dir); } if (file["content"] != null) { fs\writeFileAtomic(path, content: file["content"]); } else if (file["exists"]) { fs\writeFileAtomic(path, content: merge\json(fs\readFile(path), overlay: json\stringify(file["fragment"]))); } else { fs\writeFileAtomic(path, content: json\stringify(file["fragment"], indent: "  ") + "\n"); } } }`

// harnessMergeCommand renders the one command a person runs to bring files current.
// It reads the plan back from `magus describe harness`, so re-running it after a
// partial failure redoes only what is still missing.
func harnessMergeCommand(id string, files map[string]types.HarnessFile) string {
	if len(files) == 0 {
		return ""
	}
	return "magus describe harness " + id + " -o json | magus buzz -e " + posixQuote(harnessMergeScript)
}

func posixQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// VerifyHarness validates both the descriptor and the concrete harness config.
// It reports an explicit status so callers cannot mistake an absent hook for a
// healthy one. Coverage is a config that still carries the declared fragments
// and invokes magus somehow (a shipped script basename, magus shell, or
// magus session). A skills-only descriptor has nothing to wire. PromptStatus reports the
// host-native approval prompts separately, since a host can guard every call and still
// never ask the person about one.
func VerifyHarness(ctx context.Context, root, id string) (HarnessVerification, error) {
	if err := ctx.Err(); err != nil {
		return HarnessVerification{}, err
	}
	d, source, loadErr := LoadHarness(ctx, root, id)
	if loadErr != nil {
		//nolint:nilerr // a missing descriptor is a coverage verdict, carried in Reason, not a command failure
		return HarnessVerification{ID: id, Status: HarnessUncovered, Reason: loadErr.Error()}, nil
	}
	result, err := verifyHarnessConfig(ctx, root, d, source)
	if err == nil {
		verifyHarnessPrompts(root, d, &result)
	}
	return result, err
}

func verifyHarnessConfig(ctx context.Context, root string, d HarnessDescriptor, source string) (HarnessVerification, error) {
	result := HarnessVerification{ID: d.ID, Descriptor: source}
	if d.Config.Path == "" {
		// A descriptor that names no config.path (skills-only) has wired no guard
		// at all: validateHarnessDescriptor already refuses managed_entries
		// without a config.path, so there is nothing here to have run. Reporting this as
		// HarnessVerified was the exact bug: every deny and advise rule is unenforced for
		// this collaborator, and "verified" is the one word that says otherwise.
		result.Status = HarnessSkillsOnly
		result.Reason = "descriptor declares no guard config (skills-only): nothing is wired to invoke magus, so there is nothing to cover"
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
		//nolint:nilerr // an unparseable host config is an invalid verdict, carried in Reason, not a command failure
		return result, nil
	}
	for _, group := range d.ManagedEntries {
		entries, err := pathEntries(config, group.Path)
		if err != nil {
			result.Status, result.Reason = HarnessInvalid, err.Error()
			verifyHarnessMCP(d, &result)
			//nolint:nilerr // a malformed managed-entry path is an invalid verdict, carried in Reason, not a command failure
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
	// Presence is not proof: a config that carries the declared fragments verbatim
	// still needs the wired command to actually answer. probeHarnessCommands runs it
	// for real, against a synthetic event, and only THAT earns HarnessVerified.
	result.Status, result.Reason = probeHarnessCommands(ctx, root, config)
	result.Guarded = result.Status == HarnessVerified
	verifyHarnessMCP(d, &result)
	return result, nil
}

func LoadHarnessSkills(ctx context.Context, root, id string) (HarnessSkills, error) {
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

// mergeManagedGroup returns group's array in config as it reads holding exactly the declared
// entries plus the person's own, and what that changes. The person's entries keep their
// positions, an entry a descriptor wrote that no declared entry takes the place of is
// retired, and declared entries nothing matched are appended. config is not modified.
func mergeManagedGroup(config map[string]any, group HarnessEntries) ([]any, []types.HarnessChange, error) {
	existing, err := pathEntries(config, group.Path)
	if err != nil {
		return nil, nil, err
	}
	claimed, ops, err := claimExisting(existing, group.Entries)
	if err != nil {
		return nil, nil, err
	}
	entries := make([]any, 0, len(existing)+len(group.Entries))
	var retired []any
	for j, raw := range existing {
		if i, ok := claimed[j]; ok {
			entries = append(entries, group.Entries[i])
			continue
		}
		if entry, ok := raw.(map[string]any); ok && ownedByMagus(entry) {
			retired = append(retired, raw)
			continue
		}
		entries = append(entries, raw)
	}
	key := strings.Join(group.Path, ".")
	var changes []types.HarnessChange
	for i, wanted := range group.Entries {
		if ops[i] == types.HarnessAdd {
			entries = append(entries, wanted)
		}
		if ops[i] != "" {
			changes = append(changes, types.HarnessChange{Op: ops[i], Key: key, Value: wanted})
		}
	}
	for _, entry := range retired {
		changes = append(changes, types.HarnessChange{Op: types.HarnessRetire, Key: key, Value: entry})
	}
	return entries, changes, nil
}

// claimExisting pairs each declared entry with the existing entry it takes the place of:
// one equal to it, else the first with its managedIdentityKey. claimed maps an index into
// existing to one into wanted. ops[i] is empty for an equal entry, HarnessReplace for an
// identity match and HarnessAdd for an entry nothing matched.
func claimExisting(existing []any, wanted []map[string]any) (claimed map[int]int, ops []types.HarnessChangeOp, err error) {
	claimed = make(map[int]int, len(wanted))
	ops = make([]types.HarnessChangeOp, len(wanted))
	for i, entry := range wanted {
		j, err := exactEntryIndex(existing, entry, claimed)
		if err != nil {
			return nil, nil, err
		}
		if j < 0 {
			ops[i] = types.HarnessAdd
			continue
		}
		claimed[j] = i
	}
	for i, entry := range wanted {
		if ops[i] != types.HarnessAdd {
			continue
		}
		identity := managedIdentityKey(entry)
		for j, raw := range existing {
			have, ok := raw.(map[string]any)
			if _, taken := claimed[j]; !ok || taken || managedIdentityKey(have) != identity {
				continue
			}
			claimed[j], ops[i] = i, types.HarnessReplace
			break
		}
	}
	return claimed, ops, nil
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
	j, err := exactEntryIndex(entries, wanted, nil)
	return j >= 0, err
}

// exactEntryIndex is the index of the first entry in entries equal to wanted and not in
// skip, or -1.
func exactEntryIndex(entries []any, wanted map[string]any, skip map[int]int) (int, error) {
	want, err := json.Marshal(wanted)
	if err != nil {
		return -1, fmt.Errorf("agent: marshal managed entry: %w", err)
	}
	for j, raw := range entries {
		entry, ok := raw.(map[string]any)
		if _, skipped := skip[j]; !ok || skipped {
			continue
		}
		got, err := json.Marshal(entry)
		if err != nil {
			return -1, fmt.Errorf("agent: marshal existing harness entry: %w", err)
		}
		if bytes.Equal(got, want) {
			return j, nil
		}
	}
	return -1, nil
}

// ownedByMagus reports whether a descriptor wrote entry, which is what lets a merge retire
// it once no declared entry takes its place. Without that, managedIdentityKey being built
// from the command means any rewrite of a command appends the new entry beside the old,
// and every tool call is then judged twice, once by wiring the tree replaced.
//
// Two marks say so, and validateHarnessEntries refuses a declared entry carrying neither:
// a command that runs a template magus ships, whose path is magus's own, or one ending in
// types.HarnessOwnedMarker. Anything else is the person's, whatever it mentions.
func ownedByMagus(entry map[string]any) bool {
	var commands []string
	collectCommands(entry, &commands)
	return slices.ContainsFunc(commands, func(command string) bool {
		return runsAShippedTemplate(command) || carriesOwnedMarker(command)
	})
}

// carriesOwnedMarker reports whether command ends in the marker as a shell comment. A
// marker with no blank before it is part of a word, which the shell does run.
func carriesOwnedMarker(command string) bool {
	before, found := strings.CutSuffix(strings.TrimRight(command, " \t"), types.HarnessOwnedMarker)
	return found && strings.TrimRight(before, " \t") != before
}

// runsAShippedTemplate reports whether command names a template this repository ships.
// Narrower than invokesMagus on purpose: that one also answers true for a bare
// `magus ...` line, which says nothing about who wrote it.
func runsAShippedTemplate(command string) bool {
	for _, template := range []string{"magus-command", "magus-path", "magus-observe", "cursor-hook.", "magus-checkpoint", "magus-rehydrate", "magus-session.buzz"} {
		if strings.Contains(command, template) {
			return true
		}
	}
	return false
}

// managedIdentityKey is matcher/match (when present) plus the collected command strings,
// the key under which a statusMessage or timeout edit replaces an entry in place.
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

// EntryCommands is the matcher a managed entry fires on (empty when it names none) and every
// command it runs, in a stable order, for a reader who wants the wiring without the host
// JSON around it.
func EntryCommands(entry map[string]any) (matcher string, commands []string) {
	collectCommands(entry, &commands)
	return cmp.Or(stringField(entry, "matcher"), stringField(entry, "match")), commands
}

func collectCommands(v any, out *[]string) {
	switch t := v.(type) {
	case map[string]any:
		if command, ok := t["command"].(string); ok && command != "" {
			*out = append(*out, command)
		}
		// Sorted keys, so a rendering of the commands reads the same on every run.
		for _, key := range slices.Sorted(maps.Keys(t)) {
			collectCommands(t[key], out)
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
// doctor's guardTemplateBasenames plus magus-observe), or a magus
// session/session-hook invocation. A generic *-guard.sh does not count.
func invokesMagus(command string) bool {
	switch {
	case strings.Contains(command, "magus-command"),
		strings.Contains(command, "magus-path"),
		strings.Contains(command, "magus-observe"):
		return true
	case strings.Contains(command, "cursor-hook."):
		return true
	case strings.Contains(command, "magus-checkpoint"):
		return true
	case strings.Contains(command, "magus-rehydrate"):
		return true
	case magusGuardInvocation.MatchString(command):
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
	link, err := firstSymlinkComponent(workspace, inside)
	if err != nil {
		return "", err
	}
	if link != "" {
		return "", fmt.Errorf("harness config path contains symlink %q", link)
	}
	return path, nil
}

// firstSymlinkComponent returns the first existing component of rel under dir
// that is a symlink, and "" when there is none. Cleaning a path is lexical and
// sees no symlink, so every caller that resolves a caller-supplied relative path
// before writing or deleting through it walks the components with Lstat. Shared
// so the write side and the delete side cannot harden differently.
func firstSymlinkComponent(dir, rel string) (string, error) {
	current := dir
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return "", nil
		}
		if err != nil {
			return "", fmt.Errorf("agent: stat %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return current, nil
		}
	}
	return "", nil
}

// decodeHarnessJSON keeps large numeric values exact and rejects duplicate keys
// before a merge could collapse an ambiguous user document on re-encode.
func decodeHarnessJSON(body []byte, dst any) error {
	return json.UnmarshalLossless(body, dst)
}

// KnownHarnesses is useful to generic UIs and tests without leaking a fixed
// provider list into the binary. It returns IDs in deterministic order.
//
// wired are magusfile-selected harness spell names (Magus.Harnesses()) and are
// the only source of an ID: there is no compat JSON directory left to union
// them against, so a caller that wants a spell to appear must pass its id. A
// workspace may wire several hosts; bouncing between LLM providers is the
// intended case.
//
// Omitting wired (or passing a nil/empty slice) means no known harnesses,
// which is fine. A blank entry inside wired is not: empty and whitespace-only
// names are rejected rather than skipped, so a bad wiring cannot disappear
// silently.

// HarnessConfigPaths resolves the config file of every known harness that has one in this
// checkout, skipping any that cannot be resolved or is not there.
//
// It deliberately does not verify: VerifyHarness RUNS the wired command, so it reports
// nothing for a checkout whose wired interpreter is missing, which is exactly the state a
// caller asking which configs exist needs to inspect.
func HarnessConfigPaths(ctx context.Context, root string, wired ...string) []string {
	ids, err := KnownHarnesses(ctx, wired...)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		d, _, err := LoadHarness(ctx, root, id)
		if err != nil || d.Config.Path == "" {
			continue
		}
		path, err := harnessConfigPath(root, d.Config.Path)
		if err != nil {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		out = append(out, path)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// KnownHarnesses returns the wired harness ids trimmed, deduplicated and
// sorted. An empty id is an error, since it names no spell to load.
func KnownHarnesses(ctx context.Context, wired ...string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(wired))
	ids := make([]string, 0, len(wired))
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
