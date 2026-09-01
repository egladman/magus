package types

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ContextParamAnnotation is the exact parameter type annotation that marks a target: an
// exported magusfile function is a target if and only if its FIRST parameter carries
// this annotation. Buzz namespaces a qualified type with a backslash
// (`serialize\Boxed`), not a dot, so the context type is spelled `magus\Context`;
// the dotted `magus.Context` is not valid Buzz type syntax (the parser stops at the
// dot). Recognition keys on this raw annotation string, independent of
// whether the checker can resolve the type (it treats an unknown qualified name
// permissively). A ctx-less exported function is rejected at load (MGS1008).
const ContextParamAnnotation = `magus\Context`

// kebabCaseSplitWord / kebabCaseSplitNumberLetter mirror the word-boundary
// regexes samber/lo's KebabCase uses, so kebabCase produces identical output
// for identifier-like inputs (FooBar->foo-bar, HTTPServer->http-server,
// build2->build-2) without the lo dependency.
var (
	kebabCaseSplitWord         = regexp.MustCompile(`([a-z])([A-Z0-9])|([a-zA-Z])([0-9])|([0-9])([a-zA-Z])|([A-Z])([A-Z])([a-z])`)
	kebabCaseSplitNumberLetter = regexp.MustCompile(`([0-9])([a-zA-Z])`)
)

// kebabCase lowercases s and inserts '-' at camelCase and letter/digit
// boundaries, collapsing every non-alphanumeric run to a single '-' and
// trimming leading/trailing '-'.
func kebabCase(s string) string {
	s = kebabCaseSplitWord.ReplaceAllString(s, `$1$3$5$7 $2$4$6$8$9`)
	s = kebabCaseSplitNumberLetter.ReplaceAllString(s, "$1 $2")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.ToLower(strings.Join(strings.Fields(b.String()), "-"))
}

// Normalize canonicalizes any magus entity name - a target, a charm, a spell, a
// spell op - to kebab-case, so go_build, goBuild and go-build all name the same
// thing. Applied at BOTH registration and lookup; a name normalized on only one
// side is a silent miss, not an error.
//
// One function rather than the TargetNameNormalizer interface it replaces. That
// interface had a single implementation, and its injection seam
// (magus.WithTargetNameNormalizer) had zero callers anywhere in the tree -
// including tests - so `run.Normalizer` was always nil and the seam only ever
// installed this same kebab-casing. Meanwhile sixteen call sites skipped the
// interface and reached for the package-level default directly, which is what an
// injected normalizer would have had to fight. The indirection bought nothing and
// hid that spell and op names were not going through it at all.
func Normalize(name string) string { return kebabCase(name) }

// TargetCI is the one reserved built-in target: the affected-set anchor that
// `magus affected ci` and `magus affected --plan` key off. It lives in the
// magusfile (composed via magus.needs), never in a spell. Compare against it
// only after normalizing the candidate name (see Normalize).
const TargetCI = "ci"

// targetNameRe constrains target names to alphanumerics plus '-' and '_'.
// Everything else (notably ':' and '@') is reserved for target-reference
// grammar such as "spell::target" and possible future modal forms like
// "go::lint:<mode>".
var targetNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// ValidateTargetName reports whether name is a well-formed target name.
// Allowed characters are letters, digits, '-' and '_'; a non-nil error
// describes the violation.
func ValidateTargetName(name string) error {
	if !targetNameRe.MatchString(name) {
		return fmt.Errorf("magus: target name %q: must contain only letters, digits, '-' or '_'", name)
	}
	return nil
}

// ValidateCharmName reports whether name is a well-formed charm name.
// Charms share the target-name charset (letters, digits, '-' and '_').
func ValidateCharmName(name string) error {
	if !targetNameRe.MatchString(name) {
		return fmt.Errorf("magus: charm %q: must contain only letters, digits, '-' or '_'", name)
	}
	return nil
}

