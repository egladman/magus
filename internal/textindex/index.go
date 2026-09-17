// Package textindex answers the text half of a lookup: is this string present in the
// tree, and where. It is the missing counterpart to the symbol index, which can only
// report that a name is not a SYMBOL and has no basis for the stronger claim that it is
// not in the tree.
//
// The design is the trigram index Google Code Search and Zoekt use: every distinct
// three-byte window of every file becomes a posting list of the files holding it, a
// query intersects the posting lists of its own trigrams, and the real matcher runs only
// over what survives. The index does not answer the query; it removes the files that
// cannot possibly match, which is where the time goes.
//
// Not a competitor to ripgrep at scanning. rg wins a cold byte-for-byte scan and always
// will. What this can do is scan fewer files (declared sources, never generated
// output), skip files whose content hash has not moved, and answer from a warm process
// rather than a directory walk.
package textindex

import (
	"bytes"
	"fmt"
	"sort"
)

// MinGram is the shortest pattern a trigram index can narrow. Anything shorter has no
// three-byte window to look up, so it degrades to scanning every file rather than
// reporting nothing.
const MinGram = 3

// ReadFunc supplies a file's bytes. Injected rather than assumed so a caller can serve
// from a cache, an archive, or memory, and so tests need no filesystem.
type ReadFunc func(path string) ([]byte, error)

// Index maps each trigram to the files containing it.
//
// Posting lists hold file IDs rather than paths: an ID is 4 bytes against a path's
// dozens, and the intersection below is the hot loop.
type Index struct {
	paths []string
	post  map[uint32][]uint32
	read  ReadFunc
}

// Match is one occurrence: the file, the 1-based line and column, and that whole line.
type Match struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Col  int    `json:"col"`
	Text string `json:"text"`
}

// trigram packs three bytes into the key the posting map is keyed on.
func trigram(a, b, c byte) uint32 {
	return uint32(a)<<16 | uint32(b)<<8 | uint32(c)
}

// Build indexes paths, reading each through read.
//
// A file that cannot be read is skipped rather than failing the build: the file set is
// routinely a live working tree, where a file can vanish between the walk and the read,
// and one such file must not cost the whole index.
func Build(paths []string, read ReadFunc) (*Index, error) {
	if read == nil {
		return nil, fmt.Errorf("textindex: a ReadFunc is required")
	}
	ix := &Index{paths: make([]string, 0, len(paths)), post: make(map[uint32][]uint32), read: read}
	// optimization: direct-mapped seen[] indexed by trigram, replacing a map dedupe.
	//   measured: BenchmarkTextIndexBuild 119.6ms -> 72.9ms over 4MB of files, -39% ns/op
	//             (n=50). The map cost one hash per BYTE read.
	//   trade-off: B/op 47MB -> 114MB, a flat 64 MiB scratch per Build whatever the input
	//             size, and the "is this a set" reading is gone. Sorting a per-file slice
	//             was tried instead and measured 69% SLOWER (120ms -> 202ms).
	//   assumes:  nothing platform-specific; 1<<24 covers every 3-byte key exactly.
	//
	// seen holds fileID+1 rather than a bool, so one allocation serves every file: a
	// stale entry from an earlier file simply does not equal this file's mark, which is
	// what makes clearing between files unnecessary.
	seen := make([]uint32, 1<<24)
	for _, p := range paths {
		body, err := read(p)
		if err != nil {
			continue
		}
		id := uint32(len(ix.paths))
		ix.paths = append(ix.paths, p)
		mark := id + 1
		for i := 0; i+2 < len(body); i++ {
			g := trigram(body[i], body[i+1], body[i+2])
			if seen[g] == mark {
				continue
			}
			seen[g] = mark
			// TODO: this append is the build's whole allocation cost (175k allocs, 47MB at
			// 4MB of files): ~22k posting lists each doubling their way to ~1k entries. A
			// flat arena sliced per gram would make it ~30 allocations. Measure before
			// landing; the sort attempt above looked equally obvious and lost.
			ix.post[g] = append(ix.post[g], id)
		}
	}
	// Posting lists are appended in file order, so they are already ascending. Sorting
	// would be wasted work; asserting the property in one place is cheaper than trusting
	// it at every intersection.
	for g, list := range ix.post {
		if !sort.SliceIsSorted(list, func(i, j int) bool { return list[i] < list[j] }) {
			return nil, fmt.Errorf("textindex: posting list for %d is unsorted", g)
		}
	}
	return ix, nil
}

// Len reports how many files the index covers.
func (ix *Index) Len() int { return len(ix.paths) }

// Grams reports how many distinct trigrams the index holds, for size budgeting.
func (ix *Index) Grams() int { return len(ix.post) }

