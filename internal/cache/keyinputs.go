package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"slices"
	"strings"
)

// KeyInputs is a snapshot of the fields that fed a Step's cache-key hash,
// captured at snapshot time and stored alongside the manifest. It exists so
// `magus describe target --cache` can explain a miss - which source file
// changed, which env var flipped, which upstream project moved, which tool
// or magus binary upgraded - without re-deriving history the manifest never
// recorded. It never participates in a cache lookup: hashStep's line
// encoding remains the sole source of truth for the key itself; ComputeKeyInputs
// describes the same inputs hashStep reads, it does not compute a different key.
//
// Env values never appear here in plain text, only a fingerprint, so a
// manifest does not become a new place secrets come to rest on disk.
type KeyInputs struct {
	KeyVersion      int           `json:"keyVersion"`
	SpellDefVersion string        `json:"spellDefVersion,omitempty"`
	Charms          []string      `json:"charms,omitempty"`
	ExtraArgs       []string      `json:"extraArgs,omitempty"`
	Sources         []SourceInput `json:"sources,omitempty"`
	EnvAllow        []EnvInput    `json:"envAllow,omitempty"`
	EnvPassthrough  []EnvInput    `json:"envPassthrough,omitempty"`
	ExecOverrides   []string      `json:"execOverrides,omitempty"`
	Deps            []string      `json:"deps,omitempty"`
	// DepsByProject maps each of Step.DependsOn to the hash of that upstream
	// project's most recent cache entry for the same target, as of the moment
	// ComputeKeyInputs ran. Called from snapshot() during a RunAll, upstream
	// projects have already snapshotted (topological order), so this is exact;
	// called later from `describe --cache`, it is a best-effort proxy for
	// "what upstream would hash to right now".
	DepsByProject map[string]string `json:"depsByProject,omitempty"`
	ToolVersions  []string          `json:"toolVersions,omitempty"`
}

// SourceInput is one hashed source file: its workspace-relative path,
// content hash, and executable bit - the same triple hashStep folds into a
// "src:" key line.
type SourceInput struct {
	Path string `json:"path"`
	Hash string `json:"hash"`
	Exec bool   `json:"exec,omitempty"`
}

// EnvInput is one environment variable that fed the key: its name, whether
// it was set, and a fingerprint of its value - never the value itself.
type EnvInput struct {
	Name      string `json:"name"`
	Set       bool   `json:"set"`
	ValueHash string `json:"valueHash,omitempty"`
}

// fingerprintEnvValue returns a short, non-reversible fingerprint of an env
// value, stored in place of the value itself so a describe --cache diff can
// report that a variable changed without persisting what it held.
func fingerprintEnvValue(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])[:12]
}

// ComputeKeyInputs gathers the same fields hashStep hashes for s, structured
// for comparison instead of folded into one digest. It mirrors hashStep's
// gathering exactly (same source walk, same env resolution, same sort order)
// so the two can never disagree about what the key depends on. It does not
// hash anything and does not change hashStep's algorithm or byte layout.
func (c *Cache) ComputeKeyInputs(ctx context.Context, s *Step) (*KeyInputs, error) {
	ki := &KeyInputs{
		KeyVersion:      keyVersion,
		SpellDefVersion: s.SpellDefVersion,
		Charms:          append([]string(nil), s.Charms...),
		ExtraArgs:       append([]string(nil), s.ExtraArgs...),
	}
	slices.Sort(ki.Charms)

	files, err := expandSources(s.Sources, s.WorkspaceRoot, s.Outputs, s.IgnoreDirs)
	if err != nil {
		return nil, err
	}
	hashes, modes, err := c.hashFiles(ctx, files)
	if err != nil {
		return nil, err
	}
	ki.Sources = make([]SourceInput, len(files))
	for i, f := range files {
		ki.Sources[i] = SourceInput{Path: f.rel, Hash: hashes[i], Exec: modes[i]&0o111 != 0}
	}

	env := append([]string(nil), s.EnvAllow...)
	slices.Sort(env)
	for _, k := range env {
		if v, ok := os.LookupEnv(k); ok {
			ki.EnvAllow = append(ki.EnvAllow, EnvInput{Name: k, Set: true, ValueHash: fingerprintEnvValue(v)})
		} else {
			ki.EnvAllow = append(ki.EnvAllow, EnvInput{Name: k, Set: false})
		}
	}

	for _, kv := range matchPassthroughEnv(s.EnvPassthrough, env, os.Environ()) {
		i := strings.IndexByte(kv, '=')
		if i < 0 {
			continue
		}
		ki.EnvPassthrough = append(ki.EnvPassthrough, EnvInput{Name: kv[:i], Set: true, ValueHash: fingerprintEnvValue(kv[i+1:])})
	}

	ki.ExecOverrides = append([]string(nil), s.ExecOverrides...)
	slices.Sort(ki.ExecOverrides)

	ki.Deps = append([]string(nil), s.Deps...)
	slices.Sort(ki.Deps)

	if len(s.DependsOn) > 0 {
		ki.DepsByProject = make(map[string]string, len(s.DependsOn))
		for _, dep := range s.DependsOn {
			m, _, derr := c.LastEntryForTarget(dep, s.Target)
			if derr != nil {
				m, _, derr = c.LastEntry(dep)
			}
			if derr == nil && m != nil {
				ki.DepsByProject[dep] = m.Hash
			}
		}
	}

	ki.ToolVersions = append([]string(nil), s.ToolVersions...)
	slices.Sort(ki.ToolVersions)

	return ki, nil
}

