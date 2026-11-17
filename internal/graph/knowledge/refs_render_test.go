package knowledge

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/textindex"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/types"
)

// testEnv is a RefsEnv over buffers. Its advice is the minimum a renderer's output
// depends on: the verdict names itself, and a stale index fails the answer.
func testEnv(stdout, notices io.Writer) RefsEnv {
	return RefsEnv{
		Stdout:   stdout,
		Notices:  notices,
		Classify: func() textindex.ClassifyFunc { return nil },
		Format: func(_ output.Options, v any) error {
			b, err := json.Marshal(v)
			if err != nil {
				return err
			}
			_, err = stdout.Write(b)
			return err
		},
		Names: func(w io.Writer, names []string) error {
			for _, n := range names {
				fmt.Fprintln(w, n)
			}
			return nil
		},
		IndexCause: func(ans types.KnowledgeAnswer) types.KnowledgeAnswer { return ans },
	}
}

func TestEmitOccurrencesPrintsTheirBlockUnderTheirSite(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("one\ntwo\nthree\nfour\nfive\n"), 0o644))
	files := []types.SymbolOccurrenceFile{{File: "a.go", Occurrences: []types.SymbolOccurrence{{Line: 3, EndLine: 3, Status: types.SymbolOccurrenceVerified}}}}
	refs := types.KnowledgeRefsOutput{Symbol: "symbol:a F().", Label: "F", Answer: types.KnowledgeAnswer{Verdict: types.VerdictFound}}

	var stdout, notices bytes.Buffer
	env := testEnv(&stdout, &notices)
	env.Root = root
	err := EmitOccurrences(context.Background(), env, output.Options{Format: output.Text}, refs,
		&OccurrenceRead{Files: files, Names: []string{"F"}}, RefsWindow{SourceWindow: SourceWindow{Show: true, To: 1}}, RefsPage{})
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "    3:0-3:0  verified\n      >3  three\n       4  four\n")
}

func TestEmitOccurrencesWithoutAReadExitsOne(t *testing.T) {
	var stdout, notices bytes.Buffer
	err := EmitOccurrences(context.Background(), testEnv(&stdout, &notices), output.Options{Format: output.Text},
		types.KnowledgeRefsOutput{}, nil, RefsWindow{}, RefsPage{})
	assert.Equal(t, exit.Reported(exit.Failure), err)
	assert.Empty(t, stdout.String(), "a failed probe is not an empty list")
}

func TestEmitOccurrencesWithAnUnverifiedSiteExitsOne(t *testing.T) {
	files := []types.SymbolOccurrenceFile{{File: "a.go", Occurrences: []types.SymbolOccurrence{{Line: 3, EndLine: 3, Status: types.SymbolOccurrenceMismatch}}}}
	refs := types.KnowledgeRefsOutput{Symbol: "symbol:a F().", Label: "F", Answer: types.KnowledgeAnswer{Verdict: types.VerdictFound}}

	var stdout, notices bytes.Buffer
	env := testEnv(&stdout, &notices)
	env.Root = t.TempDir()
	err := EmitOccurrences(context.Background(), env, output.Options{Format: output.Name}, refs,
		&OccurrenceRead{Files: files, Names: []string{"F"}}, RefsWindow{}, RefsPage{})
	assert.Equal(t, exit.Reported(exit.Failure), err)
	assert.Empty(t, stdout.String(), "-o name lists verified sites only")
}

func TestEmitDefinitionsText(t *testing.T) {
	out := types.KnowledgeDefinitionsOutput{
		Symbol: "symbol:a F().", Label: "F",
		Definitions: []types.KnowledgeDefinitionSite{
			{File: "a.go", StartLine: 3, EndLine: 5, Status: types.DefinitionVerified, Source: "func F() {\n\treturn\n}", SourceStartLine: 3},
			{File: "b.go", StartLine: 9, Status: types.DefinitionVerified, Source: "var F = 1", SourceStartLine: 9},
			{File: "c.go"},
		},
		Answer: types.KnowledgeAnswer{Verdict: types.VerdictFound},
	}
	var stdout, notices bytes.Buffer
	require.NoError(t, EmitDefinitions(context.Background(), testEnv(&stdout, &notices), output.Options{Format: output.Text}, out))
	assert.Equal(t, "symbol: symbol:a F().  (F)\n"+
		"a.go:3-5  verified\n>3  func F() {\n 4  \treturn\n 5  }\n"+
		"\nb.go:9  verified  (the index recorded no end line; the declaration line alone)\n>9  var F = 1\n"+
		"c.go  (the index recorded no line in this file)\n", stdout.String())
}

