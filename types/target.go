package types

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/samber/lo"
)

// TargetNameNormalizer converts raw identifier strings (e.g. from exported
// function names) to their canonical registered target name. Applied at both
// registration and lookup so the two can never drift.
type TargetNameNormalizer interface {
	NormalizeTargetName(string) string
}

type kebabTargetNormalizer struct{}

func (kebabTargetNormalizer) NormalizeTargetName(s string) string { return lo.KebabCase(s) }

// DefaultTargetNameNormalizer normalizes identifiers to kebab-case so that
// go_build, goBuild, and go-build all resolve to the same target "go-build".
var DefaultTargetNameNormalizer TargetNameNormalizer = kebabTargetNormalizer{}

// TargetCI is the one reserved built-in target: the affected-set anchor that
// `magus affected ci` and `magus affected --plan` key off. It lives in the
// magusfile (composed via magus.depends_on), never in a spell. Compare against
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
	Path   string   // workspace-relative project path; empty = all projects
	Name   string   // e.g. "build", "test"
	Charms []string // execution charms parsed from the "target:charm,..." suffix
	Files  []string // changed files within project; populated by affected expansion
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
