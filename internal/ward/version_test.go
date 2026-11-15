package ward

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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

// RelinkSuite pins the one recovery line the guard and the hook glue serve.
type RelinkSuite struct {
	suite.Suite
}

func TestRelinkSuite(t *testing.T) {
	suite.Run(t, new(RelinkSuite))
}

func (s *RelinkSuite) TestAimsTheLinkAtTheRoot() {
	s.Equal("GOEXPERIMENT=jsonv2 go -C /src/magus build -trimpath -o magus ./cmd/magus", RelinkAt("/src/magus"))
}

func (s *RelinkSuite) TestNoRootIsTheBareLine() {
	s.Equal("GOEXPERIMENT=jsonv2 go build -trimpath -o magus ./cmd/magus", RelinkAt(""))
}

func (s *RelinkSuite) TestQuotesARootAShellWouldSplit() {
	s.Equal(`GOEXPERIMENT=jsonv2 go -C '/a b/it'\''s' build -trimpath -o magus ./cmd/magus`, RelinkAt("/a b/it's"))
}

func TestPinnedRelease(t *testing.T) {
	t.Parallel()
	for constraint, want := range map[string]string{
		"= 0.4.3":            "v0.4.3",
		"0.4.3":              "v0.4.3",
		"== v0.5.0-rc.1":     "v0.5.0-rc.1",
		">= 0.4.3, < 0.5.0":  "v0.4.3",
		">= 0.4.3, <= 0.4.9": "v0.4.3",
		"~0.4.3":             "v0.4.3",
		"^0.4.3":             "v0.4.3",
		">= 0.4.4":           "",
		"< 0.5.0":            "",
		"> 0.4.3, < 0.5.0":   "",
		">= 0.4.3 || 0.6.0":  "",
		">= 0.5.0, < 0.4.0":  "",
		"soon":               "",
		"":                   "",
	} {
		assert.Equal(t, want, PinnedRelease(constraint), constraint)
	}
}

// TestStaleBuildAdvice pins every rendering: the gap sentence names whichever fact is
// unknown, and the fixes grow with what magus knows about the build and the tree.
func TestStaleBuildAdvice(t *testing.T) {
	t.Parallel()
	const selfUpdate = "Fix it:\n  - released binary: magus self update"
	const script = "\n  - this workspace's script for it: ./magus buzz hack/dev/bootstrap-checkout.buzz"
	const bootstrap = "\n  - built from source, in the checkout it came from: GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache ."
	served := []string{"./magus buzz hack/dev/bootstrap-checkout.buzz"}
	for name, tc := range map[string]struct {
		build StaleBuild
		want  string
	}{
		"a dev build with a served script, below the floor": {
			build: StaleBuild{Running: "v0.4.3-122-g1a2b3c4", Floor: ">= 0.4.4", Scripts: served},
			want:  "This magus (v0.4.3-122-g1a2b3c4) is older than the workspace needs (>= 0.4.4).\n" + selfUpdate + script + bootstrap,
		},
		"a release with a served script gets no bootstrap": {
			build: StaleBuild{Running: "v0.4.3", Floor: ">= 0.4.4", Scripts: served},
			want:  "This magus (v0.4.3) is older than the workspace needs (>= 0.4.4).\n" + selfUpdate + script,
		},
		"a release with no served script": {
			build: StaleBuild{Running: "v0.4.3", Floor: ">= 0.4.4"},
			want:  "This magus (v0.4.3) is older than the workspace needs (>= 0.4.4).\n" + selfUpdate,
		},
		"a dev build with no served script": {
			build: StaleBuild{Running: "v0.4.3-1-gabc", Floor: ">= 0.4.4"},
			want:  "This magus (v0.4.3-1-gabc) is older than the workspace needs (>= 0.4.4).\n" + selfUpdate + bootstrap,
		},
		"a dev build that meets the floor": {
			build: StaleBuild{Running: "v0.4.4-3-gabc", Floor: ">= 0.4.4", Scripts: served},
			want: "This magus (v0.4.4-3-gabc) meets the workspace floor (>= 0.4.4) but was likely built before the workspace changed.\n" +
				selfUpdate + script + bootstrap,
		},
		"an unknown running version is not a dev build": {
			build: StaleBuild{Floor: ">= 0.4.4", Scripts: served},
			want:  "This magus's version is unknown here; the workspace needs >= 0.4.4.\n" + selfUpdate + script,
		},
		"nothing known": {
			want: "This magus's version is unknown here, and the workspace declares no required_version floor.\n" + selfUpdate,
		},
		"no floor": {
			build: StaleBuild{Running: "v0.4.3"},
			want:  "This magus is v0.4.3; the workspace declares no required_version floor.\n" + selfUpdate,
		},
		"an unstamped build": {
			build: StaleBuild{Running: DevVersion, Floor: ">= 0.4.4", Scripts: served},
			want:  "This magus is an unstamped build, so its version is unknown; the workspace needs >= 0.4.4.\n" + selfUpdate + script + bootstrap,
		},
		"a floor that does not parse": {
			build: StaleBuild{Running: "v0.4.3", Floor: "soon"},
			want:  "This magus is v0.4.3; the workspace needs soon, and the two cannot be compared.\n" + selfUpdate,
		},
		"an exact pin names its release": {
			build: StaleBuild{Running: "v0.4.2", Floor: "= 0.4.3"},
			want:  "This magus (v0.4.2) is older than the workspace needs (= 0.4.3).\n" + selfUpdate + " --version v0.4.3",
		},
		"an upper-bounded range names its lower bound": {
			build: StaleBuild{Running: "v0.4.2", Floor: ">= 0.4.3, < 0.5.0"},
			want:  "This magus (v0.4.2) is older than the workspace needs (>= 0.4.3, < 0.5.0).\n" + selfUpdate + " --version v0.4.3",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.build.Advice())
		})
	}
}
