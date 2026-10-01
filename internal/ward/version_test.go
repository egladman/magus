package ward

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/diagnostics"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckRequiredVersion(t *testing.T) {
	t.Run("a satisfied floor passes", func(t *testing.T) {
		assert.Nil(t, CheckRequiredVersion(">= 0.4.0", "v0.4.0"))
		assert.Nil(t, CheckRequiredVersion(">= 0.4.0", "v0.5.1"))
	})

	t.Run("a too-old build is MGS1021 and names both fixes", func(t *testing.T) {
		d := CheckRequiredVersion(">= 0.4.0", "v0.3.0")
		require.NotNil(t, d)
		assert.Equal(t, types.WorkspaceNeedsNewerMagus, d.Code)
		assert.Contains(t, d.Error(), "v0.3.0")
		assert.Contains(t, d.Error(), ">= 0.4.0")
		assert.Contains(t, d.Error(), "self update", "the remedy must name the local fix")
		assert.Contains(t, d.Error(), "CI", "and the CI fix, which is the other half")
	})

	// Three ways to have nothing to compare, all of which must load rather than block.
	t.Run("no floor declared", func(t *testing.T) {
		assert.Nil(t, CheckRequiredVersion("", "v0.1.0"))
	})
	t.Run("no running version supplied", func(t *testing.T) {
		assert.Nil(t, CheckRequiredVersion(">= 99.0.0", ""), "a library caller has no version to be too old")
	})
	t.Run("an unparsable running version", func(t *testing.T) {
		assert.Nil(t, CheckRequiredVersion(">= 0.4.0", "some-fork-build"),
			"a version this ward cannot read is not the workspace's fault; stranding the user is worse")
	})

	// A dev build is typically NEWER than any release, so blocking it against a floor
	// the working tree itself just raised would be exactly backwards.
	t.Run("an unstamped dev build is never too old", func(t *testing.T) {
		assert.Nil(t, CheckRequiredVersion(">= 99.0.0", DevVersion))
	})

	// A prerelease of the version that satisfies the floor satisfies it. Semver
	// constraints otherwise exclude every prerelease, so v0.4.0-rc1 would be told to
	// upgrade to 0.4.0, which it already effectively is.
	t.Run("a prerelease of the required version satisfies it", func(t *testing.T) {
		assert.Nil(t, CheckRequiredVersion(">= 0.4.0", "v0.4.0-rc1"))
	})
	t.Run("a prerelease still below the floor does not", func(t *testing.T) {
		assert.NotNil(t, CheckRequiredVersion(">= 0.4.0", "v0.3.0-rc1"))
	})

	// A floor nobody can parse protects nobody, and a silent pass would let a typo
	// read as "no floor declared".
	t.Run("a malformed constraint is reported, not ignored", func(t *testing.T) {
		d := CheckRequiredVersion("at least 0.4", "v0.1.0")
		require.NotNil(t, d)
		assert.Equal(t, types.WorkspaceNeedsNewerMagus, d.Code)
		assert.Contains(t, d.Error(), "not a valid semver constraint")
	})
}

// A source build must satisfy any floor, including one naming a release that does not
// exist yet. This is what makes a floor armable the moment the feature lands rather than
// only after it ships, and shipping is exactly when the floor stops mattering.
func TestCheckRequiredVersion_SourceBuildIsNotBlocked(t *testing.T) {
	for _, running := range []string{
		"v0.3.0-286-ga899daa3",       // git describe: 286 commits past v0.3.0
		"v0.3.0-286-ga899daa3-dirty", // ... with uncommitted changes
		"unknown",                    // unstamped
		"",                           // no version supplied at all
	} {
		t.Run(running, func(t *testing.T) {
			assert.Nil(t, CheckRequiredVersion(">= 99.0.0", running),
				"a dev build is compiled from the workspace it runs, so it cannot lack a feature that workspace uses")
		})
	}
}

