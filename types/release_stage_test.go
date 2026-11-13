package types

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types/release"
)

func newTag(name, prefix, version string) VCSTag {
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
		want release.Stage
	}{
		"no tags is unknown": {},
		"newest by semver, not by list order": {
			tags: []VCSTag{newTag("v0.9.0", "", "v0.9.0"), newTag("v0.10.0", "", "v0.10.0"), newTag("v0.2.0", "", "v0.2.0")},
			want: release.Stage{Tag: "v0.10.0", Major: 0},
		},
		"a major release": {
			tags: []VCSTag{newTag("v1.2.3", "", "v1.2.3"), newTag("v0.9.0", "", "v0.9.0")},
			want: release.Stage{Tag: "v1.2.3", Major: 1},
		},
		"a nested module tag does not set the stage": {
			tags: []VCSTag{newTag("libs/gopherbuzz/v1.0.0", "libs/gopherbuzz/", "v1.0.0"), newTag("v0.3.0", "", "v0.3.0")},
			want: release.Stage{Tag: "v0.3.0", Major: 0},
		},
		"a prerelease does not set the stage": {
			tags: []VCSTag{newTag("v1.0.0-rc.1", "", "v1.0.0-rc.1"), newTag("v0.9.0", "", "v0.9.0")},
			want: release.Stage{Tag: "v0.9.0", Major: 0},
		},
		"a tag that is not semver is skipped": {
			tags: []VCSTag{newTag("checkpoint", "", "")},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, NewestReleaseStage(tc.tags))
		})
	}
}
