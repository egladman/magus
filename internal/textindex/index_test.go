package textindex

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memTree is a tree held in memory, so every test below is a pure function of its
// fixture and needs no filesystem.
type memTree map[string][]byte

func (c memTree) read(path string) ([]byte, error) {
	body, ok := c[path]
	if !ok {
		return nil, fmt.Errorf("no such file: %s", path)
	}
	return body, nil
}

// paths is sorted so a failure names the same file every run.
func (c memTree) paths() []string {
	out := make([]string, 0, len(c))
	for p := range c {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// TestScanReportsLineAndColumn pins the coordinates a reader acts on. An off-by-one
// here sends someone to the wrong line, which is worse than no answer.
func TestScanReportsLineAndColumn(t *testing.T) {
	tree := memTree{"a.go": []byte("package a\n\nfunc Target() {}\nvar x = Target\n")}

	got, err := Scan(tree.paths(), tree.read, "Target", false)
	require.NoError(t, err)

	assert.Equal(t, []Match{
		{Path: "a.go", Line: 3, Col: 6, Text: "func Target() {}"},
		{Path: "a.go", Line: 4, Col: 9, Text: "var x = Target"},
	}, got)
}

func TestScanFindsAOneBytePattern(t *testing.T) {
	tree := memTree{"a.txt": []byte("xy\nzy\n")}

	got, err := Scan(tree.paths(), tree.read, "y", false)
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestScanOrdersByPathThenLine(t *testing.T) {
	tree := memTree{"b.txt": []byte("hit\n"), "a.txt": []byte("x\nhit\n")}

	got, err := Scan(tree.paths(), tree.read, "hit", false)
	require.NoError(t, err)
	assert.Equal(t, []Match{
		{Path: "a.txt", Line: 2, Col: 1, Text: "hit"},
		{Path: "b.txt", Line: 1, Col: 1, Text: "hit"},
	}, got)
}

// TestScanFoldReportsTheOriginalLine: folding is for comparison only, so the reported
// line keeps its case.
func TestScanFoldReportsTheOriginalLine(t *testing.T) {
	tree := memTree{"a.go": []byte("one\nfunc LeafScore() {}\n")}

	got, err := Scan(tree.paths(), tree.read, "leafscore", true)
	require.NoError(t, err)
	assert.Equal(t, []Match{{Path: "a.go", Line: 2, Col: 6, Text: "func LeafScore() {}"}}, got)

	got, err = Scan(tree.paths(), tree.read, "leafscore", false)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestScanRefusesAnEmptyPattern(t *testing.T) {
	tree := memTree{"a.txt": []byte("x")}
	_, err := Scan(tree.paths(), tree.read, "", false)
	require.Error(t, err)
}

// TestScanSkipsAnUnreadableFile pins the live-tree case: a file can vanish between the
// walk and the read, and one such file must not cost the whole search.
func TestScanSkipsAnUnreadableFile(t *testing.T) {
	tree := memTree{"a.txt": []byte("findme here\n")}

	got, err := Scan([]string{"a.txt", "gone.txt"}, tree.read, "findme", false)
	require.NoError(t, err)
	assert.Len(t, got, 1)
}

func benchTree(files, size int) memTree {
	rng := rand.New(rand.NewSource(7))
	alphabet := "abcdefghijklmnopqrstuvwxyz \n"
	c := memTree{}
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
// run: a bare BenchmarkScan here also selects any other package's own, and the tree-wide
// run that produces is what a scoped-looking invocation actually launches.
func BenchmarkTextIndexScan(b *testing.B) {
	c := benchTree(1000, 4096)
	paths := c.paths()
	b.ResetTimer()
	for b.Loop() {
		if _, err := Scan(paths, c.read, "HandleRequestPayload", false); err != nil {
			b.Fatal(err)
		}
	}
}

// writeTree lays a tree on disk with files ABOVE mmapFloor, which is the only size
// where the mapping path is exercised at all.
func writeTree(tb testing.TB, files, size int) []string {
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
	paths := writeTree(t, 3, mmapFloor*2)

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

func BenchmarkTextIndexScanFromDiskMapped(b *testing.B) {
	paths := writeTree(b, 200, 64<<10)
	b.ResetTimer()
	for b.Loop() {
		r := NewReader()
		if _, err := Scan(paths, r.Read, "HandleRequestPayload", false); err != nil {
			b.Fatal(err)
		}
		if err := r.Close(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTextIndexScanFromDiskCopied(b *testing.B) {
	paths := writeTree(b, 200, 64<<10)
	b.ResetTimer()
	for b.Loop() {
		if _, err := Scan(paths, readFileOnly, "HandleRequestPayload", false); err != nil {
			b.Fatal(err)
		}
	}
}
