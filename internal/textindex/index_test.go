package textindex

import (
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memCorpus is a corpus held in memory, so every test below is a pure function of its
// fixture and needs no filesystem.
type memCorpus map[string][]byte

func (c memCorpus) read(path string) ([]byte, error) {
	body, ok := c[path]
	if !ok {
		return nil, fmt.Errorf("no such file: %s", path)
	}
	return body, nil
}

func (c memCorpus) paths() []string {
	out := make([]string, 0, len(c))
	for p := range c {
		out = append(out, p)
	}
	// Sorted so a failure names the same file every run.
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func buildFrom(t *testing.T, c memCorpus) *Index {
	t.Helper()
	ix, err := Build(c.paths(), c.read)
	require.NoError(t, err)
	return ix
}

// scanAll is the reference implementation: no index, every file, same match shape. The
// index is only ever allowed to be FASTER than this, never different.
func scanAll(c memCorpus, pattern string) []Match {
	var out []Match
	for _, p := range c.paths() {
		out = append(out, matchesIn(p, c[p], []byte(pattern))...)
	}
	return out
}

// TestSearchAgreesWithABruteForceScan is the correctness gate. A trigram index is an
// optimization, and an optimization that returns a different answer is a bug however
// fast it is, so the index is graded against the scan it replaces rather than against a
// hand-written expectation.
func TestSearchAgreesWithABruteForceScan(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := "abcdefg \n{}();"
	corpus := memCorpus{}
	for f := range 40 {
		var sb strings.Builder
		for range 400 + rng.Intn(400) {
			sb.WriteByte(alphabet[rng.Intn(len(alphabet))])
		}
		corpus[fmt.Sprintf("file%02d.txt", f)] = []byte(sb.String())
	}
	// A planted needle proves the index finds what a random corpus would rarely contain.
	corpus["file07.txt"] = append(corpus["file07.txt"], []byte("\nplanted_needle_value\n")...)

	ix := buildFrom(t, corpus)

	patterns := []string{"planted_needle_value", "abc", "a", "ab", "zzz", "();", "\n{", "gfedcba"}
	for f := range 25 {
		body := corpus[fmt.Sprintf("file%02d.txt", f)]
		start := rng.Intn(len(body) - 12)
		patterns = append(patterns, string(body[start:start+3+rng.Intn(9)]))
	}

	for _, p := range patterns {
		got, err := ix.SearchLiteral(p)
		require.NoError(t, err, "pattern %q", p)
		assert.Equal(t, scanAll(corpus, p), got,
			"the index must return exactly what a full scan returns, for pattern %q", p)
	}
}

// TestSearchReportsLineAndColumn pins the coordinates a reader acts on. An off-by-one
// here sends someone to the wrong line, which is worse than no answer.
func TestSearchReportsLineAndColumn(t *testing.T) {
	corpus := memCorpus{"a.go": []byte("package a\n\nfunc Target() {}\nvar x = Target\n")}
	ix := buildFrom(t, corpus)

	got, err := ix.SearchLiteral("Target")
	require.NoError(t, err)
	require.Len(t, got, 2)

	assert.Equal(t, Match{Path: "a.go", Line: 3, Col: 6, Text: "func Target() {}"}, got[0])
	assert.Equal(t, Match{Path: "a.go", Line: 4, Col: 9, Text: "var x = Target"}, got[1])
}

// TestShortPatternStillSearches covers the shape with no trigram to look up. Returning
// nothing there would be a silent wrong answer: the pattern is unindexable, not absent.
func TestShortPatternStillSearches(t *testing.T) {
	corpus := memCorpus{"a.txt": []byte("xy\nzy\n")}
	ix := buildFrom(t, corpus)

	got, err := ix.SearchLiteral("y")
	require.NoError(t, err)
	assert.Len(t, got, 2, "a pattern under the trigram floor falls back to scanning, never to silence")
}

// TestAbsentTrigramEndsTheQuery is the property the index exists for: one trigram no
// file holds settles the whole question without reading a single file.
func TestAbsentTrigramEndsTheQuery(t *testing.T) {
	corpus := memCorpus{"a.txt": []byte("hello world\n")}
	reads := 0
	counting := func(p string) ([]byte, error) {
		reads++
		return corpus.read(p)
	}
	ix, err := Build(corpus.paths(), counting)
	require.NoError(t, err)

	before := reads
	got, err := ix.SearchLiteral("qqq")
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, before, reads, "an absent trigram must answer from the index alone, reading no file")
}

// TestEmptyPatternIsRefused keeps the surface honest: everything is not an answer.
func TestEmptyPatternIsRefused(t *testing.T) {
	ix := buildFrom(t, memCorpus{"a.txt": []byte("x")})
	_, err := ix.SearchLiteral("")
	require.Error(t, err)
}

// TestBuildSkipsAnUnreadableFile pins the live-tree case: a file can vanish between the
// walk and the read, and one such file must not cost the whole index.
func TestBuildSkipsAnUnreadableFile(t *testing.T) {
	corpus := memCorpus{"a.txt": []byte("findme here\n")}
	ix, err := Build([]string{"a.txt", "gone.txt"}, corpus.read)
	require.NoError(t, err)
	assert.Equal(t, 1, ix.Len())

	got, err := ix.SearchLiteral("findme")
	require.NoError(t, err)
	assert.Len(t, got, 1)
}

// benchCorpus is sized to make the candidate-narrowing visible: many files, one holding
// the needle. That is the shape of a real precedent hunt.
func benchCorpus(files, size int) memCorpus {
	rng := rand.New(rand.NewSource(7))
	alphabet := "abcdefghijklmnopqrstuvwxyz \n"
	c := memCorpus{}
	for f := range files {
		b := make([]byte, size)
		for i := range b {
			b[i] = alphabet[rng.Intn(len(alphabet))]
		}
		c[fmt.Sprintf("f%04d.txt", f)] = b
	}
	c["f0500.txt"] = append(c["f0500.txt"], []byte("\nHandleRequestPayload\n")...)
	return c
}

// The benchmark names carry the package, because -bench applies to every package in the
// run: a bare BenchmarkBuild here also selects the graph packages' own, and the tree-wide
// run that produces is what a scoped-looking invocation actually launches.
func BenchmarkTextIndexSearchIndexed(b *testing.B) {
	c := benchCorpus(1000, 4096)
	ix, err := Build(c.paths(), c.read)
	require.NoError(b, err)
	b.ResetTimer()
	for b.Loop() {
		if _, err := ix.SearchLiteral("HandleRequestPayload"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTextIndexSearchBruteForce(b *testing.B) {
	c := benchCorpus(1000, 4096)
	b.ResetTimer()
	for b.Loop() {
		scanAll(c, "HandleRequestPayload")
	}
}

// writeCorpus lays a corpus on disk with files ABOVE mmapFloor, which is the only size
// where the mapping path is exercised at all.
func writeCorpus(tb testing.TB, files, size int) []string {
	tb.Helper()
	dir := tb.TempDir()
	rng := rand.New(rand.NewSource(11))
	alphabet := "abcdefghijklmnopqrstuvwxyz \n"
	paths := make([]string, 0, files)
	for f := range files {
		body := make([]byte, size)
		for i := range body {
			body[i] = alphabet[rng.Intn(len(alphabet))]
		}
		p := fmt.Sprintf("%s/f%04d.txt", dir, f)
		require.NoError(tb, os.WriteFile(p, body, 0o644))
		paths = append(paths, p)
	}
	return paths
}

// readFileOnly is the copying baseline the mapping path is measured against.
func readFileOnly(path string) ([]byte, error) { return os.ReadFile(path) }

// TestReaderAgreesWithACopyingRead pins the mapping against the read it replaces. A
// reader that returns different bytes is not an optimization.
func TestReaderAgreesWithACopyingRead(t *testing.T) {
	paths := writeCorpus(t, 3, mmapFloor*2)

	r := NewReader()
	defer func() { assert.NoError(t, r.Close()) }()
	for _, p := range paths {
		mapped, err := r.Read(p)
		require.NoError(t, err)
		copied, err := readFileOnly(p)
		require.NoError(t, err)
		assert.Equal(t, copied, mapped, "%s", p)
	}
}

// TestReaderHandlesAnEmptyFile covers the size-zero case, which cannot be mapped at all.
func TestReaderHandlesAnEmptyFile(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/empty.txt"
	require.NoError(t, os.WriteFile(p, nil, 0o644))

	r := NewReader()
	defer func() { assert.NoError(t, r.Close()) }()
	body, err := r.Read(p)
	require.NoError(t, err, "an empty file is searchable, and finds nothing")
	assert.Empty(t, body)
}

func BenchmarkTextIndexBuildFromDiskMapped(b *testing.B) {
	paths := writeCorpus(b, 200, 64<<10)
	b.ResetTimer()
	for b.Loop() {
		r := NewReader()
		if _, err := Build(paths, r.Read); err != nil {
			b.Fatal(err)
		}
		if err := r.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTextIndexBuildFromDiskCopied(b *testing.B) {
	paths := writeCorpus(b, 200, 64<<10)
	b.ResetTimer()
	for b.Loop() {
		if _, err := Build(paths, readFileOnly); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTextIndexBuild(b *testing.B) {
	c := benchCorpus(1000, 4096)
	paths := c.paths()
	b.ResetTimer()
	for b.Loop() {
		if _, err := Build(paths, c.read); err != nil {
			b.Fatal(err)
		}
	}
}
