package symbols

import (
	"bytes"
	"cmp"
	"context"
	"slices"
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/egladman/magus/types"
)

// ParseOccurrences decodes a SCIP index and returns every occurrence of one symbol key,
// with its full source range and no cap. It does no I/O; the caller supplies the bytes.
//
// This is the deliberate counterpart to ParseIndex, not a variant of it. ParseIndex
// collapses occurrences to one entry per (file, symbol) with a count and a MaxRefLines-
// capped line list, because the graph stores a shard per workspace and a hot symbol would
// otherwise dominate it. Those are exactly the two properties a rewrite cannot use: the
// cap silently drops sites, and a bare line number does not say which occurrence on the
// line it means. So this walks the same documents and keeps what that one throws away,
// for ONE key rather than all of them - which is what makes keeping it affordable.
//
// names are the spellings that may legitimately appear at an occurrence, used by Verify.
// names[0] is the source identifier a rename targets; any others are alternate spellings
// the index itself asserts for the same symbol. A package needs both: `assert.Equal(...)`
// holds the bare identifier while the import statement above it holds the full path
// `github.com/stretchr/testify/assert`, and both are real occurrences of that one symbol.
// Verifying against a single spelling rejected 8025 of 8501 sites on this repo's own index
// and reported a fresh index as stale.
//
// They are returned rather than taken as an argument so the parse and the check cannot
// disagree about what the symbol is called.
//
// projectPath rebases indexer-relative document paths onto the workspace, with the same
// rules and the same outside-the-workspace drop as ParseIndex - see workspacePath.
func ParseOccurrences(ctx context.Context, data []byte, projectPath, key string) (files []types.SymbolOccurrenceFile, names []string, err error) {
	var idx scip.Index
	if err := proto.Unmarshal(data, &idx); err != nil {
		return nil, nil, err
	}
	var ident, display string

	byFile := map[string][]types.SymbolOccurrence{}
	for _, doc := range idx.Documents {
		if err := ctx.Err(); err != nil {
			// An index can carry thousands of documents; a cancelled caller should not have
			// to wait out the whole walk.
			return nil, nil, err
		}
		// SymbolInformation carries the display name, and it can live in ANY document -
		// including one holding no occurrence of the symbol, and including one whose path
		// falls outside the workspace. So it is read before the workspace filter below,
		// matching how ParseIndex builds infoByKey: a package whose SymbolInformation is
		// recorded only in a dependency document would otherwise lose its full-path
		// spelling, and every import-statement site would then read as a mismatch.
		if display == "" {
			for _, si := range doc.Symbols {
				if info, ok := parseMoniker(si.Symbol); ok && info.Key == key && si.DisplayName != "" {
					display = si.DisplayName
					break
				}
			}
		}
		docPath, ok := workspacePath(projectPath, doc.RelativePath)
		if !ok {
			continue
		}
		for _, occ := range doc.Occurrences {
			if occ.Symbol == "" || scip.IsLocalSymbol(occ.Symbol) {
				continue
			}
			info, ok := parseMoniker(occ.Symbol)
			if !ok || info.Key != key {
				continue
			}
			// The descriptor's own name is what is written at a call site, which makes it
			// the identifier a rename targets.
			if ident == "" {
				ident = info.Label
			}
			r, ok := occ.SourceRange()
			if !ok || r.Start.Line < 0 || r.Start.Character < 0 {
				// A range magus cannot read is not reported as an editable site at all.
				// Emitting it with a zero position would put a plausible-looking
				// 1:1 in the output, which is worse than the omission.
				continue
			}
			byFile[docPath] = append(byFile[docPath], types.SymbolOccurrence{
				// SCIP positions are 0-based; magus prints and accepts file:line:col
				// 1-based everywhere else, and an off-by-one here lands the edit one
				// character early on every site.
				Line:       int(r.Start.Line) + 1,
				Column:     int(r.Start.Character) + 1,
				EndLine:    int(r.End.Line) + 1,
				EndColumn:  int(r.End.Character) + 1,
				Definition: occ.SymbolRoles&int32(scip.SymbolRole_Definition) != 0,
			})
		}
	}

	files = make([]types.SymbolOccurrenceFile, 0, len(byFile))
	for p, occs := range byFile {
		slices.SortFunc(occs, func(a, b types.SymbolOccurrence) int {
			if c := cmp.Compare(a.Line, b.Line); c != 0 {
				return c
			}
			return cmp.Compare(a.Column, b.Column)
		})
		// Collapse sites that share a start position. SCIP permits an index to carry two
		// Document entries for one path, and an occurrence can legitimately be recorded
		// twice (upstream ships FlattenDocuments/FlattenOccurrences for exactly this), which
		// is harmless in a count and destructive in an edit plan: a caller rewriting
		// back-to-front applies the second replacement on top of bytes it already rewrote.
		// Measured over this repo's five indexes (1399 documents) there are none today, so
		// this is guarding the contract rather than fixing an observed break.
		occs = slices.CompactFunc(occs, func(a, b types.SymbolOccurrence) bool {
			return a.Line == b.Line && a.Column == b.Column
		})
		files = append(files, types.SymbolOccurrenceFile{File: p, Occurrences: occs})
	}
	// byFile iteration is unordered; the sort is what makes the output deterministic.
	slices.SortFunc(files, func(a, b types.SymbolOccurrenceFile) int { return cmp.Compare(a.File, b.File) })

	return files, spellings(ident, display), nil
}

