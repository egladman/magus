package knowledge

import (
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func daysAgo(n int) time.Time { return time.Now().UTC().AddDate(0, 0, -n) }

// proseShards builds a doc and a note, each pointing at one subject file.
func proseShards() []Shard {
	return []Shard{{
		Name: "@test",
		Nodes: []types.KnowledgeNode{
			{ID: "doc:docs/cache.md", Kind: types.KindDoc, Source: "docs/cache.md"},
			{ID: "note:cache-pairing", Kind: types.KindNote, Source: "notes/cache-pairing.md"},
			{ID: "file:internal/cache/cache.go", Kind: types.KindFile, Source: "internal/cache/cache.go"},
			{ID: "spell:go", Kind: types.KindSpell},
		},
		Edges: []types.KnowledgeEdge{
			{Source: "doc:docs/cache.md", Target: "file:internal/cache/cache.go", Relation: types.RelationDocuments},
			{Source: "note:cache-pairing", Target: "file:internal/cache/cache.go", Relation: types.RelationAnnotates},
		},
	}}
}

func TestAnnotateProseStaleness(t *testing.T) {
	cases := []struct {
		name          string
		proseDaysAgo  int
		subjectDaysAg int
		want          string
		wantDays      bool
	}{
		{name: "prose newer than its subject", proseDaysAgo: 10, subjectDaysAg: 100, want: StalenessCurrent},
		{name: "prose and subject the same day", proseDaysAgo: 10, subjectDaysAg: 10, want: StalenessCurrent},
		{name: "subject moved on a few months later", proseDaysAgo: 200, subjectDaysAg: 100, want: StalenessOutrun, wantDays: true},
		{name: "subject has been moving for years", proseDaysAgo: 900, subjectDaysAg: 5, want: StalenessPetrified, wantDays: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shards := proseShards()
			annotateProseStaleness(shards, vcsByPath([]types.KnowledgeVCS{
				{Path: "docs/cache.md", LastModified: daysAgo(tc.proseDaysAgo)},
				{Path: "notes/cache-pairing.md", LastModified: daysAgo(tc.proseDaysAgo)},
				{Path: "internal/cache/cache.go", LastModified: daysAgo(tc.subjectDaysAg)},
			}))

			for _, id := range []string{"doc:docs/cache.md", "note:cache-pairing"} {
				n := nodeIn(t, shards, id)
				assert.Equal(t, tc.want, n.Attrs[AttrStaleness], "%s", id)
				if tc.wantDays {
					assert.NotEmpty(t, n.Attrs[AttrOutrunDays], "the evidence must travel with the verdict")
				} else {
					assert.Empty(t, n.Attrs[AttrOutrunDays])
				}
			}
			// A non-prose node is never annotated: staleness is a claim about prose
			// falling behind, not about code being old.
			assert.Empty(t, nodeIn(t, shards, "file:internal/cache/cache.go").Attrs[AttrStaleness])
		})
	}
}

// TestAnnotateProseStalenessIsSilentWithoutEvidence pins the direction this must fail in.
// An unmeasured node is not "fresh" - it carries no attr at all, and ranking leaves it
// alone. Manufacturing a verdict from absent data is how a signal becomes noise.
func TestAnnotateProseStalenessIsSilentWithoutEvidence(t *testing.T) {
	t.Run("no vcs history at all (the default: knowledge.vcs.enabled is off)", func(t *testing.T) {
		shards := proseShards()
		annotateProseStaleness(shards, vcsByPath(nil))
		assert.Empty(t, nodeIn(t, shards, "doc:docs/cache.md").Attrs[AttrStaleness])
	})

	t.Run("history for the subject but not the prose", func(t *testing.T) {
		shards := proseShards()
		annotateProseStaleness(shards, vcsByPath([]types.KnowledgeVCS{
			{Path: "internal/cache/cache.go", LastModified: daysAgo(1)},
		}))
		assert.Empty(t, nodeIn(t, shards, "doc:docs/cache.md").Attrs[AttrStaleness])
	})

	t.Run("prose with no subject to fall behind", func(t *testing.T) {
		shards := []Shard{{
			Name:  "@test",
			Nodes: []types.KnowledgeNode{{ID: "doc:docs/orphan.md", Kind: types.KindDoc, Source: "docs/orphan.md"}},
		}}
		annotateProseStaleness(shards, vcsByPath([]types.KnowledgeVCS{
			{Path: "docs/orphan.md", LastModified: daysAgo(900)},
		}))
		assert.Empty(t, nodeIn(t, shards, "doc:docs/orphan.md").Attrs[AttrStaleness],
			"a doc that documents nothing measurable cannot be behind anything")
	})
}

