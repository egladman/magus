package std

import (
	"context"
	"fmt"

	semver "github.com/Masterminds/semver/v3"
	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-utils bindings -module semver -lang buzz -out ../internal/interp/bindings/gen/semver.go

func init() { Register(Semver) }

// Semver is the "semver" host module: semantic version parsing and comparison.
var Semver = Module{
	Name: "semver",
	Doc:  "Semantic version parsing and comparison (SemVer 2.0.0).",
	Methods: []Method{
		{
			Name: "compare",
			Doc:  "Order two semver strings: -1 when a sorts before b, 0 when they are equal, 1 when a sorts after. Use satisfies() to test a relation or a range.",
			Args: []Arg{
				{Name: "a", Type: TypeString},
				{Name: "b", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeInt}},
			Raises:  true,
			Impl:    SemverCompare,
		},
		{
			Name:    "isValid",
			Doc:     "Whether v parses as a semantic version. Use it instead of calling parse purely to see whether it raises.",
			Args:    []Arg{{Name: "v", Type: TypeString}},
			Returns: []Ret{{Type: TypeBool}},
			Impl:    SemverIsValid,
		},
		{
			Name:    "canonical",
			Doc:     `Canonical "vX.Y.Z" form of v, filling in missing components and discarding build metadata; errors on invalid input.`,
			Args:    []Arg{{Name: "v", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    SemverCanonical,
		},
		{
			Name:    "major",
			Doc:     `The major prefix of v as a string: major("1.2.3") is "v1". This is the cache token a spell's VersionKey{upTo = "major"} produces; parse().major is the same number as an int.`,
			Args:    []Arg{{Name: "v", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    SemverMajor,
		},
		{
			Name:    "majorMinor",
			Doc:     `The major.minor prefix of v as a string: majorMinor("1.2.3") is "v1.2". This is the cache token a spell's VersionKey{upTo = "minor"} produces.`,
			Args:    []Arg{{Name: "v", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    SemverMajorMinor,
		},
		{
			Name: "satisfies",
			Doc:  `Whether v meets constraint, the full range syntax magus.yaml required_version uses: ">= 1.2, < 2.0", "^1.2.3", "~1.2". compare() tests one relation; this tests a range.`,
			Args: []Arg{
				{Name: "v", Type: TypeString},
				{Name: "constraint", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeBool}},
			Raises:  true,
			Impl:    SemverSatisfies,
		},
		{
			Name:    "parse",
			Doc:     "Parse a semver string into {major, minor, patch, prerelease, metadata, original}; errors on invalid input.",
			Args:    []Arg{{Name: "v", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "SemverVersion"}},
			Raises:  true,
			Impl:    SemverParse,
		},
		{
			Name:    "next",
			Doc:     `Candidate next versions after v: {major, minor, patch}, each "vX.Y.Z" - the result of bumping the major, minor, or patch component. Errors on invalid input.`,
			Args:    []Arg{{Name: "v", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "SemverNext"}},
			Raises:  true,
			Impl:    SemverNext,
		},
	},
}

// SemverCompare orders a against b, returning -1, 0, or 1.
//
// This is what `compare` means everywhere else - Go's cmp.Compare and strings.Compare,
// x/mod/semver.Compare, Masterminds' Compare, node-semver's compare - and returning a
// bool from a function by that name is a trap: an author arriving from any of them
// guesses three-way ordering, and the guess compiles.
//
// It previously took (a, op, b) and answered whether the relation held. satisfies()
// covers that now, and covers ranges the operator form could not express, so nothing
// is lost by giving the name back its usual meaning.
func SemverCompare(_ context.Context, a, b string) (int, error) {
	va, err := semver.NewVersion(a)
	if err != nil {
		return 0, fmt.Errorf("semver.compare: invalid version %q: %w", a, err)
	}
	vb, err := semver.NewVersion(b)
	if err != nil {
		return 0, fmt.Errorf("semver.compare: invalid version %q: %w", b, err)
	}
	return va.Compare(vb), nil
}

// SemverParse parses v into its constituent parts.
func SemverParse(_ context.Context, v string) (types.SemverVersion, error) {
	sv, err := semver.NewVersion(v)
	if err != nil {
		return types.SemverVersion{}, fmt.Errorf("semver.parse: %w", err)
	}
	return types.SemverVersion{
		Major:      int(sv.Major()),
		Minor:      int(sv.Minor()),
		Patch:      int(sv.Patch()),
		Prerelease: sv.Prerelease(),
		Metadata:   sv.Metadata(),
		Original:   sv.Original(),
	}, nil
}

// SemverNext returns the three candidate next versions after v: bumping
// major, minor, or patch. Delegates to Masterminds/semver's Inc* methods
// rather than hand-rolling "+1" arithmetic: per version.go's IncPatch and
// semver.org spec item 9, a version WITH a prerelease has IncPatch strip the
// prerelease and KEEP the patch (v1.2.3-rc1 -> v1.2.3, not v1.2.4) - a
// subtlety naive arithmetic gets wrong and that this delegation gets free.
func SemverNext(_ context.Context, v string) (types.SemverNext, error) {
	sv, err := semver.NewVersion(v)
	if err != nil {
		return types.SemverNext{}, fmt.Errorf("semver.next: %w", err)
	}
	major := sv.IncMajor()
	minor := sv.IncMinor()
	patch := sv.IncPatch()
	return types.SemverNext{
		Major: "v" + major.String(),
		Minor: "v" + minor.String(),
		Patch: "v" + patch.String(),
	}, nil
}

// SemverIsValid reports whether v parses as a semantic version.
//
// It exists because the module previously offered no way to ask: magusfile.buzz called
// `semver\parse(v)` purely for its side effect of raising, which is an exception used
// as a predicate. Both x/mod (IsValid) and node-semver (valid) carry one.
func SemverIsValid(_ context.Context, v string) (bool, error) {
	_, err := semver.NewVersion(v)
	return err == nil, nil
}

// SemverCanonical returns v in canonical "vX.Y.Z" form.
//
// The leading "v" is what every function in this file that returns a version STRING
// emits, so a caller can compare two of them directly. Input stays lenient - "1.2",
// "v1.2.0", and "1.2.0+build" all canonicalize - which keeps it consistent with parse,
// the module's other entry point.
func SemverCanonical(_ context.Context, v string) (string, error) {
	sv, err := semver.NewVersion(v)
	if err != nil {
		return "", fmt.Errorf("semver.canonical: %w", err)
	}
	return fmt.Sprintf("v%d.%d.%d", sv.Major(), sv.Minor(), sv.Patch()), nil
}

// SemverMajor returns the major prefix of v, e.g. "v1" for "1.2.3".
//
// It returns a STRING where parse().major returns an int, and the difference is the
// point: this is the projection a cache key holds, so two versions sharing a major
// produce one identical token. Named for golang.org/x/mod/semver.Major, which is the
// same function and the same spelling the engine calls when narrowing a probe.
func SemverMajor(_ context.Context, v string) (string, error) {
	sv, err := semver.NewVersion(v)
	if err != nil {
		return "", fmt.Errorf("semver.major: %w", err)
	}
	return fmt.Sprintf("v%d", sv.Major()), nil
}

// SemverMajorMinor returns the major.minor prefix of v, e.g. "v1.2" for "1.2.3".
// See SemverMajor for why it is a string.
func SemverMajorMinor(_ context.Context, v string) (string, error) {
	sv, err := semver.NewVersion(v)
	if err != nil {
		return "", fmt.Errorf("semver.majorMinor: %w", err)
	}
	return fmt.Sprintf("v%d.%d", sv.Major(), sv.Minor()), nil
}

// SemverSatisfies reports whether v meets a full constraint RANGE.
//
// compare() takes a single operator and one version, so it cannot express ">= 1.2,
// < 2.0" - the form magus.yaml's required_version already accepts and the one a
// magusfile reaches for when gating on a toolchain window.
func SemverSatisfies(_ context.Context, v, constraint string) (bool, error) {
	c, err := semver.NewConstraint(constraint)
	if err != nil {
		return false, fmt.Errorf("semver.satisfies: invalid constraint %q: %w", constraint, err)
	}
	sv, err := semver.NewVersion(v)
	if err != nil {
		return false, fmt.Errorf("semver.satisfies: invalid version %q: %w", v, err)
	}
	return c.Check(sv), nil
}