// candidates returns the file IDs that could hold pattern, or every ID when the pattern
// is too short to narrow.
//
// Intersecting from the RAREST trigram first is the whole optimization: the result can
// only shrink, so starting from the smallest posting list makes every later pass walk
// the shortest possible list.
func (ix *Index) candidates(pattern []byte) []uint32 {
	if len(pattern) < MinGram {
		all := make([]uint32, len(ix.paths))
		for i := range all {
			all[i] = uint32(i)
		}
		return all
	}
	grams := make([]uint32, 0, len(pattern)-2)
	seen := make(map[uint32]struct{}, len(pattern))
	for i := 0; i+2 < len(pattern); i++ {
		g := trigram(pattern[i], pattern[i+1], pattern[i+2])
		if _, dup := seen[g]; dup {
			continue
		}
		seen[g] = struct{}{}
		grams = append(grams, g)
	}
	lists := make([][]uint32, 0, len(grams))
	for _, g := range grams {
		list, ok := ix.post[g]
		if !ok {
			// One absent trigram is proof no file matches, so the whole query is over.
			return nil
		}
		lists = append(lists, list)
	}
	sort.Slice(lists, func(i, j int) bool { return len(lists[i]) < len(lists[j]) })
	out := lists[0]
	for _, list := range lists[1:] {
		out = intersect(out, list)
		if len(out) == 0 {
			return nil
		}
	}
	return out
}

// intersect returns the sorted values present in both a and b.
//
// A linear merge rather than a map: both sides are sorted and the result is consumed in
// order, so a hash set would cost an allocation and a worse cache profile to answer a
// question two cursors already answer.
func intersect(a, b []uint32) []uint32 {
	out := a[:0:0]
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}

// SearchLiteral returns every occurrence of pattern, in file order then line order.
//
// The index narrows; bytes.Index decides. That split is deliberate: Go's byte search is
// already vectorized on the platforms that matter, so the win available here is running
// it over fewer files rather than trying to beat it.
func (ix *Index) SearchLiteral(pattern string) ([]Match, error) {
	if pattern == "" {
		return nil, fmt.Errorf("textindex: an empty pattern matches everything; say what you are looking for")
	}
	pat := []byte(pattern)
	var out []Match
	for _, id := range ix.candidates(pat) {
		path := ix.paths[id]
		body, err := ix.read(path)
		if err != nil {
			continue
		}
		out = append(out, matchesIn(path, body, pat)...)
	}
	return out, nil
}

// Scan searches paths for pattern with no index at all.
//
// The index is an accelerator, never a precondition. A text search that required one
// could answer "unknown, build an index and ask again", and a search that can say that
// is one a reader stops trusting: raw text needs no index, only bytes. So this path is
// the primary and Index.SearchLiteral is the fast case layered over it.
//
// fold lowercases both sides. It allocates a copy per file, which is why it is a
// parameter rather than the default.
func Scan(paths []string, read ReadFunc, pattern string, fold bool) ([]Match, error) {
	if pattern == "" {
		return nil, fmt.Errorf("textindex: an empty pattern matches everything; say what you are looking for")
	}
	pat := []byte(pattern)
	if fold {
		pat = bytes.ToLower(pat)
	}
	var out []Match
	for _, p := range paths {
		body, err := read(p)
		if err != nil {
			continue
		}
		hay := body
		if fold {
			hay = bytes.ToLower(body)
		}
		for _, m := range matchesIn(p, hay, pat) {
			// The line is reported from the ORIGINAL bytes: a reader acting on this needs
			// the text as it is written, not as it was folded for comparison.
			if fold {
				m.Text = lineAt(body, m.Line)
			}
			out = append(out, m)
		}
	}
	return out, nil
}

// lineAt returns the 1-based line of body, without its newline.
func lineAt(body []byte, line int) string {
	start := 0
	for n := 1; n < line; n++ {
		nl := bytes.IndexByte(body[start:], '\n')
		if nl < 0 {
			return ""
		}
		start += nl + 1
	}
	end := bytes.IndexByte(body[start:], '\n')
	if end < 0 {
		return string(body[start:])
	}
	return string(body[start : start+end])
}

// matchesIn walks one file's occurrences, carrying the line number forward rather than
// recounting newlines from the start for each hit.
func matchesIn(path string, body, pat []byte) []Match {
	var out []Match
	line, lineStart, from := 1, 0, 0
	for {
		rel := bytes.Index(body[from:], pat)
		if rel < 0 {
			return out
		}
		at := from + rel
		for nl := bytes.IndexByte(body[lineStart:at], '\n'); nl >= 0; nl = bytes.IndexByte(body[lineStart:at], '\n') {
			line++
			lineStart += nl + 1
		}
		end := bytes.IndexByte(body[lineStart:], '\n')
		if end < 0 {
			end = len(body)
		} else {
			end += lineStart
		}
		out = append(out, Match{
			Path: path,
			Line: line,
			Col:  at - lineStart + 1,
			Text: string(body[lineStart:end]),
		})
		from = at + len(pat)
		if from > len(body)-len(pat) {
			return out
		}
	}
}