// Target identifies one unit of work (project x target name).
// An empty Path means all projects.
//
// Target plays a dual role, and the meaningful subset of fields differs by use:
//   - As a work-unit it carries identity: Path/Name/Charms/Files describe which
//     project x target to run and against which changed files.
//   - As a policy bag it carries per-target execution policy
//     (SkipCache/Exclusive/Drift/RetryOnVolatile). When used purely as policy
//     (Project.TargetPolicies values, EvaluatedTargetEntry.Policy) only the policy
//     fields are meaningful; the identity fields are unset/ignored.
type Target struct {
	// The identity fields carry omitempty because Target is also serialized as a
	// per-target POLICY (describe's target_policies map, keyed by target name),
	// where path/name/charms/files are all empty and would be pure noise.
	Path   string   `json:"path,omitempty"   buzz:"projectPath"` // workspace-relative project path; empty = all projects
	Name   string   `json:"name,omitempty"`                      // e.g. "build", "test"
	Charms []string `json:"charms,omitempty"`                    // execution charms parsed from the "target:charm,..." suffix
	Files  []string `json:"files,omitempty"`                     // changed files within project; populated by affected expansion

	// Declared and DeclaredCharms are the raw spellings ParseTarget rewrote, empty
	// when the caller already wrote canonical form. Provenance, not identity: Name
	// and Charms above are what magus resolves against, and these only record how it
	// was typed. Same convention as TargetGraphNode.Declared.
	//
	// They exist so a caller can react to a non-canonical spelling - the CLI hints
	// the canonical form - WITHOUT the parser reaching out to print anything. A parse
	// function that writes to stderr cannot be used by the daemon, the MCP handler,
	// or a test without dragging that output along; returning the fact instead lets
	// each caller decide, which is why this is on the value rather than in a wrapper.
	Declared       string   `json:"declared,omitempty"`
	DeclaredCharms []string `json:"declared_charms,omitempty"`

	// Per-target execution policy. SkipCache, Exclusive, Slots, and Drift are
	// author-facing, serialized into the Buzz object Target. RetryOnVolatile is a CI-only
	// hook set via the Go registration API, excluded from the Buzz object (buzz:"-").
	SkipCache bool `json:"skip_cache,omitempty" buzz:"skip_cache"` // opt out of the cache: always run, never replay/snapshot
	// SkipCacheReason is the prose the magusfile gave for SkipCache. It is required
	// (a bare `true` is a load error) because opting out is a claim that REPLAYING
	// THIS TARGET WOULD BE WRONG - it signs a fresh artifact, records a screen
	// capture, mutates go.mod - and not a preference for a fresh run, which is what
	// --no-cache is for. Demanding prose is what keeps the two apart: six of these
	// were once workarounds for a snapshot error that no longer exists, and a bare
	// bool gave no way to tell them from the real ones.
	SkipCacheReason string `json:"skip_cache_reason,omitempty" buzz:"skip_cache_reason"`
	Exclusive       bool   `json:"exclusive,omitempty"` // run alone: no other target runs concurrently
	Slots           int    `json:"slots,omitempty"`     // concurrency slots to hold while running (0 or 1 = one slot); throttles parallel work around a resource-heavy target. Clamped to the run's total slot budget.
	// MemoryMB is the peak resident memory this target needs, in megabytes; 0 means
	// undeclared. It is a portable way to spell Slots: an author knows a race-enabled
	// test suite wants 8GB, but nobody can say how many slots that is on a machine
	// they have never seen, and the answer differs between a 16GB CI runner and a
	// 64GB workstation. magus converts it against the host's memory-per-slot share
	// and holds that many slots, so there is ONE admission path rather than two
	// budgets that can disagree.
	//
	// Undeclared (0) and an unmeasurable host both mean "take one slot", which is
	// exactly the behavior that existed before this field.
	//
	// A COMPOSED target inherits the largest declaration in its chain; see
	// ChainMemoryMB, which is the figure both halves of admission actually read.
	MemoryMB int `json:"memory_mb,omitempty" buzz:"memory_mb"`
	// Drift is what happens when this target's declared outputs move under a read-only
	// run. Empty is the DEFAULT, which gates any target that declares outputs - see
	// DriftPolicy for why that is on rather than off.
	Drift DriftPolicy `json:"drift,omitempty" buzz:"drift"`
	// DriftReason is the prose the magusfile gave for turning the gate off, and it is
	// required for exactly the reason SkipCacheReason is: switching off a check that
	// protects everyone downstream is a claim, not a preference, and a bare "off" leaves
	// the next reader no way to tell a considered exemption from a workaround somebody
	// never came back to.
	DriftReason     string `json:"drift_reason,omitempty" buzz:"drift_reason"`
	RetryOnVolatile bool   `json:"retryOnVolatile,omitempty" buzz:"-"` // route through volatility detection + auto-retry
	// IncludeOS and IncludeArch override cache.include.*.enabled for this target.
	// nil inherits the workspace answer, which is what an undeclared target gets.
	//
	// Authored NESTED, to mirror magus.yaml rather than invent a second shape for the
	// same decision:
	//
	//	"image": { "cache": { "include": { "arch": { "enabled": false } } } }
	//
	// Stored flat because the nesting exists for the author's benefit - a Go caller
	// reading two optional bools should not walk three structs to find them.
	IncludeOS   *bool `json:"includeOS,omitempty" buzz:"-"`
	IncludeArch *bool `json:"includeArch,omitempty" buzz:"-"`
}

