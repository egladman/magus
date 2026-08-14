package main

import (
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadAffectedPlanPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		null  bool
		want  []string
	}{
		{
			name:  "newline paths are sorted and deduplicated",
			input: "z/file.go\na file.go\n\nz/file.go\n",
			want:  []string{"a file.go", "z/file.go"},
		},
		{
			name:  "nul batches collapse into one plan",
			input: "z/file.go\x00\x00a file.go\x00z/file.go\x00",
			null:  true,
			want:  []string{"a file.go", "z/file.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := readAffectedPlanPaths(strings.NewReader(tt.input), tt.null)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestShardSkillsDeriveFromWhatTheShardDoes pins the derivation, not a table. A second
// copy of the routing table would drift the first time a project changed spells; deriving
// it from the shard's own declarations cannot. Each verdict carries its reason because a
// routing decision an agent cannot audit is one it should not act on.
func TestShardSkillsDeriveFromWhatTheShardDoes(t *testing.T) {
	t.Parallel()

	skills, why := shardSkills(shardDetail{})
	assert.Equal(t, []string{"magus-run-full"}, skills, "every shard is a target invocation, named as the twin a delegate can rely on")
	assert.Len(t, why, 2, "the verdict plus the variant note")

	skills, why = shardSkills(shardDetail{Writes: []string{"gen/*.json"}})
	assert.Equal(t, []string{"magus-run-full", "magus-vcs-hygiene-full"}, skills,
		"a shard that writes declared outputs leaves generated files behind, which is magus-vcs-hygiene's whole subject")
	assert.Len(t, why, 3)

	// Exclusivity is not a skill, but it IS something an orchestrator must not miss: it
	// says this shard cannot be handed out beside another.
	_, why = shardSkills(shardDetail{Exclusive: true})
	assert.Contains(t, strings.Join(why, "\n"), "exclusive")
}

// TestJoinProjectGlobRootsTheCollisionSurface: two briefings are compared for overlap, so
// a glob that stayed project-relative would read as colliding with an identically-named
// one in a different project - the exact mistake the field exists to prevent.
func TestJoinProjectGlobRootsTheCollisionSurface(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "console/gen/**", joinProjectGlob("console", "gen/**"))
	assert.Equal(t, "gen/*.json", joinProjectGlob(".", "gen/*.json"), "the root project is already the workspace")
	assert.Equal(t, "docs/MAGUS.md", joinProjectGlob("docs", "docs/MAGUS.md"), "an already-rooted glob is left alone")
}

func TestAppendUniquePreservesOrderAndDropsBlanks(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"go", "buzz"}, appendUnique(nil, "go", "", "buzz", "go"))
}

// TestFilterShardsIntersectsRatherThanReplaces pins the distinction the filter exists for.
// `magus run ci docs` runs docs whether or not the diff touched it; this answers "of the
// work this change implies, give me the docs part" - which is what an orchestrator
// splitting an affected set across agents is actually asking.
func TestFilterShardsIntersectsRatherThanReplaces(t *testing.T) {
	t.Parallel()
	shards := []types.Shard{
		{ID: "0", ProjectPaths: []string{".", "docs"}},
		{ID: "1", ProjectPaths: []string{"console"}},
		{ID: "2", ProjectPaths: []string{"proto"}},
	}

	got := filterShardPaths(shards, map[string]bool{"docs": true, "proto": true})

	require.Len(t, got, 2, "a shard left with nothing drops out")
	// IDs are preserved, not renumbered, so a filtered plan reads against the one it came
	// from - shard 1 is absent rather than shard 2 being renamed to 1.
	assert.Equal(t, "0", got[0].ID)
	assert.Equal(t, []string{"docs"}, got[0].ProjectPaths, "the unselected project leaves the shard it shared")
	assert.Equal(t, "2", got[1].ID)
	assert.Equal(t, []string{"proto"}, got[1].ProjectPaths)
}

// TestFilterShardPathsEmptyWhenNothingMatches: a real project outside the affected set is
// the honest empty answer, not an error - that is precisely the question being asked.
func TestFilterShardPathsEmptyWhenNothingMatches(t *testing.T) {
	t.Parallel()
	got := filterShardPaths([]types.Shard{{ID: "0", ProjectPaths: []string{"docs"}}}, map[string]bool{"libs/foo": true})
	assert.Empty(t, got)
}