// DiffKeyInputs compares two KeyInputs snapshots - typically a stored
// entry's captured inputs against a freshly computed one - and returns one
// line per differing component, in the order a cache key folds them:
// keyVersion, spellDefVersion, charms, args, sources, env, exec overrides,
// deps, tool versions. An empty result means every captured component is
// identical: a real miss with no observed difference points at an input
// this version of magus does not capture (or a manifest written before
// KeyInputs existed on the other side of the comparison).
func DiffKeyInputs(prev, cur *KeyInputs) []string {
	var out []string

	if prev.KeyVersion != cur.KeyVersion {
		out = append(out, fmt.Sprintf("keyVersion: %d -> %d (magus upgrade; invalidates every entry)", prev.KeyVersion, cur.KeyVersion))
	}
	if prev.SpellDefVersion != cur.SpellDefVersion {
		out = append(out, fmt.Sprintf("spellDefVersion: %s -> %s (spell/op definitions changed, likely a magus upgrade)",
			shortHash(prev.SpellDefVersion), shortHash(cur.SpellDefVersion)))
	}

	for _, name := range diffAdded(prev.Charms, cur.Charms) {
		out = append(out, "charm added: "+name)
	}
	for _, name := range diffAdded(cur.Charms, prev.Charms) {
		out = append(out, "charm removed: "+name)
	}

	if !slices.Equal(prev.ExtraArgs, cur.ExtraArgs) {
		out = append(out, fmt.Sprintf("args changed: %v -> %v", prev.ExtraArgs, cur.ExtraArgs))
	}

	out = append(out, diffSources(prev.Sources, cur.Sources)...)
	out = append(out, diffEnv("env", prev.EnvAllow, cur.EnvAllow)...)
	// Labeled apart from EnvAllow: the two sets are disjoint by construction, and
	// a reader needs to know whether a declared opt-in or an ambient sandbox
	// passthrough moved the key - the remedies differ.
	out = append(out, diffEnv("env passthrough", prev.EnvPassthrough, cur.EnvPassthrough)...)

	for _, v := range diffAdded(prev.ExecOverrides, cur.ExecOverrides) {
		out = append(out, "exec override added: "+v)
	}
	for _, v := range diffAdded(cur.ExecOverrides, prev.ExecOverrides) {
		out = append(out, "exec override removed: "+v)
	}

	out = append(out, diffDeps(prev, cur)...)
	out = append(out, diffToolVersions(prev.ToolVersions, cur.ToolVersions)...)

	return out
}

// diffAdded returns the entries present in b but not a.
func diffAdded(a, b []string) []string {
	set := make(map[string]struct{}, len(a))
	for _, x := range a {
		set[x] = struct{}{}
	}
	var out []string
	for _, x := range b {
		if _, ok := set[x]; !ok {
			out = append(out, x)
		}
	}
	slices.Sort(out)
	return out
}

func diffSources(prev, cur []SourceInput) []string {
	prevBy := make(map[string]SourceInput, len(prev))
	for _, s := range prev {
		prevBy[s.Path] = s
	}
	curBy := make(map[string]SourceInput, len(cur))
	for _, s := range cur {
		curBy[s.Path] = s
	}
	var added, removed, changed []string
	for path, cs := range curBy {
		ps, ok := prevBy[path]
		if !ok {
			added = append(added, path)
			continue
		}
		if ps.Hash != cs.Hash || ps.Exec != cs.Exec {
			changed = append(changed, path)
		}
	}
	for path := range prevBy {
		if _, ok := curBy[path]; !ok {
			removed = append(removed, path)
		}
	}
	slices.Sort(added)
	slices.Sort(removed)
	slices.Sort(changed)

	out := make([]string, 0, len(changed)+len(added)+len(removed))
	for _, p := range changed {
		out = append(out, "src changed: "+p)
	}
	for _, p := range added {
		out = append(out, "src added: "+p)
	}
	for _, p := range removed {
		out = append(out, "src removed: "+p)
	}
	return out
}

