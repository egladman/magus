package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestCommitToMap covers the Buzz boundary map, including the RFC3339 date
// formatting and the nested author record.
func TestCommitToMap(t *testing.T) {
	c := Commit{
		ID:      "deadbeef",
		Short:   "dead",
		Author:  Person{Name: "Eli", Email: "eli@example.com"},
		Date:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Subject: "fix",
		Body:    "the details",
		Parents: []string{"cafe"},
	}
	want := map[string]any{
		"id":      "deadbeef",
		"short":   "dead",
		"author":  map[string]any{"name": "Eli", "email": "eli@example.com"},
		"date":    "2026-01-02T03:04:05Z",
		"subject": "fix",
		"body":    "the details",
		"parents": []string{"cafe"},
	}
	assert.Equal(t, want, c.ToMap())
}

// A zero commit date must serialize as the empty string, not a formatted zero time.
func TestCommitToMapZeroDate(t *testing.T) {
	got := Commit{ID: "x"}.ToMap()
	assert.Equal(t, "", got["date"])
}

// TestTagToMap covers the Buzz boundary map, including the nested
// SemverVersion record and the RFC3339 date formatting.
func TestTagToMap(t *testing.T) {
	tag := Tag{
		Name:    "libs/gopherbuzz/v0.1.0",
		Prefix:  "libs/gopherbuzz/",
		Version: SemverVersion{Major: 0, Minor: 1, Patch: 0, Original: "0.1.0"},
		Date:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		ID:      "deadbeef",
	}
	want := map[string]any{
		"name":   "libs/gopherbuzz/v0.1.0",
		"prefix": "libs/gopherbuzz/",
		"version": map[string]any{
			"major": 0, "minor": 1, "patch": 0,
			"prerelease": "", "metadata": "", "original": "0.1.0",
		},
		"date": "2026-01-02T03:04:05Z",
		"id":   "deadbeef",
	}
	assert.Equal(t, want, tag.ToMap())
}

// A tag whose Name never parsed as semver carries the zero Version - test
// Version.Original == "" rather than a separate bool, and ToMap must nest
// that zero value rather than omitting the key.
func TestTagToMapNonSemver(t *testing.T) {
	got := Tag{Name: "checkpoint", ID: "x"}.ToMap()
	assert.Equal(t, SemverVersion{}.ToMap(), got["version"])
	assert.Equal(t, "", got["prefix"])
}

func TestVCSErrorSentinels(t *testing.T) {
	for _, sentinel := range []error{ErrVCSUnsupported, ErrVCSUnknown} {
		assert.NotNil(t, sentinel)
		assert.NotEmpty(t, sentinel.Error())
		assert.ErrorIs(t, sentinel, sentinel)
	}
}

func TestVCSSourceConstants(t *testing.T) {
	sources := []VCSSource{
		VCSSourceExplicit,
		VCSSourceAuto,
		VCSSourceDefault,
		VCSSourceDisabled,
	}
	seen := map[VCSSource]bool{}
	for _, s := range sources {
		assert.NotEmpty(t, string(s), "VCSSource constant is empty")
		assert.Falsef(t, seen[s], "duplicate VCSSource value %q", s)
		seen[s] = true
	}
}

func TestVCSResolution_ZeroValue(t *testing.T) {
	var r VCSResolution
	assert.Equal(t, VCSResolution{}, r)
	assert.Nil(t, r.VCS)
	assert.Empty(t, r.Name)
}
