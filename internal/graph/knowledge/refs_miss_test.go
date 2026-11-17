package knowledge

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/textindex"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/types"
)

// A bare name two workspace definitions carry must not quietly answer for one of them: refs
// exits 2 and names each candidate as a command that asks for it alone.
func TestRefsSymbolRefusesANameSeveralDefinitionsCarry(t *testing.T) {
	const hintID, queueID, depID = "symbol:x `hint`/Classify().", "symbol:x `queue`/Workspace#Classify().", "symbol:dep/Classify()."
	sym := func(id string) types.KnowledgeNode {
		return types.KnowledgeNode{ID: id, Kind: types.KindSymbol, Label: "Classify"}
	}
	defines := func(file, id string) types.KnowledgeEdge {
		return types.KnowledgeEdge{Source: "file:" + file, Target: id, Relation: types.RelationDefines, Confidence: types.ConfidenceExtracted, Score: 1}
	}
	g := NewGraph()
	g.Merge([]types.KnowledgeNode{sym(hintID), sym(queueID), sym(depID)},
		[]types.KnowledgeEdge{defines("hint/a.go", hintID), defines("queue/b.go", queueID)})

	var stderr bytes.Buffer
	_, err := refsSymbol(&stderr, g, "Classify")

	assert.Equal(t, exit.Usage, exit.Code(err))
	assert.True(t, exit.AlreadyReported(err))
	assert.Equal(t, fmt.Sprintf(`magus refs: "Classify" names 2 symbols defined in this workspace; ask for one by id:
  %s
  %s
`, hint.Refs.With("'"+hintID+"'"), hint.Refs.With("'"+queueID+"'")), stderr.String())

	got, err := refsSymbol(&stderr, g, queueID)
	require.NoError(t, err)
	assert.Equal(t, queueID, got, "an exact id picks itself")

	only := NewGraph()
	only.Merge([]types.KnowledgeNode{sym(hintID), sym(depID)}, []types.KnowledgeEdge{defines("hint/a.go", hintID)})
	got, err = refsSymbol(&stderr, only, "Classify")
	require.NoError(t, err)
	assert.Equal(t, hintID, got, "the one workspace definition wins over a dependency's symbol of that name")
}

func TestBuzzImportersAnswersFromTheImportEdges(t *testing.T) {
	g := NewGraph()
	g.Merge([]types.KnowledgeNode{{ID: "file:a.buzz", Kind: types.KindFile}, {ID: "file:b.buzz", Kind: types.KindFile}, {ID: "file:c.buzz", Kind: types.KindFile}},
		[]types.KnowledgeEdge{
			{Source: "file:a.buzz", Target: "file:c.buzz", Relation: types.RelationImports, Confidence: types.ConfidenceExtracted, Score: 1},
			{Source: "file:b.buzz", Target: "file:c.buzz", Relation: types.RelationImports, Confidence: types.ConfidenceExtracted, Score: 1},
			{Source: "file:c.buzz", Target: "file:a.buzz", Relation: types.RelationDefines, Confidence: types.ConfidenceExtracted, Score: 1},
		})
	importers := BuzzImporters(g)
	assert.ElementsMatch(t, []string{"a.buzz", "b.buzz"}, importers("c.buzz"))
	assert.Empty(t, importers("a.buzz"), "only import edges count")
}

