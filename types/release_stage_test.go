package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func tagOf(name, prefix, version string) VCSTag {
	tag := VCSTag{Name: name, Prefix: prefix}
	if v, _ := ParseVersion(version); version != "" {
		tag.Version = v
	}
	return tag
}

func TestNewestReleaseStage(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		tags []VCSTag
		want ReleaseStage
	}{
		"no tags is unknown": {},
		"newest by semver, not by list order": {
			tags: []VCSTag{tagOf("v0.9.0", "", "v0.9.0"), tagOf("v0.10.0", "", "v0.10.0"), tagOf("v0.2.0", "", "v0.2.0")},
			want: ReleaseStage{Tag: "v0.10.0", Major: 0},
		},
		"a major release": {
			tags: []VCSTag{tagOf("v1.2.3", "", "v1.2.3"), tagOf("v0.9.0", "", "v0.9.0")},
			want: ReleaseStage{Tag: "v1.2.3", Major: 1},
		},
		"a nested module tag does not set the stage": {
			tags: []VCSTag{tagOf("libs/gopherbuzz/v1.0.0", "libs/gopherbuzz/", "v1.0.0"), tagOf("v0.3.0", "", "v0.3.0")},
			want: ReleaseStage{Tag: "v0.3.0", Major: 0},
		},
		"a prerelease does not set the stage": {
			tags: []VCSTag{tagOf("v1.0.0-rc.1", "", "v1.0.0-rc.1"), tagOf("v0.9.0", "", "v0.9.0")},
			want: ReleaseStage{Tag: "v0.9.0", Major: 0},
		},
		"a tag that is not semver is skipped": {
			tags: []VCSTag{tagOf("checkpoint", "", "")},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, NewestReleaseStage(tc.tags))
		})
	}
}

func TestReleaseStageKnown(t *testing.T) {
	t.Parallel()

	assert.False(t, ReleaseStage{}.Known(), "no tag visible is unknown, never pre-1.0 by guess")
	assert.True(t, ReleaseStage{Tag: "v0.4.0"}.Known())
}