// A changed range still prints, marked, but the exit status says the lines are not the
// symbol's; -o name withholds it, since a bare range has nowhere to carry the mark.
func TestEmitDefinitionsChangedExitsOne(t *testing.T) {
	out := types.KnowledgeDefinitionsOutput{
		Symbol: "symbol:a F().", Label: "F",
		Definitions: []types.KnowledgeDefinitionSite{
			{File: "a.go", StartLine: 3, EndLine: 5, Status: types.DefinitionChanged},
			{File: "b.go", StartLine: 1, EndLine: 2, Status: types.DefinitionVerified},
		},
		Answer: types.KnowledgeAnswer{Verdict: types.VerdictFound},
	}
	var stdout, notices bytes.Buffer
	err := EmitDefinitions(context.Background(), testEnv(&stdout, &notices), output.Options{Format: output.Text}, out)
	assert.Equal(t, exit.Reported(exit.Failure), err)
	assert.Contains(t, stdout.String(), "a.go:3-5  changed  (edited since indexing; refresh with `")

	stdout.Reset()
	err = EmitDefinitions(context.Background(), testEnv(&stdout, &notices), output.Options{Format: output.Name}, out)
	assert.Equal(t, exit.Reported(exit.Failure), err)
	assert.Equal(t, "b.go:1-2\n", stdout.String())
}

// A stale index fails a refs answer, but a digest-verified definition is proof of itself:
// the notice still prints, and only an answer with an unverified site exits 1 for it.
func TestEmitDefinitionsStaleIndex(t *testing.T) {
	answer := types.KnowledgeAnswer{Verdict: types.VerdictFound, StaleIndexes: []string{"."}, Reason: types.ReasonIndexStale}
	site := types.KnowledgeDefinitionSite{File: "a.go", StartLine: 3, EndLine: 5, Status: types.DefinitionVerified}
	out := types.KnowledgeDefinitionsOutput{Symbol: "symbol:a F().", Definitions: []types.KnowledgeDefinitionSite{site}, Answer: answer}

	var stdout, notices bytes.Buffer
	env := testEnv(&stdout, &notices)
	require.NoError(t, EmitDefinitions(context.Background(), env, output.Options{Format: output.Text}, out))
	assert.Contains(t, notices.String()+stdout.String(), staleIndexNotice(answer))

	out.Definitions[0].Status = types.DefinitionUnverified
	err := EmitDefinitions(context.Background(), env, output.Options{Format: output.Text}, out)
	assert.Equal(t, exit.Reported(exit.Failure), err)
}

func TestWriteRefsText(t *testing.T) {
	out := types.KnowledgeRefsOutput{
		Symbol: "symbol:a F().", Label: "F",
		Defs:      []types.KnowledgeRefSite{{File: "a.go"}},
		Refs:      []types.KnowledgeRefSite{{File: "b.go", Count: 2, Lines: []int{4, 9}}, {File: "c.go", Count: 1}},
		FileCount: 2, RefCount: 3,
		Answer: types.KnowledgeAnswer{Verdict: types.VerdictFound},
	}
	var b bytes.Buffer
	require.NoError(t, WriteRefsText(&b, out))
	assert.Equal(t, fmt.Sprintf(`symbol: symbol:a F().  (F)
defined in:
  a.go
referenced in 2 file(s), 3 occurrence(s):
  b.go  (2)%s
  c.go  (1)
`, linesSuffix([]int{4, 9})), b.String())

	// A resolved symbol nothing references is a verified answer, unless the verdict is not.
	out.Refs, out.FileCount, out.RefCount = nil, 0, 0
	b.Reset()
	require.NoError(t, WriteRefsText(&b, out))
	assert.Equal(t, "symbol: symbol:a F().  (F)\ndefined in:\n  a.go\nno references found\n", b.String())

	out.Answer.Verdict = types.VerdictUnknown
	b.Reset()
	err := WriteRefsText(&b, out)
	assert.True(t, exit.AlreadyReported(err))
	assert.Contains(t, b.String(), "verdict: unknown")
}