// The binary a floor exists to catch: a real release, built elsewhere and shipped, that
// predates the feature the magusfile needs. Without the floor this is the build that
// fails somewhere unrelated, or, for a silently-ignored argument, hangs.
func TestCheckRequiredVersion_ShippedReleaseBelowTheFloorIsRefused(t *testing.T) {
	err := CheckRequiredVersion(">= 0.4.0", "v0.3.1")
	require.Error(t, err)
	assert.ErrorIs(t, err, types.WorkspaceNeedsNewerMagus)
	assert.Contains(t, err.Error(), "v0.3.1", "the message names the build that is too old")
}

// realBuzzErr compiles src and returns the failure wrapped the way workspace load wraps it.
//
// It goes through the checker rather than hand-building a *diagnostics.Error, and that is the
// whole point of the helper. The hand-built fixture is not the shape gopherbuzz returns (the
// checker returns its own unexported error type), so a test built on it proved that errors.As
// works and nothing else. It was green for the entire time the BZZ1002 half of this explainer
// could not fire.
//
// The test-only import of gopherbuzz does not move this package's production dependency floor,
// which is why version.go still spells the codes as literals.
func realBuzzErr(t *testing.T, src string) error {
	t.Helper()
	err := buzz.NewSession(t.Context()).Exec(t.Context(), src)
	require.Error(t, err, "the fixture must actually fail to compile, or it pins nothing")
	return fmt.Errorf("magusfile: exec magusfile.buzz: %w", err)
}

// buzzErr builds a coded diagnostic directly, for codes no Buzz source can produce here (a
// magus code, an unrelated BZZ one). The positive cases go through realBuzzErr instead.
func buzzErr(code, msg string) error {
	return fmt.Errorf("magusfile: exec magusfile.buzz: %w", &diagnostics.Error{
		Code: diagnostics.Code(code),
		Msg:  msg,
	})
}

// TestExplainStaleBinary_AnnotatesTheDeadlockShapes covers the two codes an
// out-of-date binary actually produces. Both were observed together in one incident:
// a spell referenced a type the binary did not provide (BZZ1002), which made the
// module unresolvable to the magusfile importing it (BZZ2001).
func TestExplainStaleBinary_AnnotatesTheDeadlockShapes(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"undefined type from a spell", `final s = Secret{value = "x"};`, `undefined type "Secret"`},
		{"the import it cascades into", `import "spells/github/actions";`, "module not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := realBuzzErr(t, tc.src)
			got := ExplainStaleBinary(src, "v0.3.9", ">= 0.4.0")
			require.Error(t, got)
			assert.Contains(t, src.Error(), tc.want, "the fixture must be the shape it names")
			assert.Equal(t, types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s", src.Error()+"\n"+
				"This build does not provide that name; unless it is misspelled, this magus is out of date.\n\n"+
				"This magus (v0.3.9) is older than the workspace needs (>= 0.4.0).\n"+
				"Fix it:\n"+
				"  - released binary: magus self update").Error(), got.Error())
			assert.ErrorIs(t, got, src, "the original diagnostic stays in the chain")
		})
	}
}

// TestExplainStaleBinary_LeavesEverythingElseAlone is the half that keeps this honest.
// A hint attached to every load failure would be noise on a plain syntax error, and
// worse, it would teach people to ignore it.
func TestExplainStaleBinary_LeavesEverythingElseAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"a plain error", errors.New("magusfile: exec magusfile.buzz: unexpected '}'")},
		{"a different buzz code", buzzErr("BZZ1006", "call may raise but is neither declared with !> nor caught")},
		{"a magus code", buzzErr("MGS1002", "duplicate spell source")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ExplainStaleBinary(tc.err, "0.4.0", ">= 0.4.0")
			assert.Equal(t, tc.err, got, "must be returned untouched")
		})
	}
}

