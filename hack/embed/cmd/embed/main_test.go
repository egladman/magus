package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/hack/embed/teacher"
)

func TestChunks(t *testing.T) {
	cases := []struct {
		name string
		text string
		max  int
		want []chunk
	}{
		{"whole lines pack", "ab\ncd\nef\n", 6, []chunk{
			{first: 1, last: 2, text: "ab\ncd\n"}, {first: 3, last: 3, text: "ef\n"},
		}},
		{"no final newline", "ab\ncd", 64, []chunk{{first: 1, last: 2, text: "ab\ncd"}}},
		{"blank pieces drop", "ab\n\n\n\ncd\n", 3, []chunk{
			{first: 1, last: 1, text: "ab\n"}, {first: 5, last: 5, text: "cd\n"},
		}},
		{"long line splits on runes", "a\néééé\nb\n", 5, []chunk{
			{first: 1, last: 1, text: "a\n"},
			{first: 2, last: 2, part: 1, text: "éé"}, {first: 2, last: 2, part: 2, text: "éé\n"},
			{first: 3, last: 3, text: "b\n"},
		}},
		{"empty", "", 8, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := chunks(tc.text, tc.max)
			require.Equal(t, tc.want, got)
			for _, c := range got {
				require.LessOrEqual(t, len(c.text), tc.max)
			}
		})
	}
}

func TestChunkSpan(t *testing.T) {
	require.Equal(t, "3-9", chunk{first: 3, last: 9}.span())
	require.Equal(t, "4-4#2", chunk{first: 4, last: 4, part: 2}.span())
}

func TestReadBatch(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("aaa blob 3\nabc\nbbb blob 0\n\n"))
	got, err := readBatch(r, "HEAD", []string{"x.go", "y.go"})
	require.NoError(t, err)
	require.Equal(t, map[string][]byte{"x.go": []byte("abc"), "y.go": {}}, got)

	_, err = readBatch(bufio.NewReader(strings.NewReader("HEAD:z.go missing\n")), "HEAD", []string{"z.go"})
	require.ErrorContains(t, err, "HEAD:z.go missing")
}

func TestRecord(t *testing.T) {
	cache := t.TempDir()
	texts := filepath.Join(cache, "training", "texts.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(texts), 0o755))
	require.NoError(t, os.WriteFile(texts, []byte(`{"id":"a","text":"x"}`+"\n"+`{"id":"b","text":"y"}`+"\n"+`{"id":"c","text":"z"}`+"\n"), 0o644))
	digest, err := teacher.FileSHA256(texts)
	require.NoError(t, err)

	spec := teacher.RunSpec{Model: "M", Revision: "r1", MaxLen: 256, Dim: 4, ShardSize: 2, TextsSHA256: digest}
	dir := filepath.Join(cache, "teacher", "M", spec.Key())
	require.NoError(t, os.MkdirAll(dir, 0o755))
	manifest := filepath.Join(dir, "manifest.jsonl")
	writeRecords(t, manifest,
		teacher.Record{Kind: "run", Spec: &spec},
		teacher.Record{Kind: "shard", Shard: 0, Rows: 2, Tokens: 10, Seconds: 1.5},
	)
	out := filepath.Join(t.TempDir(), "runs.jsonl")
	args := []string{"-cache", cache, "-model", "M", "-texts", texts, "-max-len", "256", "-out", out}

	require.NoError(t, runRecord(args))
	require.NoError(t, runRecord(args), "an unchanged manifest is not recorded twice")
	lines := readRunLines(t, out)
	require.Len(t, lines, 1)
	require.Equal(t, runLine{
		Kind: "teacher", Run: spec.Key(), Teacher: "M", Revision: "r1", TextsSHA256: digest,
		TextsFile: "training/texts.jsonl", MaxLen: 256, ShardSize: 2, Shards: 1, Texts: 3, Rows: 2,
		Tokens: 10, Seconds: 1.5, Manifest: "teacher/M/" + spec.Key() + "/manifest.jsonl",
		ManifestSHA256: lines[0].ManifestSHA256,
	}, lines[0])

	writeRecords(t, manifest,
		teacher.Record{Kind: "run", Spec: &spec},
		teacher.Record{Kind: "shard", Shard: 0, Rows: 2, Tokens: 10, Seconds: 1.5},
		teacher.Record{Kind: "shard", Shard: 1, Rows: 1, Tokens: 4, Seconds: 0.5},
		teacher.Record{Kind: "shard", Shard: 1, Rows: 1, Tokens: 5, Seconds: 0.25},
	)
	require.NoError(t, runRecord(args))
	lines = readRunLines(t, out)
	require.Len(t, lines, 2)
	last := lines[1]
	require.True(t, last.Complete)
	require.Equal(t, []int{2, 3, 15}, []int{last.Shards, last.Rows, last.Tokens}, "a re-encoded shard counts once, by its later record")
	require.InDelta(t, 1.8, last.Seconds, 1e-9)

	err = runRecord([]string{"-cache", cache, "-model", "M", "-texts", texts, "-max-len", "512", "-out", out})
	require.ErrorContains(t, err, "0 M runs")
}

func writeRecords(t *testing.T, path string, records ...teacher.Record) {
	t.Helper()
	var b strings.Builder
	for _, r := range records {
		line, err := json.Marshal(r)
		require.NoError(t, err)
		b.Write(append(line, '\n'))
	}
	require.NoError(t, os.WriteFile(path, []byte(b.String()), 0o644))
}

func readRunLines(t *testing.T, path string) []runLine {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var out []runLine
	for line := range strings.Lines(string(b)) {
		var r runLine
		require.NoError(t, json.Unmarshal([]byte(line), &r))
		out = append(out, r)
	}
	return out
}