// A name refs cannot resolve, from a workspace with an unbuilt index: -o json prints the
// record with its verdict and index cause on stdout.
func TestReportRefsMissCarriesTheIndexCause(t *testing.T) {
	ans := types.ClassifyAnswer(false, "", []types.KnowledgeSymbolGap{{Project: types.NewProjectRef("libs/api", ""), State: types.SymbolIndexNotBuilt}})
	ans.IndexCause = &types.KnowledgeIndexCause{
		Why: "no server is running, so the refresh hook's `magus job run sync-graph` does nothing",
		Fix: "`magus server start` keeps it current from now on",
	}
	miss := RefsMiss{Name: "Foo", Answer: ans}

	var stdout, stderr bytes.Buffer
	env := testEnv(&stdout, &stderr)
	require.NoError(t, ReportRefsMiss(&stderr, env, output.Options{Format: output.JSON}, miss))
	var record types.KnowledgeRefsOutput
	require.NoError(t, codec.Unmarshal(stdout.Bytes(), &record), "stdout: %q", stdout.String())
	assert.Equal(t, types.KnowledgeRefsOutput{
		Definition: record.Definition, // the noun's prose, which the describe catalog pins
		APIVersion: record.APIVersion, // the wire version, pinned by its own test
		Symbol:     "Foo",
		Answer: types.KnowledgeAnswer{
			Verdict:    types.VerdictUnknown,
			Reason:     types.ReasonSymbolIndexMissing,
			Gaps:       record.Answer.Gaps, // the gap's own wire form is pinned by the cmd test
			IndexCause: ans.IndexCause,
		},
	}, record)
	assert.Contains(t, stdout.String(), `"index_cause"`)

	stdout.Reset()
	stderr.Reset()
	require.NoError(t, ReportRefsMiss(&stderr, env, output.Options{Format: output.Text}, miss))
	assert.Empty(t, stdout.String(), "text output keeps stdout for answers")
	assert.Contains(t, stderr.String(), `magus refs: no node matches "Foo"`)
}

func TestMissPresenceCapsSitesAndCountsTheRest(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "ws")
	var matches []textindex.Match
	for i := 1; i <= types.TextPresenceSiteCap+3; i++ {
		matches = append(matches, textindex.Match{Path: filepath.Join(root, "pkg", "a.go"), Line: i})
	}
	matches = append(matches, textindex.Match{Path: filepath.Join(root, "b.go"), Line: 7})

	sites := make([]string, types.TextPresenceSiteCap)
	for i := range sites {
		sites[i] = fmt.Sprintf("pkg/a.go:%d", i+1)
	}
	assert.Equal(t, types.KnowledgeTextPresence{Hits: types.TextPresenceSiteCap + 4, Files: 2, Sites: sites, More: 4},
		MissPresence(root, matches))
}

// A symbol miss with text hits names the sites in the text block and in the -o json record.
func TestReportRefsMissPrintsTextSites(t *testing.T) {
	ans := types.ClassifyAnswer(false, "", nil)
	ans.Text = &types.KnowledgeTextPresence{Hits: 3, Files: 2, Sites: []string{"a/b.go:4", "c.go:9"}, More: 1}
	miss := RefsMiss{Name: "Foo", Answer: ans, Searched: 10}

	var stdout, stderr bytes.Buffer
	env := testEnv(&stdout, &stderr)
	require.NoError(t, ReportRefsMiss(&stderr, env, output.Options{Format: output.Text}, miss))
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "    a/b.go:4\n    c.go:9\n    ... and 1 more\n")

	stderr.Reset()
	require.NoError(t, ReportRefsMiss(&stderr, env, output.Options{Format: output.JSON}, miss))
	var record types.KnowledgeRefsOutput
	require.NoError(t, codec.Unmarshal(stdout.Bytes(), &record), "stdout: %q", stdout.String())
	assert.Equal(t, ans.Text, record.Answer.Text)
}

// An absent verdict with no text match says what the text search did not read.
func TestReportRefsMissNamesTheSkippedFilesUnderAbsent(t *testing.T) {
	miss := RefsMiss{Name: "Foo", Answer: types.ClassifyAnswer(false, "", nil), Searched: 10, Notes: textindex.PresenceNotes(6, 0, 0, true, false)}
	var stdout, stderr bytes.Buffer
	require.NoError(t, ReportRefsMiss(&stderr, testEnv(&stdout, &stderr), output.Options{Format: output.Text}, miss))
	assert.Contains(t, stderr.String(), "verdict: absent")
	assert.Contains(t, stderr.String(), "no text match in 10 file(s) searched (6 skipped: binary, empty, or over")
}
