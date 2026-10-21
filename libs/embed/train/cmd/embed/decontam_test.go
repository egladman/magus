package main

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/embed/train/teacher"
)

const evalSentence = "Return the lowercase hex SHA-256 digest of data read from the named file on disk, or an error when it cannot be read"

func plantedDecontaminator(t *testing.T) *decontaminator {
	t.Helper()
	e := &evalCorpus{}
	e.add("csn-go", evalSentence)
	e.add("commit-subjects", "split the holdout by provenance group", "drop the shard when its manifest is torn")
	boilerplate := "if err != nil { return nil, err } return out, nil } func next() error {"
	e.add("repo-holdout", boilerplate+" one", boilerplate+" two", boilerplate+" three")
	d, err := newDecontaminator(e, 2)
	require.NoError(t, err)
	return d
}

func TestDecontamDropsAPlantedThirteenGram(t *testing.T) {
	d := plantedDecontaminator(t)
	words := strings.Fields(evalSentence)
	planted := "// " + strings.Join(words[3:16], "\n\t ") + "\nfunc Sum(path string) (string, error) {"
	require.False(t, d.admit("cornstack", "row:1", "hash a file", planted), "13 words, whitespace aside")
	require.True(t, d.admit("cornstack", "row:2", strings.Join(words[3:15], " ")), "12 words are not a 13-gram")
	require.False(t, d.admit("repo", "a.go:1-4", "commits: drop the shard when its manifest is torn then retry"), "an 8-word eval text is its own gram")
	require.False(t, d.admit("repo", "b.go:1-1", "split  the holdout\tby provenance group"), "exact match after whitespace")
	require.True(t, d.admit("repo", "c.go:1-9", "we split the holdout by provenance group today"), "under 8 words only an exact match drops")
	require.True(t, d.admit("repo", "d.go:1-9", "if err != nil { return nil, err } return out, nil } func next() error {"), "a gram in more than -common eval texts is boilerplate")

	require.Equal(t, map[string]int{"cornstack": 2, "repo": 4}, d.report.Scanned)
	require.Equal(t, map[string]int{"cornstack": 1, "repo": 2}, d.report.Dropped)
	require.Equal(t, map[string]int{
		"cornstack/ngram/csn-go":     1,
		"repo/ngram/commit-subjects": 1,
		"repo/exact/commit-subjects": 1,
	}, d.report.DroppedBy)
	require.Equal(t, []droppedText{
		{ID: "row:1", Kind: "cornstack", Reason: "ngram", Sets: []string{"csn-go"}},
		{ID: "a.go:1-4", Kind: "repo", Reason: "ngram", Sets: []string{"commit-subjects"}},
		{ID: "b.go:1-1", Kind: "repo", Reason: "exact", Sets: []string{"commit-subjects"}},
	}, d.dropped)
	require.Equal(t, 1, d.report.EvalTexts["csn-go"])
	require.Positive(t, d.report.CommonGrams)
}

func TestDecontamWindowHashesRollLikeFreshOnes(t *testing.T) {
	words := strings.Fields("a b c d e f g h")
	rolled := windowHashes(words, 3)
	require.Len(t, rolled, 6)
	for i, h := range rolled {
		require.Equal(t, windowHashes(words[i:i+3], 3)[0], h, "window %d", i)
	}
	require.Nil(t, windowHashes(words[:2], 3))
}

func TestDecontamRefusesAMissingEvalSet(t *testing.T) {
	dir := t.TempDir()
	writeSet := func(name, queries, corpus string) {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, name), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name, "queries.jsonl"), []byte(queries), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name, "corpus.jsonl"), []byte(corpus), 0o644))
	}
	writeSet("cosqa", `{"id":"q1","text":"sort a map by value"}`+"\n", `{"id":"d1","text":"sorted(m.items())"}`+"\n")
	err := readEvalSets(&evalCorpus{}, dir, map[string]bool{})
	require.ErrorContains(t, err, "eval set csn-go: no csn-go/queries.jsonl")

	e := &evalCorpus{}
	require.NoError(t, readEvalSets(e, dir, map[string]bool{"csn-go": true, "stackoverflow-qa": true}))
	require.Equal(t, []string{"cosqa"}, e.sets)
	require.Equal(t, []string{"sort a map by value", "sorted(m.items())"}, e.texts["cosqa"])
	require.Len(t, e.files["cosqa/queries.jsonl"], 64)
	require.Len(t, e.files["cosqa/corpus.jsonl"], 64)

	writeSet("csn-go", `{"id":"q1","text":"open a file"}`+"\n", `{"text":"no id"}`+"\n")
	require.ErrorContains(t, readEvalSets(&evalCorpus{}, dir, map[string]bool{}), "corpus.jsonl:1: no id")

	_, err = newDecontaminator(&evalCorpus{}, 0)
	require.ErrorContains(t, err, "-common 0 must be at least 1")
}

func TestDecontamReplacesADroppedCornstackRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shard-00000.jsonl.gz")
	f, err := os.Create(path)
	require.NoError(t, err)
	gz := gzip.NewWriter(f)
	_, err = gz.Write([]byte(`{"query":"open a file","document":"func Open() {}"}` + "\n" +
		`{"query":"hash it","document":"// ` + evalSentence + `\nfunc Sum() {}"}` + "\n" +
		`{"query":"close a file","document":"func Close() {}"}` + "\n"))
	require.NoError(t, err)
	require.NoError(t, gz.Close())
	require.NoError(t, f.Close())

	d := plantedDecontaminator(t)
	got, err := readCornstack(path, "cs", 2, 512, d)
	require.NoError(t, err)
	require.Equal(t, []teacher.Text{
		{ID: "cs:0:query", Text: "open a file"}, {ID: "cs:0:document", Text: "func Open() {}"},
		{ID: "cs:2:query", Text: "close a file"}, {ID: "cs:2:document", Text: "func Close() {}"},
	}, got)
	require.Equal(t, []droppedText{{ID: "cs:1", Kind: "cornstack", Reason: "ngram", Sets: []string{"csn-go"}}}, d.dropped)
}