func TestStalenessLabel(t *testing.T) {
	verdict, days := stalenessLabel(nil)
	assert.Empty(t, verdict, "unmeasured makes no claim")
	assert.Zero(t, days)

	verdict, _ = stalenessLabel(map[string]string{AttrStaleness: StalenessCurrent})
	assert.Empty(t, verdict, "keeping up is not a finding")

	verdict, days = stalenessLabel(map[string]string{
		AttrStaleness: StalenessPetrified, AttrOutrunDays: "400",
	})
	assert.Equal(t, StalenessPetrified, verdict)
	assert.Equal(t, 400, days, "the day count is the evidence and must reach the caller")
}

// Staleness annotates and never reorders. The subject that keeps moving is where knowledge is
// worth most (churn predicts defect density), and prose whose subject is gone is often the
// only surviving evidence of why it went - ranking either one down buries it exactly when it
// is needed. Guru and g3doc both label rather than demote for the same reason.
func TestResolveLabelsStalenessWithoutReordering(t *testing.T) {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{
		ID: "doc:docs/cache.md", Kind: types.KindDoc, Label: "cache",
		Attrs: map[string]string{AttrStaleness: StalenessPetrified, AttrOutrunDays: "400"},
	})
	g.AddNode(types.KnowledgeNode{ID: "doc:docs/cache-current.md", Kind: types.KindDoc, Label: "cache"})

	matches := g.Resolve("cache", 0)
	require.Len(t, matches, 2)

	byID := map[string]types.KnowledgeMatch{}
	for _, m := range matches {
		byID[m.ID] = m
	}
	stale, current := byID["doc:docs/cache.md"], byID["doc:docs/cache-current.md"]

	assert.Equal(t, current.Score, stale.Score, "being behind costs a match no rank")
	assert.Equal(t, StalenessPetrified, stale.Staleness)
	assert.Equal(t, 400, stale.OutrunDays, "the number is the evidence, and it must reach the caller")
	assert.Empty(t, current.Staleness, "prose that kept up carries no staleness claim")
	assert.Zero(t, current.OutrunDays)
}

func nodeIn(t *testing.T, shards []Shard, id string) types.KnowledgeNode {
	t.Helper()
	for _, sh := range shards {
		for _, n := range sh.Nodes {
			if n.ID == id {
				return n
			}
		}
	}
	t.Fatalf("no node %q", id)
	return types.KnowledgeNode{}
}

// TestOutrunDaysCountsCalendarDays pins the unit. Subtracting raw instants truncates, so
// prose committed late one evening and a subject edited two dates later reports 1 - which
// reads as "yesterday" for a two-day gap, and would misplace prose sitting on the petrified
// cutoff. Days are also what the graph publishes (vcs_last_modified is a date), so this is
// the arithmetic a reader can check the number against.
func TestOutrunDaysCountsCalendarDays(t *testing.T) {
	late := time.Date(2026, 8, 11, 23, 0, 0, 0, time.UTC)

	assert.Equal(t, 2, outrunDays(late, late.Add(47*time.Hour)), "23:00 Tue -> 22:00 Thu is two dates, not one")
	assert.Equal(t, 0, outrunDays(late, late.Add(30*time.Minute)), "the same date is not a day behind")
	assert.Equal(t, 1, outrunDays(late, late.Add(2*time.Hour)), "crossing midnight is one date")

	// A zone ahead of UTC is normalized, not counted twice: the day is the UTC day the
	// published attr prints, whatever the committer's clock said.
	tokyo := time.FixedZone("JST", 9*60*60)
	assert.Equal(t, 0, outrunDays(late, time.Date(2026, 8, 12, 7, 0, 0, 0, tokyo)))

	// Prose newer than its subject goes negative, which annotateProseStaleness reads as
	// current - it must not wrap to a large positive and report a petrified doc.
	assert.Negative(t, outrunDays(late, late.AddDate(0, 0, -5)))
}
