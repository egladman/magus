package types

import (
	semver "github.com/Masterminds/semver/v3"

	"github.com/egladman/magus/types/release"
)

// NewestReleaseStage is the stage of the highest stable semver tag among tags that name the
// workspace root, so a nested module's `libs/x/v0.2.0` and a prerelease do not set it. A
// tag that is not semver-shaped is skipped. The zero stage means none was visible.
func NewestReleaseStage(tags []VCSTag) release.Stage {
	var (
		best  *semver.Version
		stage release.Stage
	)
	for _, t := range tags {
		if t.Prefix != "" || t.Version.Original == "" {
			continue
		}
		v, err := semver.NewVersion(t.Version.Original)
		if err != nil || v.Prerelease() != "" {
			continue
		}
		if best == nil || v.GreaterThan(best) {
			best, stage = v, release.Stage{Tag: t.Name, Major: int(v.Major())}
		}
	}
	return stage
}
