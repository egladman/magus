package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

// TestStaleIndexNotice: the banner names what is stale and the one command that fixes it,
// and says nothing at all when nothing is.
func TestStaleIndexNotice(t *testing.T) {
	answer := func(stale ...types.KnowledgeStaleIndex) types.KnowledgeAnswer {
		return knowledge.Answer("Foo", true, knowledge.Coverage{Seeded: true, Probed: true, Stale: stale})
	}
	got := staleIndexNotice(answer(types.KnowledgeStaleIndex{Project: "libs/api"}, types.KnowledgeStaleIndex{Project: "."}))
	assert.Contains(t, got, "stale index")
	assert.Contains(t, got, "libs/api", "the reader has to know WHICH answers are short")
	assert.Contains(t, got, "magus graph build", "and the command that fixes it")
	assert.Contains(t, got, "projects changed", "two projects read as plural")

	assert.Contains(t, staleIndexNotice(answer(types.KnowledgeStaleIndex{Project: "."})), "a project changed")
	assert.Empty(t, staleIndexNotice(answer()),
		"a current index draws silence; a banner on every lookup is one nobody reads")

	both := staleIndexNotice(answer(
		types.KnowledgeStaleIndex{Project: ".", Language: "buzz", Op: "scip-buzz"},
		types.KnowledgeStaleIndex{Project: ".", Language: "go", Op: "scip"}))
	assert.Contains(t, both, "a project changed", "two indexes of one project are one project")
	assert.Contains(t, both, "missing sites in . (buzz index), . (go index).", "and the line says which of its indexes is behind")
}

// An answer from a server that predates per-index detail still names its projects.
func TestStaleIndexNoticeFromProjectsAlone(t *testing.T) {
	got := staleIndexNotice(types.KnowledgeAnswer{Verdict: types.VerdictFound, StaleIndexes: []string{"libs/api"}})
	assert.Contains(t, got, "missing sites in libs/api.")
}

// A graph read mid-rebase is told why the graph is stale before anything else, and when
// rebuilding it is worth it.
func TestStaleGraphAdviceLeadsWithWhy(t *testing.T) {
	underway := staleGraphAdviceFor("a rebase is in progress", nil)
	assert.True(t, strings.HasPrefix(underway,
		"magus workspace: the graph is stale, a rebase is in progress. Run `magus graph build` once any merge or rebase is finished, then ask again."), underway)

	api := []types.KnowledgeStaleIndex{{Project: "libs/api"}}
	both := staleGraphAdviceFor("the index was built at 0123456789ab and the checkout is at fedcba9", api)
	assert.True(t, strings.HasPrefix(both, "magus workspace: the graph is stale, the index was built at 0123456789ab and the checkout is at fedcba9."), both)
	assert.Contains(t, both, "One project changed since magus graph build last indexed it: libs/api.")

	projects := staleGraphAdviceFor("", api)
	assert.True(t, strings.HasPrefix(projects, "magus workspace: run `magus graph build` first, then ask again."), projects)

	root := staleGraphAdviceFor("", []types.KnowledgeStaleIndex{{Project: ".", Language: "buzz"}, {Project: ".", Language: "go"}})
	assert.Contains(t, root, "One project changed since magus graph build last indexed it: . (buzz index), . (go index).", root)

	assert.Empty(t, staleGraphAdviceFor("", nil))
}

// The contradiction this closes: query printed "verdict: absent (magus searched everything
// it could reach)" and "stale index: ..." in one output. Both halves now read the same
// answer, so an index the verdict did not account for cannot be announced under it.
func TestStalenessBannerAgreesWithTheVerdict(t *testing.T) {
	cov := knowledge.Coverage{Seeded: true, Probed: true, Stale: []types.KnowledgeStaleIndex{{Project: "libs/api", Language: "go"}}}

	render := func(ans types.KnowledgeAnswer) string {
		var buf bytes.Buffer
		printVerdict(&buf, ans, "")
		_ = reportIndexStaleness(&buf, ans)
		return buf.String()
	}

	// The miss the index explains: the verdict carries it, so the banner stays out of the
	// way rather than repeating the projects and the refresh a second time.
	miss := render(knowledge.Answer("Foo", false, cov))
	assert.Contains(t, miss, "verdict: unknown, not absent")
	assert.Contains(t, miss, "libs/api")
	assert.NotContains(t, miss, "stale index")

	// A hit: the rows are facts and the index's age is a caveat on them, which is the case
	// the banner exists for.
	hit := render(knowledge.Answer("Foo", true, cov))
	assert.Contains(t, hit, "stale index")
	assert.Contains(t, hit, "libs/api")

	// A kind outside the lazy layer: the symbol index could not have held the answer, so
	// the absence is verified and naming a stale index would point at a layer never in scope.
	outOfScope := render(knowledge.Answer("kind=author nobody", false, cov))
	assert.Contains(t, outOfScope, "absent (magus searched everything")
	assert.NotContains(t, outOfScope, "stale index")
}
