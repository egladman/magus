package release

// Stage is where the workspace's own releases stand, read from its newest tag.
// Derived only: no config key states or overrides it, so it cannot disagree
// with the tags.
type Stage struct {
	// Tag is the newest stable semver tag of the workspace root, as written.
	// Empty when no such tag is visible.
	Tag string `json:"tag,omitempty" yaml:"tag,omitempty"`
	// Major is that tag's major version. Meaningless while Tag is empty.
	Major int `json:"major" yaml:"major"`
}

// Known reports whether a tag was found. An unknown stage says nothing about
// whether the workspace is pre-1.0: it is never guessed.
func (s Stage) Known() bool { return s.Tag != "" }
