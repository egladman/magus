package types

import (
	semver "github.com/Masterminds/semver/v3"
)

// ReleaseStage is where the workspace's own releases stand, read from its newest tag.
// Derived only: no config key states or overrides it, so it cannot disagree with the tags.
type ReleaseStage struct {
	// Tag is the newest stable semver tag of the workspace root, as written. Empty when no
	// such tag is visible.
	Tag string `json:"tag,omitempty" yaml:"tag,omitempty"`
	// Major is that tag's major version. Meaningless while Tag is empty.
	Major int `json:"major" yaml:"major"`
}

// Known reports whether a tag was found. An unknown stage says nothing about whether the
// workspace is pre-1.0: it is never guessed.
func (s ReleaseStage) Known() bool { return s.Tag != "" }

// NewestReleaseStage is the stage of the highest stable semver tag among tags that name the
// workspace root, so a nested module's `libs/x/v0.2.0` and a prerelease do not set it. A
// tag that is not semver-shaped is skipped. The zero stage means none was visible.
func NewestReleaseStage(tags []VCSTag) ReleaseStage {
	var (
		best  *semver.Version
		stage ReleaseStage
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
			best, stage = v, ReleaseStage{Tag: t.Name, Major: int(v.Major())}
		}
	}
	return stage
}
