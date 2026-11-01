package knowledge

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/types"
)

// SourceWindow is what --source prints around a range. From and To count from
// it: 0 is the range however long, -1 the line above it, 1 the line below it.
type SourceWindow struct {
	Show     bool
	From, To int
	// MaxLines caps a block, 0 for no cap. SkipLines is where its page starts.
	MaxLines, SkipLines int
}

// SourceBlock is the lines a window kept, with no sentence about them.
// StartLine is 0 when it kept none. Total is the window's length, set only
// when the page cut it, so StartLine 0 with Total 0 fell outside the file.
type SourceBlock struct {
	Text             string
	StartLine, Total int
}

// Text is the window around lines[first:end], clamped to the file.
// The empty element after a final newline is not a line to print.
func (w SourceWindow) Text(lines [][]byte, first, end int) SourceBlock {
	last := len(lines)
	if last > 0 && len(lines[last-1]) == 0 {
		last--
	}
	start, stop := first, end
	switch {
	case w.From < 0:
		start = first + w.From
	case w.From > 0:
		start = end + w.From - 1
	}
	switch {
	case w.To < 0:
		stop = first + w.To + 1
	case w.To > 0:
		stop = end + w.To
	}
	start, stop = max(start, 0), min(stop, last)
	if start >= stop {
		return SourceBlock{}
	}
	whole := stop - start
	if w.SkipLines >= whole {
		return SourceBlock{Total: whole}
	}
	start += w.SkipLines
	shown := stop - start
	if w.MaxLines > 0 {
		shown = min(shown, w.MaxLines)
	}
	block := SourceBlock{Text: string(bytes.Join(lines[start:start+shown], []byte("\n"))), StartLine: start + 1}
	if shown != whole {
		block.Total = whole
	}
	return block
}

// FillOccurrences sets each occurrence's source from its file under root.
// Only a file it cannot read gets a SourceNote.
func (w SourceWindow) FillOccurrences(root string, files []types.SymbolOccurrenceFile) {
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.File)))
		for j := range file.Occurrences {
			occ := &file.Occurrences[j]
			if err != nil {
				occ.SourceNote = "no lines: cannot read " + file.File
				continue
			}
			block := w.Text(types.SplitSourceLines(data), occ.Line-1, max(occ.Line, occ.EndLine))
			occ.Source, occ.SourceStartLine, occ.SourceTotalLines = block.Text, block.StartLine, block.Total
		}
	}
}

// CheckDefinitions sets each site's Status against its file under root, and
// its source when w.Show is set. The index keeps the positions of an older
// tree, so a digest of today's lines proves nothing. The name still on its
// start line (else changed) and a file predating its index (else unverified)
// do. A nil indexedAt verifies nothing.
func (w SourceWindow) CheckDefinitions(root, name string, sites []types.KnowledgeDefinitionSite, indexedAt func(string) (time.Time, bool)) {
	type sourceFile struct {
		lines   [][]byte
		modTime time.Time
	}
	files := map[string]*sourceFile{}
	for i := range sites {
		site := &sites[i]
		if site.StartLine == 0 {
			continue
		}
		file, seen := files[site.File]
		if !seen {
			path := filepath.Join(root, filepath.FromSlash(site.File))
			if data, err := os.ReadFile(path); err == nil {
				file = &sourceFile{lines: types.SplitSourceLines(data)}
				if info, err := os.Stat(path); err == nil {
					file.modTime = info.ModTime()
				}
			}
			files[site.File] = file
		}
		end := max(site.StartLine, site.EndLine)
		if file == nil || end > len(file.lines) {
			site.Status = types.DefinitionUnreadable
			continue
		}
		site.Status = types.DefinitionUnverified
		switch {
		case name != "" && !containsWord(file.lines[site.StartLine-1], name):
			site.Status = types.DefinitionChanged
		case indexedAt != nil:
			if at, ok := indexedAt(site.File); ok && !file.modTime.After(at) {
				site.Status = types.DefinitionVerified
			}
		}
		if w.Show {
			block := w.Text(file.lines, docStart(file.lines, site.StartLine-1), end)
			site.Source, site.SourceStartLine, site.SourceTotalLines = block.Text, block.StartLine, block.Total
		}
	}
}

