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
			Doc:  `Compare two semver strings; op is "==", "!=", "<", "<=", ">", or ">=" - true when the relation holds.`,
			Args: []Arg{
				{Name: "a", Type: TypeString},
				{Name: "op", Type: TypeString},
				{Name: "b", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeBool}},
			Impl:    SemverCompare,
		},
		{
			Name:    "parse",
			Doc:     "Parse a semver string into {major, minor, patch, prerelease, metadata, original}; errors on invalid input.",
			Args:    []Arg{{Name: "v", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "SemverVersion"}},
			Impl:    SemverParse,
		},
		{
			Name:    "next",
			Doc:     `Candidate next versions after v: {major, minor, patch}, each "vX.Y.Z" - the result of bumping the major, minor, or patch component. Errors on invalid input.`,
			Args:    []Arg{{Name: "v", Type: TypeString}},
			Returns: []Ret{{Type: TypeAnyMap, Object: "SemverNext"}},
			Impl:    SemverNext,
		},
	},
}

// SemverCompare returns true when the relation expressed by op holds between a and b.
// op must be one of "==", "!=", "<", "<=", ">", ">=".
func SemverCompare(_ context.Context, a, op, b string) (bool, error) {
	c, err := semver.NewConstraint(op + " " + b)
	if err != nil {
		return false, fmt.Errorf("semver.compare: invalid constraint %q %q: %w", op, b, err)
	}
	va, err := semver.NewVersion(a)
	if err != nil {
		return false, fmt.Errorf("semver.compare: invalid version %q: %w", a, err)
	}
	return c.Check(va), nil
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
