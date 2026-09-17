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

// TestStaleSourceProjects pins the complement SplitExplainedOutputs does not answer: a
// project whose source changed but whose output did not move AT ALL in the same files.
func TestStaleSourceProjects(t *testing.T) {
	t.Run("source and output both moved: not stale", func(t *testing.T) {
		files := []FileEntry{
			{Path: "api/schema.proto", Role: "source", SourceOf: []string{"api"}},
			{Path: "api/gen/schema.pb.go", Role: "output", OutputOf: []string{"api"}},
		}
		assert.Empty(t, StaleSourceProjects(files))
	})

	t.Run("source moved with no matching output: stale", func(t *testing.T) {
		files := []FileEntry{
			{Path: "api/schema.proto", Role: "source", SourceOf: []string{"api"}},
		}
		assert.Equal(t, []string{"api"}, StaleSourceProjects(files))
	})

	t.Run("output moved with no source: not this function's question", func(t *testing.T) {
		// This is SplitExplainedOutputs' unexplained case (MGS4005/MGS4003), not a stale
		// source: there is no source project to report as stale here.
		files := []FileEntry{
			{Path: "api/gen/schema.pb.go", Role: "output", OutputOf: []string{"api"}},
		}
		assert.Empty(t, StaleSourceProjects(files))
	})

	t.Run("one project regenerated, a sibling did not: only the sibling is stale", func(t *testing.T) {
		files := []FileEntry{
			{Path: "api/schema.proto", Role: "source", SourceOf: []string{"api"}},
			{Path: "api/gen/schema.pb.go", Role: "output", OutputOf: []string{"api"}},
			{Path: "web/schema.graphql", Role: "source", SourceOf: []string{"web"}},
		}
		assert.Equal(t, []string{"web"}, StaleSourceProjects(files))
	})

	t.Run("multiple stale projects come back sorted", func(t *testing.T) {
		files := []FileEntry{
			{Path: "web/schema.graphql", Role: "source", SourceOf: []string{"web"}},
			{Path: "api/schema.proto", Role: "source", SourceOf: []string{"api"}},
		}
		assert.Equal(t, []string{"api", "web"}, StaleSourceProjects(files))
	})

	assert.Empty(t, StaleSourceProjects(nil))
}