// A file naming a namespace it never imported lacks an import line, which no newer magus
// supplies, so "self update" would send its author in a circle.
func TestExplainStaleBinary_LeavesAMissingImportAlone(t *testing.T) {
	for _, src := range []string{
		`fun f(ctx: magus\Context) > void {}`,
		`final s = magus\Secret{value = "x"};`,
		`final p = magus\project;`,
	} {
		t.Run(src, func(t *testing.T) {
			err := realBuzzErr(t, src)
			require.ErrorContains(t, err, "no import binds magus in this module")
			got := ExplainStaleBinary(err, "v0.3.9", ">= 0.4.0")
			assert.Equal(t, err, got, "must be returned untouched")
			assert.NotErrorIs(t, got, types.WorkspaceNeedsNewerMagus)
		})
	}
}

// TestExplainStaleBinary_WorksWithNothingDeclared is the case that matters for every
// workspace that is not this one: no required_version, and a build with no stamp. The
// hint still has to say something useful, because a workspace with no floor is exactly
// the one whose users get no warning from CheckRequiredVersion.
func TestExplainStaleBinary_WorksWithNothingDeclared(t *testing.T) {
	got := ExplainStaleBinary(realBuzzErr(t, `final s = Secret{value = "x"};`), DevVersion, "")
	require.Error(t, got)
	assert.Contains(t, got.Error(), "\n\nThis magus is an unstamped build, so its version is unknown, "+
		"and the workspace declares no required_version floor.\n"+
		"Fix it:\n"+
		"  - released binary: magus self update\n"+
		"  - built from source, in the checkout it came from: GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .\n",
		"an unstamped build was built from source, so a rebuild is among the fixes")
}

// TestExplainStaleBinary_AppliesToDevBuilds pins the deliberate difference from
// CheckRequiredVersion, which exempts them. A dev build predating a pull is the most
// common way to reach this, so exempting it here would skip the majority case.
func TestExplainStaleBinary_AppliesToDevBuilds(t *testing.T) {
	got := ExplainStaleBinary(realBuzzErr(t, `final s = Secret{value = "x"};`), DevVersion, ">= 0.4.0")
	require.ErrorIs(t, got, types.WorkspaceNeedsNewerMagus)
	assert.Contains(t, got.Error(), "this magus is out of date")

	// And the contrast, in one place so the asymmetry is visible: the floor ward does
	// exempt the same build.
	assert.Nil(t, CheckRequiredVersion(">= 99.0.0", DevVersion),
		"CheckRequiredVersion exempts dev builds; ExplainStaleBinary deliberately does not")
}

// TestExplainStaleBinary_NotesOnlyTheBranchItExplains pins the joined load error: one
// stale-shaped failure among several must not relabel the others as a stale binary, and
// the version gap is stated once however many branches are stale.
func TestExplainStaleBinary_NotesOnlyTheBranchItExplains(t *testing.T) {
	first := realBuzzErr(t, `final s = Secret{value = "x"};`)
	other := buzzErr("MGS1038", "option removed")
	second := realBuzzErr(t, `import "spells/github/actions";`)
	got := ExplainStaleBinary(errors.Join(first, other, second), "v0.3.9", ">= 0.4.0")

	multi, ok := got.(interface{ Unwrap() []error })
	require.True(t, ok, "the join must survive")
	branches := multi.Unwrap()
	require.Len(t, branches, 3)
	note := "This build does not provide that name; unless it is misspelled, this magus is out of date."
	assert.Equal(t, types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s\n%s", first, note).Error(),
		branches[0].Error(), "an earlier stale branch carries the note, not the advice")
	assert.Equal(t, other, branches[1], "an unrelated branch is returned untouched")
	assert.Equal(t, types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s\n%s\n\n%s", second, note,
		StaleBuild{Running: "v0.3.9", Floor: ">= 0.4.0"}.Advice()).Error(), branches[2].Error())
	assert.Equal(t, 1, strings.Count(got.Error(), "Fix it:"), "the advice is stated once")
}

