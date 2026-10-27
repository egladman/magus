package main

import (
	"bufio"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/embed/train/teacher"
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

func TestHoldoutGroupsNeverSpanSplits(t *testing.T) {
	file := func(path, group, split string) sourceRecord {
		return sourceRecord{Kind: "file", Path: path, Group: group, Split: split}
	}
	together := []sourceRecord{
		file("std/crypto.go", "dir:std", "holdout"),
		file("std/crypto_test.go", "dir:std", "holdout"),
		file("docs/reference/buzz/crypto.md", "dir:std", "holdout"),
		file("internal/agent/skills/magus-multi-agent/SKILL.md", "dir:internal/agent/skills/magus-multi-agent", "train"),
		file("internal/agent/skills/magus-multi-agent/reference.md", "dir:internal/agent/skills/magus-multi-agent", "train"),
		file("docs/reference/skills/magus-multi-agent.md", "dir:internal/agent/skills/magus-multi-agent", "train"),
		{Kind: "forced", Tokens: []string{"x"}},
	}
	require.NoError(t, checkGroups(together))

	for _, tc := range []struct {
		name  string
		stray sourceRecord
		want  string
	}{
		{"a test file split from its source", file("std/crypto_test.go", "dir:std", "train"), "group dir:std spans both splits: std/crypto_test.go is train"},
		{"a generated page split from its source", file("docs/reference/buzz/crypto.md", "dir:std", "train"), "group dir:std spans both splits"},
		{"a skill copy split from its skill", file("docs/reference/skills/magus-multi-agent.md", "dir:internal/agent/skills/magus-multi-agent", "holdout"), "spans both splits"},
		{"a file with no group", file("README.md", "", "train"), "README.md names no provenance group"},
		{"a file with no split", file("README.md", "dir:.", ""), `README.md has split ""`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorContains(t, checkGroups(append(slices.Clone(together), tc.stray)), tc.want)
		})
	}
}

func TestTextsLeaveOutGeneratedFilesAndChangelogs(t *testing.T) {
	kept := []byte("package textrank\n")
	records := []sourceRecord{
		{Kind: "file", Path: "internal/textrank/tokens.go", Split: "train", SHA256: sha256Hex(kept)},
		{Kind: "file", Path: "docs/reference/buzz/crypto.md", Split: "train", Generated: true},
		{Kind: "file", Path: "changes/unreleased/split-holdout.md", Split: "train"},
		{Kind: "file", Path: "CHANGELOG.md", Split: "train"},
		{Kind: "file", Path: "std/crypto.go", Split: "holdout"},
	}
	files := map[string][]byte{"internal/textrank/tokens.go": kept}
	d, err := newDecontaminator(&evalCorpus{}, 1)
	require.NoError(t, err)
	m := textMix{forced: map[string]bool{}, excluded: map[string]int{}, decontam: d}
	require.NoError(t, m.addRepository(records, files, "HEAD", "sources.jsonl", 512))
	require.Equal(t, []teacher.Text{{ID: "internal/textrank/tokens.go:1-1", Text: "package textrank\n"}}, m.texts)
	require.Equal(t, map[string]int{"generated": 1, "changelog-fragment": 1, "changelog": 1}, m.excluded)
}

func TestForcedReportCountsHoldoutOnlyWords(t *testing.T) {
	forced := map[string]bool{"fs\\readFile": true, "MGS2001": true, "hasCharm": true}
	texts := []teacher.Text{{ID: "a.go:1-2", Text: "data, err := fs.ReadFile(path)"}}
	holdout := [][]byte{[]byte("// MGS2001 names a target the magusfile never declares\n")}
	got, err := countForced(forced, texts, holdout)
	require.NoError(t, err)
	// fs, readfile, read and file are in a training text; mgs2001 is only in a holdout
	// file; hascharm, has and charm are nowhere.
	require.Equal(t, forcedReport{Tokens: 8, InTrainingTexts: 4, HoldoutOnly: 1, Unseen: 3}, got)

	_, err = countForced(map[string]bool{"--": true}, texts, holdout)
	require.ErrorContains(t, err, "yields no token")
}
