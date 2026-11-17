package knowledge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/textindex"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/diff"
)

// Attrs the docsection index records on a section node.
const (
	docSectionStartLine = "start_line"
	docSectionEndLine   = "end_line"
	docSectionDigest    = "digest"
)

// DocSectionSource reads a doc section's lines from its indexed extent.
func DocSectionSource(root string, node types.KnowledgeNode, w RefsWindow) (types.KnowledgeDefinitionSite, error) {
	if node.Kind != types.KindDocSection {
		return types.KnowledgeDefinitionSite{}, exit.Usagef("magus explain: --source reads a doc section, and %s is a %s; a symbol's body is magus refs --source", node.ID, node.Kind)
	}
	file, _, _ := strings.Cut(node.Source, "#")
	start, _ := strconv.Atoi(node.Attrs[docSectionStartLine])
	end, _ := strconv.Atoi(node.Attrs[docSectionEndLine])
	site := types.KnowledgeDefinitionSite{File: file, StartLine: start, EndLine: end, Status: types.DefinitionUnreadable}
	if start == 0 {
		site.SourceNote = "no lines: the index recorded no extent for this section"
		return site, nil
	}
	lines, readable := readSourceLines(root, file)
	if !readable {
		site.SourceNote = "no lines: cannot read " + file
		return site, nil
	}
	if end > len(lines) {
		site.SourceNote = fmt.Sprintf("no lines: the index recorded %d-%d and %s is shorter", start, end, file)
		return site, nil
	}
	sum := sha256.Sum256(bytes.Join(lines[start-1:end], []byte("\n")))
	site.Status = types.DefinitionChanged
	if hex.EncodeToString(sum[:]) == node.Attrs[docSectionDigest] {
		site.Status = types.DefinitionVerified
	}
	block := w.Text(lines, start-1, end)
	site.Source, site.SourceStartLine, site.SourceTotalLines, site.SourceNote = block.Text, block.StartLine, block.Total, w.note(block)
	return site, nil
}

func readSourceLines(root, file string) ([][]byte, bool) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
	if err != nil {
		return nil, false
	}
	return types.SplitSourceLines(data), true
}

// EmitDocSectionSource prints site alone, and exits 1 when its lines are not
// the section's: a reader citing them would cite the wrong text.
func EmitDocSectionSource(w io.Writer, site types.KnowledgeDefinitionSite) error {
	fmt.Fprintf(w, "%s  %s\n", definitionRange(site), site.Status)
	writeSourceBlock(w, "", site.Source, site.SourceStartLine, site.StartLine, site.SourceNote)
	return DocSectionExit(site)
}

// DocSectionExit is the status a section's lines earn: 0 only when they are its own.
func DocSectionExit(site types.KnowledgeDefinitionSite) error {
	if site.Status == types.DefinitionVerified {
		return nil
	}
	return exit.Reported(exit.Failure)
}

// writeSourceBlock prints source numbered, marking the line focus, then note.
func writeSourceBlock(w io.Writer, indent, source string, start, focus int, note string) {
	if start > 0 {
		lines := strings.Split(source, "\n")
		width := len(strconv.Itoa(start + len(lines) - 1))
		for i, line := range lines {
			marker := " "
			if start+i == focus {
				marker = ">"
			}
			fmt.Fprintf(w, "%s%s%*d  %s\n", indent, marker, width, start+i, line)
		}
	}
	if note != "" {
		fmt.Fprintf(w, "%s(%s)\n", indent, note)
	}
}

// markOccurrenceFiles sets what a site's file is: a test by its path, generated
// by its declaration. It returns a note when it could not tell generated apart.
func markOccurrenceFiles(ctx context.Context, files []types.SymbolOccurrenceFile, classify textindex.ClassifyFunc) string {
	paths := make([]string, len(files))
	for i := range files {
		paths[i] = files[i].File
		files[i].Test = types.IsTestPath(files[i].File)
	}
	if len(paths) == 0 {
		return ""
	}
	if classify == nil {
		return "generated files are not marked: the workspace did not load"
	}
	entries, err := classify(ctx, paths)
	if err != nil {
		return "generated files are not marked: " + err.Error()
	}
	generated := map[string]bool{}
	for _, e := range entries {
		generated[e.Path] = e.Role == diff.RoleOutput
	}
	for i := range files {
		files[i].Generated = generated[files[i].File]
	}
	return ""
}

// definitionRange renders a site as path:start-end, path:start when the end is unknown, or
// the bare path when no line was recorded.
func definitionRange(s types.KnowledgeDefinitionSite) string {
	switch {
	case s.StartLine == 0:
		return s.File
	case s.EndLine == 0:
		return fmt.Sprintf("%s:%d", s.File, s.StartLine)
	}
	return fmt.Sprintf("%s:%d-%d", s.File, s.StartLine, s.EndLine)
}

// linesSuffix renders a capped line list as " lines 5,8,12", or "" when absent.
func linesSuffix(lines []int) string {
	if len(lines) == 0 {
		return ""
	}
	parts := make([]string, len(lines))
	for i, ln := range lines {
		parts[i] = strconv.Itoa(ln)
	}
	return "  lines " + strings.Join(parts, ",")
}