// spellings collects the forms a symbol may be written in at an occurrence, shortest-and-
// most-identifier-like first, deduplicated.
//
// The last-segment rule is what makes packages work. A Go package's descriptor name is its
// full import path, so both the descriptor and the display name give
// "github.com/stretchr/testify/assert" - yet the only place that string appears in source
// is the import statement, while every call site writes the bare "assert". Without the
// segment, verification rejects every call site of every package.
//
// It widens the accepted set rather than replacing it: the full path is still accepted, so
// the import statement verifies too. The cost of the widening is that a genuinely stale
// range coincidentally holding the bare segment would verify, which is a far smaller error
// than refusing every real site.
func spellings(ident, display string) []string {
	var out []string
	add := func(s string) {
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	// out[0] is contractually the identifier a rename targets, and it is derived from IDENT
	// alone - never from display. For a package, ident is itself the full path, so the
	// identifier is its last segment; for everything else ident is already the identifier.
	// Letting display's segment reach that slot would hand an indexer's display convention
	// the position that belongs to the symbol's own name, which is what a caller edits.
	if i := strings.LastIndexByte(ident, '/'); i >= 0 {
		add(ident[i+1:])
	}
	add(ident)
	if i := strings.LastIndexByte(display, '/'); i >= 0 {
		add(display[i+1:])
	}
	add(display)
	return out
}

// VerifyOccurrences stamps a status on every occurrence by reading the range out of the
// file as it is NOW and checking it against names - the spellings ParseOccurrences found
// for this symbol. read returns one file's current bytes.
//
// It MUTATES files in place and returns only an error. An earlier version also returned
// the slice, which made a caller's `files = Verify(files, ...)` read like a transform over
// an untouched input when it was self-assignment over a shared backing array.
//
// Matching a SET rather than one string is what keeps the check honest for symbols that
// are written more than one way, a package being the clear case: its import statement
// holds the full path and its call sites hold the bare identifier. The safety property is
// unchanged, because it never rested on there being exactly one spelling - it rests on the
// range still holding a spelling the index itself asserts for this symbol. Each occurrence
// reports the Text it actually holds, so a caller replacing one is never guessing which.
//
// This is the property that makes the output safe to act on mechanically. A SCIP range
// describes the bytes that were indexed, and nothing in the index says whether those are
// still the bytes on disk - so an index built before an edit points confidently at text
// that has moved. Checking each range against the file turns that from a silent
// corruption into a reported mismatch. It also covers a subtler case for free: SCIP
// positions may be UTF-8 or UTF-16 offsets, magus reads them as bytes, and a document
// using UTF-16 with non-ASCII text ahead of the symbol lands off by the difference. That
// too shows up as a mismatch rather than as a wrong edit.
//
// Files are read once each, not once per occurrence, because a hot symbol has many
// occurrences in one file and re-reading per site would make verification quadratic in
// the thing it exists to keep affordable.
func VerifyOccurrences(ctx context.Context, files []types.SymbolOccurrenceFile, names []string, read func(path string) ([]byte, error)) error {
	for i := range files {
		if err := ctx.Err(); err != nil {
			// A hot symbol reaches hundreds of files; a cancelled caller stops here rather
			// than reading out the rest.
			return err
		}
		f := &files[i]
		data, err := read(f.File)
		if err != nil {
			for j := range f.Occurrences {
				f.Occurrences[j].Status = types.SymbolOccurrenceUnreadable
			}
			f.Stale = true
			continue
		}
		lines := bytes.Split(data, []byte("\n"))
		for j := range f.Occurrences {
			occ := &f.Occurrences[j]
			text, ok := sliceRange(lines, *occ)
			if !ok {
				occ.Status = types.SymbolOccurrenceUnreadable
				f.Stale = true
				continue
			}
			occ.Text = text
			// An empty names set verifies nothing. "No expectation" must not collapse into
			// "everything matches", which would verify every site by vacuum and defeat the
			// entire point of the check.
			if slices.Contains(names, text) {
				occ.Status = types.SymbolOccurrenceVerified
			} else {
				occ.Status = types.SymbolOccurrenceMismatch
				// One bad range proves the file moved under the index, which makes the
				// whole file's occurrence list suspect - including sites it may now be
				// missing. See types.SymbolOccurrenceFile.Stale.
				f.Stale = true
			}
		}
	}
	return nil
}

// sliceRange extracts the text an occurrence's range covers from a file's lines. Returns
// ok=false when the range falls outside the file, which is a real case rather than a
// defensive one: that is exactly what a range recorded against a since-shortened file
// looks like.
//
// Positions are 1-based and the end is exclusive, matching types.SymbolOccurrence. A
// multi-line range is joined with "\n" so the returned text is what the file holds; a
// symbol name never spans lines, so such a range fails the name comparison and is
// reported as a mismatch, which is the correct outcome for a range that cannot be a
// plain identifier.
func sliceRange(lines [][]byte, occ types.SymbolOccurrence) (string, bool) {
	// Convert to 0-based half-open bounds once. Every check below is then an ordinary
	// slice bound rather than a 1-based expression re-derived at each use, which is what
	// the earlier form got wrong often enough for a static analyzer to notice.
	startLine, endLine := occ.Line-1, occ.EndLine-1
	startCol, endCol := occ.Column-1, occ.EndColumn-1
	if startLine < 0 || endLine < startLine || endLine >= len(lines) {
		return "", false
	}
	if startCol < 0 || endCol < 0 {
		return "", false
	}
	// A line's trailing "\r" (a CRLF file) is part of the byte content the indexer saw,
	// so columns already account for it and no trimming is wanted here.
	first, last := lines[startLine], lines[endLine]
	if startCol > len(first) || endCol > len(last) {
		return "", false
	}
	if startLine == endLine {
		if endCol < startCol {
			return "", false
		}
		return string(first[startCol:endCol]), true
	}
	parts := make([][]byte, 0, endLine-startLine+1)
	parts = append(parts, first[startCol:])
	parts = append(parts, lines[startLine+1:endLine]...)
	parts = append(parts, last[:endCol])
	return string(bytes.Join(parts, []byte("\n"))), true
}