func diffEnv(label string, prev, cur []EnvInput) []string {
	prevBy := make(map[string]EnvInput, len(prev))
	for _, e := range prev {
		prevBy[e.Name] = e
	}
	curBy := make(map[string]EnvInput, len(cur))
	for _, e := range cur {
		curBy[e.Name] = e
	}
	var added, removed, changed []string
	for name, ce := range curBy {
		pe, ok := prevBy[name]
		if !ok {
			added = append(added, name)
			continue
		}
		if pe.Set != ce.Set || pe.ValueHash != ce.ValueHash {
			changed = append(changed, name)
		}
	}
	for name := range prevBy {
		if _, ok := curBy[name]; !ok {
			removed = append(removed, name)
		}
	}
	slices.Sort(added)
	slices.Sort(removed)
	slices.Sort(changed)

	out := make([]string, 0, len(changed)+len(added)+len(removed))
	for _, n := range changed {
		out = append(out, label+" changed: "+n)
	}
	for _, n := range added {
		out = append(out, label+" added: "+n)
	}
	for _, n := range removed {
		out = append(out, label+" removed: "+n)
	}
	return out
}

func diffDeps(prev, cur *KeyInputs) []string {
	if len(prev.DepsByProject) > 0 || len(cur.DepsByProject) > 0 {
		var added, removed, changed []string
		for path, ch := range cur.DepsByProject {
			ph, ok := prev.DepsByProject[path]
			if !ok {
				added = append(added, path)
				continue
			}
			if ph != ch {
				changed = append(changed, path)
			}
		}
		for path := range prev.DepsByProject {
			if _, ok := cur.DepsByProject[path]; !ok {
				removed = append(removed, path)
			}
		}
		slices.Sort(added)
		slices.Sort(removed)
		slices.Sort(changed)

		out := make([]string, 0, len(changed)+len(added)+len(removed))
		for _, p := range changed {
			out = append(out, "dep changed: "+p)
		}
		for _, p := range added {
			out = append(out, "dep added: "+p)
		}
		for _, p := range removed {
			out = append(out, "dep removed: "+p)
		}
		return out
	}
	// No per-project attribution captured (an old manifest, or no DependsOn):
	// fall back to a coarse count over the sorted hash lists.
	if !slices.Equal(prev.Deps, cur.Deps) {
		return []string{fmt.Sprintf("deps changed: %d -> %d entries (no per-project attribution captured)", len(prev.Deps), len(cur.Deps))}
	}
	return nil
}

// toolVersionKey splits a "spell:version" or "spell:tool:version" entry
// (see Step.ToolVersions) into its identifying key and version, so two runs
// can be compared per spell/tool rather than as opaque strings.
func toolVersionKey(s string) (key, version string) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+1:]
}

func diffToolVersions(prev, cur []string) []string {
	prevBy := make(map[string]string, len(prev))
	for _, s := range prev {
		k, v := toolVersionKey(s)
		prevBy[k] = v
	}
	curBy := make(map[string]string, len(cur))
	for _, s := range cur {
		k, v := toolVersionKey(s)
		curBy[k] = v
	}
	var added, removed, changed []string
	for k, cv := range curBy {
		pv, ok := prevBy[k]
		if !ok {
			added = append(added, fmt.Sprintf("%s (%s)", k, cv))
			continue
		}
		if pv != cv {
			changed = append(changed, fmt.Sprintf("%s: %s -> %s", k, pv, cv))
		}
	}
	for k, pv := range prevBy {
		if _, ok := curBy[k]; !ok {
			removed = append(removed, fmt.Sprintf("%s (%s)", k, pv))
		}
	}
	slices.Sort(added)
	slices.Sort(removed)
	slices.Sort(changed)

	out := make([]string, 0, len(changed)+len(added)+len(removed))
	for _, s := range changed {
		out = append(out, "tool version changed: "+s)
	}
	for _, s := range added {
		out = append(out, "tool added: "+s)
	}
	for _, s := range removed {
		out = append(out, "tool removed: "+s)
	}
	return out
}
