package types

import (
	"fmt"
	"regexp"
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
//     (SkipCache/Exclusive/FailOnDrift/RetryOnVolatile). When used purely as policy
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

	// Per-target execution policy. SkipCache, Exclusive, and Slots are author-facing,
	// serialized into the Buzz object Target. FailOnDrift and RetryOnVolatile are CI-only
	// hooks set via the Go registration API, excluded from the Buzz object (buzz:"-").
	SkipCache       bool `json:"skip_cache,omitempty" buzz:"skip_cache"` // opt out of the cache: always run, never replay/snapshot
	Exclusive       bool `json:"exclusive,omitempty"`                    // run alone: no other target runs concurrently
	Slots           int  `json:"slots,omitempty"`                        // concurrency slots to hold while running (0 or 1 = one slot); throttles parallel work around a resource-heavy target. Clamped to the run's total slot budget.
	FailOnDrift     bool `json:"failOnDrift,omitempty" buzz:"-"`         // fail the run if the working tree is dirty after this target (drift gate)
	RetryOnVolatile bool `json:"retryOnVolatile,omitempty" buzz:"-"`     // route through volatility detection + auto-retry
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
// surface returns (os.exec, magus.cmd, a captured spell op); ok is code == 0. It is
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