// TestStaleBuildAdvice pins every rendering: the gap sentence names whichever fact is
// unknown, and the fixes grow with what magus knows about the build and the tree.
func TestStaleBuildAdvice(t *testing.T) {
	t.Parallel()
	const selfUpdate = "Fix it:\n  - released binary: magus self update"
	const goBuild = "\n  - built from this checkout: ./magus run go-build ."
	const bootstrap = "\n  - if that cannot load the tree either: mv magus magus.old, then GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache ."
	for name, tc := range map[string]struct {
		build StaleBuild
		want  string
	}{
		"a dev build in a source tree, below the floor": {
			build: StaleBuild{Running: "v0.4.3-122-g1a2b3c4", Floor: ">= 0.4.4", SourceTree: true},
			want:  "This magus (v0.4.3-122-g1a2b3c4) is older than the workspace needs (>= 0.4.4).\n" + selfUpdate + goBuild + bootstrap,
		},
		"a release in a source tree gets no bootstrap": {
			build: StaleBuild{Running: "v0.4.3", Floor: ">= 0.4.4", SourceTree: true},
			want:  "This magus (v0.4.3) is older than the workspace needs (>= 0.4.4).\n" + selfUpdate + goBuild,
		},
		"a release outside a source tree": {
			build: StaleBuild{Running: "v0.4.3", Floor: ">= 0.4.4"},
			want:  "This magus (v0.4.3) is older than the workspace needs (>= 0.4.4).\n" + selfUpdate,
		},
		"a dev build outside a source tree": {
			build: StaleBuild{Running: "v0.4.3-1-gabc", Floor: ">= 0.4.4"},
			want: "This magus (v0.4.3-1-gabc) is older than the workspace needs (>= 0.4.4).\n" + selfUpdate +
				"\n  - built from source, in the checkout it came from: GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .",
		},
		"a dev build that meets the floor": {
			build: StaleBuild{Running: "v0.4.4-3-gabc", Floor: ">= 0.4.4", SourceTree: true},
			want: "This magus (v0.4.4-3-gabc) meets the workspace floor (>= 0.4.4) but was likely built before the workspace changed.\n" +
				selfUpdate + goBuild + bootstrap,
		},
		"an unknown running version is not a dev build": {
			build: StaleBuild{Floor: ">= 0.4.4", SourceTree: true},
			want:  "This magus's version is unknown here; the workspace needs >= 0.4.4.\n" + selfUpdate + goBuild,
		},
		"nothing known": {
			want: "This magus's version is unknown here, and the workspace declares no required_version floor.\n" + selfUpdate,
		},
		"no floor": {
			build: StaleBuild{Running: "v0.4.3"},
			want:  "This magus is v0.4.3; the workspace declares no required_version floor.\n" + selfUpdate,
		},
		"an unstamped build": {
			build: StaleBuild{Running: DevVersion, Floor: ">= 0.4.4", SourceTree: true},
			want:  "This magus is an unstamped build, so its version is unknown; the workspace needs >= 0.4.4.\n" + selfUpdate + goBuild + bootstrap,
		},
		"a floor that does not parse": {
			build: StaleBuild{Running: "v0.4.3", Floor: "soon"},
			want:  "This magus is v0.4.3; the workspace needs soon, and the two cannot be compared.\n" + selfUpdate,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.build.Advice())
		})
	}
}

// TestExplainStaleBinary_KeepsAMultiWrapWhole pins that only a join splits: a
// "%w: %w" wrapper keeps its own text and is noted as one error.
func TestExplainStaleBinary_KeepsAMultiWrapWhole(t *testing.T) {
	stale := realBuzzErr(t, `final s = Secret{value = "x"};`)
	wrapped := fmt.Errorf("project a: %w: %w", errors.New("load"), stale)
	got := ExplainStaleBinary(wrapped, "0.4.0", ">= 0.4.0")

	assert.ErrorIs(t, got, wrapped)
	assert.Contains(t, got.Error(), wrapped.Error(), "the wrapper's own text survives")
	assert.Equal(t, 1, strings.Count(got.Error(), "Fix it:"))
}