// docStart is the first line of the comment block directly above the
// declaration at decl, or decl when none is. That block is where a symbol
// states its contract, so a reader shown the body alone would miss it.
func docStart(lines [][]byte, decl int) int {
	start := decl
	for start > 0 {
		above := bytes.TrimSpace(lines[start-1])
		if !bytes.HasPrefix(above, []byte("//")) && !bytes.HasPrefix(above, []byte("#")) {
			break
		}
		start--
	}
	return start
}

// containsWord reports whether line holds name delimited by non-identifier
// bytes, so a declaration of F is not confirmed by a line declaring FF.
func containsWord(line []byte, name string) bool {
	isIdent := func(b byte) bool {
		return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
	}
	for off := 0; ; {
		i := bytes.Index(line[off:], []byte(name))
		if i < 0 {
			return false
		}
		start, stop := off+i, off+i+len(name)
		if (start == 0 || !isIdent(line[start-1])) && (stop == len(line) || !isIdent(line[stop])) {
			return true
		}
		off = start + 1
	}
}

// OccurrencePage is the sites trailing paths, a limit and an offset keep.
// Scopes are workspace-relative and slash-separated; none holds every file.
type OccurrencePage struct {
	Scopes        []string
	Limit, Offset int
}

// NewOccurrencePage takes scopes as absolute paths under root.
func NewOccurrencePage(root string, scopes []string, limit, offset int) (OccurrencePage, error) {
	rel := make([]string, 0, len(scopes))
	for _, a := range scopes {
		r, err := filepath.Rel(root, a)
		if err != nil {
			return OccurrencePage{}, err
		}
		rel = append(rel, filepath.ToSlash(r))
	}
	return OccurrencePage{Scopes: rel, Limit: limit, Offset: offset}, nil
}

func (p OccurrencePage) holds(file string) bool {
	return len(p.Scopes) == 0 || slices.ContainsFunc(p.Scopes, func(scope string) bool {
		return scope == "." || file == scope || strings.HasPrefix(file, scope+"/")
	})
}

// Scoped keeps the files at or under the scopes.
func (p OccurrencePage) Scoped(files []types.SymbolOccurrenceFile) []types.SymbolOccurrenceFile {
	return slices.DeleteFunc(slices.Clone(files), func(f types.SymbolOccurrenceFile) bool { return !p.holds(f.File) })
}

// ScopedRefs narrows the plain reference list to the scopes and recounts it.
func (p OccurrencePage) ScopedRefs(out types.KnowledgeRefsOutput) types.KnowledgeRefsOutput {
	if len(p.Scopes) == 0 {
		return out
	}
	out.Refs = slices.DeleteFunc(slices.Clone(out.Refs), func(r types.KnowledgeRefSite) bool { return !p.holds(r.File) })
	out.FileCount, out.RefCount = len(out.Refs), 0
	for _, r := range out.Refs {
		out.RefCount += r.Count
	}
	return out
}

// Cut keeps the page's sites, drops a file left with none, and counts them.
func (p OccurrencePage) Cut(files []types.SymbolOccurrenceFile) (kept []types.SymbolOccurrenceFile, shown int) {
	skip := p.Offset
	for _, f := range files {
		sites := f.Occurrences[min(skip, len(f.Occurrences)):]
		skip -= len(f.Occurrences) - len(sites)
		if p.Limit > 0 {
			sites = sites[:min(p.Limit-shown, len(sites))]
		}
		if len(sites) > 0 {
			f.Occurrences = sites
			kept = append(kept, f)
			shown += len(sites)
		}
	}
	return kept, shown
}