// DriftPolicy says what happens when a target's declared outputs move under a read-only
// run - the generated file that was never regenerated, and the reason `magus run generate`
// is a gate at all.
//
// It is a policy about the RESPONSE, never about the diagnosis. magus always separates
// drift this change caused from drift that arrived with the base, because failing an author
// for bytes they did not move is a bug in the gate rather than a strictness setting; there
// is deliberately no way to spell that behavior. See ClassifyDrift.
//
// The zero value gates, which is the point: a target that declares an output has already
// claimed those bytes are a function of its inputs, and checking a claim the workspace
// volunteered needs no second declaration. A tool whose correctness depends on every author
// remembering to switch it on has pushed its own conformance onto its users.
type DriftPolicy string

const (
	// DriftDefault gates every target that declares outputs, and no others. Written as
	// the empty string so an undeclared policy IS the default rather than resembling one.
	DriftDefault DriftPolicy = ""
	// DriftFail is DriftDefault stated out loud, for a target whose gating a reader
	// should not have to infer from the presence of an output glob.
	DriftFail DriftPolicy = "fail"
	// DriftWarn reports drift and never fails. The migration path: a workspace adopting
	// magus over a tree with existing drift can see the whole list before it has to be
	// green, which is the difference between adopting the gate and disabling it.
	DriftWarn DriftPolicy = "warn"
	// DriftOff does not check. Requires TargetPolicy.DriftReason.
	DriftOff DriftPolicy = "off"
)

// Gates reports whether this policy checks at all. declaresOutputs carries the default's
// condition, so the caller does not restate it at each site.
func (d DriftPolicy) Gates(declaresOutputs bool) bool {
	switch d {
	case DriftOff:
		return false
	case DriftFail, DriftWarn:
		return true
	default:
		return declaresOutputs
	}
}

// Fails reports whether drift this change caused should fail the run rather than be
// reported. Only DriftWarn downgrades it.
func (d DriftPolicy) Fails() bool { return d != DriftWarn }

// ValidDriftPolicy reports whether s is a policy magus knows. A typo must not read as the
// default: "of" or "warm" would silently gate or silently not, and the author would find
// out from a merge rather than from the load.
func ValidDriftPolicy(s DriftPolicy) bool {
	switch s {
	case DriftDefault, DriftFail, DriftWarn, DriftOff:
		return true
	}
	return false
}

// String returns the canonical "path:target" form.
func (t Target) String() string { return t.Path + ":" + t.Name }

// ParseTarget parses a target reference of the form "target[:charm[,charm...]]".
// The project is supplied separately (positional), not embedded in the reference;
// ':' introduces a comma-separated list of execution charms. Both the target and
// each charm are constrained to the target-name charset.
func ParseTarget(s string) (Target, error) {
	if s == "" {
		return Target{}, fmt.Errorf("magus: target string is empty")
	}
	target := s
	var charms, declaredCharms []string
	if i := strings.IndexByte(s, ':'); i >= 0 {
		target = s[:i]
		charmPart := s[i+1:]
		if charmPart == "" {
			return Target{}, fmt.Errorf("magus: target %q: charm must not be empty", s)
		}
		for _, g := range strings.Split(charmPart, ",") {
			if err := ValidateCharmName(g); err != nil {
				return Target{}, fmt.Errorf("magus: target %q: %w", s, err)
			}
			if n := Normalize(g); n != g {
				declaredCharms = append(declaredCharms, g)
			}
			charms = append(charms, Normalize(g))
		}
	}
	if err := ValidateTargetName(target); err != nil {
		return Target{}, fmt.Errorf("magus: target %q: %w", s, err)
	}
	var declared string
	if n := Normalize(target); n != target {
		declared = target
	}
	target = Normalize(target)
	return Target{Name: target, Charms: charms, Declared: declared, DeclaredCharms: declaredCharms}, nil
}

// ExecResult is the serializable {stdout, stderr, code, ok} shape every magus exec
// surface returns (proc.exec, magus.cmd, a captured spell op); ok is code == 0. It is
// the boundary mirror of the richer internal run.ExecResult.
//
// The Buzz `object ExecResult` mirror is generated from this struct by
// cmd/magus-utils types (go:generate); keep them in lockstep through the generator.
type ExecResult struct {
	Stdout string
	Stderr string
	Code   int
	OK     bool `buzz:"ok"`
}

