package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestCommitBuzzObject covers the Buzz boundary map, including the RFC3339 date
// formatting and the nested author record.
func TestCommitBuzzObject(t *testing.T) {
	c := Commit{
		ID:      "deadbeef",
		Short:   "dead",
		Author:  Person{Name: "Eli", Email: "eli@example.com"},
		Date:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Subject: "fix",
		Body:    "the details",
		Parents: []string{"cafe"},
	}
	want := BuzzObject{
		"id":      "deadbeef",
		"short":   "dead",
		"author":  BuzzObject{"name": "Eli", "email": "eli@example.com"},
		"date":    "2026-01-02T03:04:05Z",
		"subject": "fix",
		"body":    "the details",
		"parents": []string{"cafe"},
	}
	assert.Equal(t, want, c.BuzzObject())
}

// A zero commit date must serialize as the empty string, not a formatted zero time.
func TestCommitBuzzObjectZeroDate(t *testing.T) {
	got := Commit{ID: "x"}.BuzzObject()
	assert.Equal(t, "", got["date"])
}

// TestTagBuzzObject covers the Buzz boundary map, including the nested
// SemverVersion record and the RFC3339 date formatting.
func TestTagBuzzObject(t *testing.T) {
	tag := VCSTag{
		Name:    "libs/gopherbuzz/v0.1.0",
		Prefix:  "libs/gopherbuzz/",
		Version: SemverVersion{Major: 0, Minor: 1, Patch: 0, Original: "0.1.0"},
		Date:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		ID:      "deadbeef",
	}
	want := BuzzObject{
		"name":   "libs/gopherbuzz/v0.1.0",
		"prefix": "libs/gopherbuzz/",
		"version": BuzzObject{
			"major": 0, "minor": 1, "patch": 0,
			"prerelease": "", "metadata": "", "original": "0.1.0",
		},
		"date": "2026-01-02T03:04:05Z",
		"id":   "deadbeef",
	}
	assert.Equal(t, want, tag.BuzzObject())
}

// A tag whose Name never parsed as semver carries the zero Version - test
// Version.Original == "" rather than a separate bool, and BuzzObject must nest
// that zero value rather than omitting the key.
func TestTagBuzzObjectNonSemver(t *testing.T) {
	got := VCSTag{Name: "checkpoint", ID: "x"}.BuzzObject()
	assert.Equal(t, SemverVersion{}.BuzzObject(), got["version"])
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

// TestClassifyDrift pins the fork that `magus vcs add` and the generate gate now share.
// They used to answer the same question in two places, so the same condition could be
// reported two different ways depending on which surface you met it on.
func TestClassifyDrift(t *testing.T) {
	// An input moved too: regeneration is expected, and the output belongs in the commit.
	code, msg := ClassifyDrift(true, "v0.3.0")
	assert.Equal(t, StaleGeneratedOutput, code)
	assert.Contains(t, msg, "commit")

	// Inputs unchanged on a DEV build: version skew, not the developer's change. The
	// version is named so the reader can see which build produced it.
	code, msg = ClassifyDrift(false, "v0.3.0-288-ga103255f")
	assert.Equal(t, EnvironmentalDrift, code)
	assert.Contains(t, msg, "v0.3.0-288-ga103255f")
	assert.Contains(t, msg, "do not commit")

	// An unknown version is a dev build too, and says so rather than printing an empty
	// pair of parentheses.
	_, msg = ClassifyDrift(false, "")
	assert.Contains(t, msg, "(unknown)")

	// Inputs unchanged on a RELEASE build: same inputs, same generator, different bytes.
	code, _ = ClassifyDrift(false, "v0.3.0")
	assert.Equal(t, NondeterministicOutput, code)
}
