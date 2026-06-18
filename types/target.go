package types

import (
	"fmt"
	"regexp"
	"strings"
)

// TargetNameNormalizer converts raw identifier strings (e.g. from exported
// function names) to their canonical registered target name. Applied at both
// registration and lookup so the two can never drift.
type TargetNameNormalizer interface {
	NormalizeTargetName(string) string
}

type kebabTargetNormalizer struct{}

func (kebabTargetNormalizer) NormalizeTargetName(s string) string { return kebabCase(s) }

// kebabCaseSplitWord / kebabCaseSplitNumberLetter mirror the word-boundary
// regexes that samber/lo's KebabCase uses, so kebabCase produces identical
// output for identifier-like inputs (FooBar->foo-bar, HTTPServer->http-server,
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

// DefaultTargetNameNormalizer normalizes identifiers to kebab-case so that
// go_build, goBuild, and go-build all resolve to the same target "go-build".
var DefaultTargetNameNormalizer TargetNameNormalizer = kebabTargetNormalizer{}

// TargetCI is the one reserved built-in target: the affected-set anchor that
// `magus affected ci` and `magus affected --plan` key off. It lives in the
// magusfile (composed via magus.needs), never in a spell. Compare against
// it only after normalizing the candidate name (see DefaultTargetNameNormalizer).
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

// NormalizeCharmName canonicalizes a charm name the same way target names are
// normalized (see DefaultTargetNameNormalizer), so a charm declared by a spell
// and one typed in a "target:charm" suffix can never drift on casing or
// separators: write, Write, and WRITE all resolve to the same charm, as do
// no_cache and no-cache. Applied symmetrically at both ends — when a charm
// enters from the CLI suffix (ParseTarget) and when it is matched (HasCharm).
func NormalizeCharmName(name string) string {
	return DefaultTargetNameNormalizer.NormalizeTargetName(name)
}

// Target identifies one unit of work (project × target name).
// An empty Path means all projects.
type Target struct {
	Path   string   `buzz:"projectPath"` // workspace-relative project path; empty = all projects
	Name   string   // e.g. "build", "test"
	Charms []string // execution charms parsed from the "target:charm,..." suffix
	Files  []string // changed files within project; populated by affected expansion

	// Per-target execution policy (formerly the TargetPolicy struct, inlined here).
	// SkipCache and Exclusive are author-facing — serialized into the Buzz object
	// Target. FailOnDrift and RetryOnFlake are CI-only hooks set via the Go
	// registration API, excluded from the Buzz object (buzz:"-").
	SkipCache    bool `json:"skipCache,omitempty"`             // opt out of the cache: always run, never replay/snapshot
	Exclusive    bool `json:"exclusive,omitempty"`             // run alone — no other target runs concurrently while this one does
	FailOnDrift  bool `json:"failOnDrift,omitempty" buzz:"-"`  // fail the run if the working tree is dirty after this target (drift gate)
	RetryOnFlake bool `json:"retryOnFlake,omitempty" buzz:"-"` // route through flake detection + auto-retry
}

// String returns the canonical "path:target" form.
func (t Target) String() string { return t.Path + ":" + t.Name }

// ParseTarget parses a target reference of the form "target[:charm[,charm...]]".
// The project is supplied separately (positional), not embedded in the reference;
// ':' introduces a comma-separated list of execution charms. Both the target and each
// charm are constrained to the target-name charset.
func ParseTarget(s string) (Target, error) {
	if s == "" {
		return Target{}, fmt.Errorf("magus: target string is empty")
	}
	target := s
	var charms []string
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
			charms = append(charms, NormalizeCharmName(g))
		}
	}
	if err := ValidateTargetName(target); err != nil {
		return Target{}, fmt.Errorf("magus: target %q: %w", s, err)
	}
	return Target{Name: target, Charms: charms}, nil
}

// Target-query modes. A literal query that also names a Project is a cross-project
// (external) edge; see TargetQuery.IsExternal.
const (
	QueryLiteral = "literal"
	QueryGlob    = "glob"
	QueryRegex   = "regex"
)

// TargetQuery is an unresolved dependency edge: a query that, resolved against a
// project's registered targets, produces zero or more Targets. It is what
// magus.target.literal/glob/regex return and what magus.needs consumes — the recipe
// (a match Mode plus a Pattern), as distinct from Target, which is one resolved
// work-unit. A literal query is the degenerate 1→1 case; glob/regex are 1→N.
//
// The canonical Buzz `object TargetQuery` mirror is generated from this struct by
// cmd/magus-types-gen (go:generate) and shipped in the magus/target module, so the
// Go and Buzz shapes can never drift. Keep them in lockstep through the generator,
// never by hand.
type TargetQuery struct {
	Mode    string // QueryLiteral | QueryGlob | QueryRegex
	Pattern string // exact name for literal; the glob/regex pattern otherwise
	Project string // cross-project (external) reference; empty = same project
}

// IsExternal reports whether q is a cross-project edge: a literal query carrying
// the path of another project. Glob/regex queries are same-project only.
func (q TargetQuery) IsExternal() bool { return q.Mode == QueryLiteral && q.Project != "" }

// ExecResult is the serializable {stdout, stderr, code, ok} shape every magus exec
// surface returns (os.exec, magus.cmd, a captured spell op); ok is code == 0. It is
// the boundary mirror of the richer internal run.ExecResult.
//
// The Buzz `object ExecResult` mirror is generated from this struct by
// cmd/magus-types-gen (go:generate); keep them in lockstep through the generator.
type ExecResult struct {
	Stdout string
	Stderr string
	Code   int
	OK     bool `buzz:"ok"`
}