// ShellCommand is the argv that runs a line through the platform shell: what
// proc.shell returns, and what proc.exec takes. It exists so the shell choice is a
// VALUE rather than a decision taken inside a call - you can print it, log it, or
// assert on it before anything runs, which the old proc.shell wrapper made
// impossible.
//
// The Buzz `object ShellCommand` mirror is generated from this struct by
// cmd/magus-utils types (go:generate); keep them in lockstep through the generator.
type ShellCommand struct {
	Bin  string
	Args []string
}

// MagusfileSpellName is the spell a project's own magusfile is bound as. It is matched
// by name because that spell is a single global instance standing in for every
// project's magusfile - see Project.MagusfileTargets for why it cannot answer for one.
const MagusfileSpellName = "magusfile"

// Key returns the lines identifying this target's work for the cache: its name, because
// the body is Buzz rather than a command and has no argv to serialize - the body's text
// already reaches the key through the magusfile's own Sources entry, leaving the entry
// point as what separates build from go-build within one project.
//
// Deliberately no interface here: nothing consumes this yet, and Go declares an
// interface where it is USED. When the hasher takes either a target or an op, it
// declares the one-method interface it needs, named for what it wants from them.
func (t Target) Key() []string {
	if t.Name == "" {
		return nil
	}
	return []string{"target:" + t.Name}
}

// ChainMemoryMB is the largest memory declaration anywhere in what a target will
// actually run: its own, and every target it composes with ctx.needs, transitively.
// It returns the figure and the target that declared it.
//
// Without the fold a declaration is INERT for the command people run: only `ci` is
// scheduled as a step, so the `test` it composes reaches neither the limiter nor
// machine-wide admission. The MAXIMUM, not the sum, because a chain runs in order.
//
// lookup resolves a cross-project step and may return nil, in which case that step
// contributes nothing rather than a guess. It lives here because admission and
// doctor must agree on one figure.
func ChainMemoryMB(p *Project, target string, lookup func(path string) *Project) (mb int, declaredBy string) {
	seen := map[string]bool{}

	var walk func(proj *Project, name string) (int, string)
	walk = func(proj *Project, name string) (int, string) {
		if proj == nil {
			return 0, ""
		}
		key := proj.Path + "\x00" + name
		if seen[key] {
			return 0, "" // a cycle is rejected elsewhere; here it must simply terminate
		}
		seen[key] = true

		peak, from := proj.TargetPolicies[name].MemoryMB, ""
		if peak > 0 {
			from = name
		}
		for _, step := range proj.TargetChains[name] {
			next := proj
			if step.Project != "" {
				if lookup == nil {
					continue
				}
				next = lookup(step.Project)
			}
			if stepMB, stepFrom := walk(next, step.Target); stepMB > peak {
				peak, from = stepMB, stepFrom
			}
		}
		return peak, from
	}
	return walk(p, target)
}

// ChainSkipCacheOutputs is the declared output of every skip_cache target a target
// composes with ctx.needs, transitively, as workspace-rooted globs. target's own
// outputs are excluded.
//
// ctx.needs runs a composed target inside the parent's body, so it gets no cache
// step of its own: a cache HIT on the parent skips it, skip_cache included. The
// engine cannot key such a target's real inputs (not being keyable is why it
// opted out). It can key the artifact the target maintains: with these globs in
// the parent's sources, a stale artifact turns the parent's HIT into a miss and
// the chain re-runs. The cost is one extra miss after the artifact changes.
//
// lookup resolves a cross-project step and may return nil, in which case that step
// contributes nothing.
func ChainSkipCacheOutputs(p *Project, target string, lookup func(path string) *Project) []string {
	seen := map[string]bool{}
	var out []string

	var walk func(proj *Project, name string, collect bool)
	walk = func(proj *Project, name string, collect bool) {
		if proj == nil {
			return
		}
		key := proj.Path + "\x00" + name
		if seen[key] {
			return // load rejects cycles; the walk only has to terminate
		}
		seen[key] = true

		if collect && proj.TargetPolicies[name].SkipCache {
			for _, ref := range proj.TargetOutputs[name] {
				owner := ref.Project
				if owner == "" {
					owner = proj.Path
				}
				if g := RootGlob(owner, ref.Glob); !slices.Contains(out, g) {
					out = append(out, g)
				}
			}
		}
		for _, step := range proj.TargetChains[name] {
			next := proj
			if step.Project != "" {
				if lookup == nil {
					continue
				}
				next = lookup(step.Project)
			}
			walk(next, step.Target, true)
		}
	}
	walk(p, target, false)
	return out
}
