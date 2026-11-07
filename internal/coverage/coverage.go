package coverage

import (
	"bytes"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/spells"
)

// Block is a run of lines a report instruments: Units coverable units (Go
// statements, or one LCOV line) that the run hit Hits times.
type Block struct {
	StartLine int
	EndLine   int
	Units     int
	Hits      int
}

// File is one source file's blocks. Covered counts the units hit at least
// once, Total every unit.
type File struct {
	Path    string
	Covered int
	Total   int
	Blocks  []Block
}

// Span tallies the blocks that start on a line in [start, end].
func (f File) Span(start, end int) (covered, coverable int) {
	for _, b := range f.Blocks {
		if b.StartLine < start || b.StartLine > end {
			continue
		}
		coverable += b.Units
		covered += b.covered()
	}
	return covered, coverable
}

// Lines is each line a block spans, true when a block over it ran.
func (f File) Lines() map[int]bool {
	out := map[int]bool{}
	for _, b := range f.Blocks {
		for line := b.StartLine; line <= b.EndLine; line++ {
			out[line] = out[line] || b.Hits > 0
		}
	}
	return out
}

func (b Block) covered() int {
	if b.Hits > 0 {
		return b.Units
	}
	return 0
}

// Parse reads a report in format. modulePath strips a Go profile's
// module-qualified paths and is unused for LCOV. A format magus does not read
// is an error: guessing one would read as 0% or 100% covered. So is a report
// with bytes and no record of its format, which is not a report.
func Parse(format spells.CoverageFormat, data []byte, modulePath string) ([]File, error) {
	switch format {
	case spells.CoverageFormatLCOV:
		return parseChecked(format, data, parseLCOV(data))
	case spells.CoverageFormatGoProfile:
		return parseChecked(format, data, ParseGoProfile(data, modulePath))
	case spells.CoverageFormatNone:
	}
	return nil, fmt.Errorf("coverage format %q is not one magus reads (%s, %s)",
		format, spells.CoverageFormatLCOV, spells.CoverageFormatGoProfile)
}

func parseChecked(format spells.CoverageFormat, data []byte, files []File) ([]File, error) {
	if len(files) == 0 && len(bytes.TrimSpace(data)) > 0 && !hasRecord(format, data) {
		return nil, fmt.Errorf("the report holds %d bytes and no %s record", len(data), format)
	}
	return files, nil
}

// hasRecord reports whether any line is one of format's records. A Go
// profile's mode line counts: a run with nothing to cover writes only it.
func hasRecord(format spells.CoverageFormat, data []byte) bool {
	for _, raw := range strings.Split(string(data), "\n") {
		if isRecord(format, strings.TrimSpace(raw)) {
			return true
		}
	}
	return false
}

func isRecord(format spells.CoverageFormat, line string) bool {
	if format == spells.CoverageFormatLCOV {
		return strings.HasPrefix(line, "SF:") || strings.HasPrefix(line, "DA:")
	}
	_, _, block := parseGoLine(line)
	return block || strings.HasPrefix(line, "mode:")
}

// Rebase makes each file's path workspace-relative. A relative path is taken
// against project, the directory the tests ran in; an absolute one
// against root. A path outside the workspace is dropped.
func Rebase(files []File, root, project string) []File {
	out := make([]File, 0, len(files))
	for _, f := range files {
		if p, ok := rebasePath(f.Path, root, project); ok {
			f.Path = p
			out = append(out, f)
		}
	}
	return out
}

func rebasePath(p, root, project string) (string, bool) {
	if !filepath.IsAbs(p) {
		return path.Join(project, filepath.ToSlash(p)), true
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || !filepath.IsLocal(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// files collects blocks by path.
type files struct {
	byPath map[string]*File
}

func (fs *files) add(p string, b Block) {
	if fs.byPath == nil {
		fs.byPath = map[string]*File{}
	}
	f := fs.byPath[p]
	if f == nil {
		f = &File{Path: p}
		fs.byPath[p] = f
	}
	// A second test binary's profile repeats blocks; adding them would count
	// their units twice.
	if i := slices.IndexFunc(f.Blocks, func(o Block) bool {
		return o.StartLine == b.StartLine && o.EndLine == b.EndLine && o.Units == b.Units
	}); i >= 0 {
		f.Covered -= f.Blocks[i].covered()
		f.Blocks[i].Hits += b.Hits
		f.Covered += f.Blocks[i].covered()
		return
	}
	f.Total += b.Units
	f.Covered += b.covered()
	f.Blocks = append(f.Blocks, b)
}

func (fs *files) sorted() []File {
	if len(fs.byPath) == 0 {
		return nil
	}
	out := make([]File, 0, len(fs.byPath))
	for _, f := range fs.byPath {
		out = append(out, *f)
	}
	slices.SortFunc(out, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// ParseGoProfile reads `go test -coverprofile` output, stripping modulePath
// to leave module-relative paths. A line outside modulePath is dropped, never
// credited to a file it does not name; so is every line when modulePath is
// empty. A malformed line is skipped.
func ParseGoProfile(profile []byte, modulePath string) []File {
	if modulePath == "" {
		return nil
	}
	prefix := strings.TrimSuffix(modulePath, "/") + "/"
	var fs files
	for _, line := range strings.Split(string(profile), "\n") {
		p, b, ok := parseGoLine(strings.TrimSpace(line))
		if rel, inside := strings.CutPrefix(p, prefix); ok && inside {
			fs.add(rel, b)
		}
	}
	return fs.sorted()
}

// parseGoLine decodes "<path>:<sl>.<sc>,<el>.<ec> <units> <hits>". The two
// counts are split off the end first, so a path holding a space still parses.
func parseGoLine(line string) (string, Block, bool) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return "", Block{}, false
	}
	counts, okCounts := atois(fields[len(fields)-2:])
	loc := strings.Join(fields[:len(fields)-2], " ")
	colon := strings.LastIndexByte(loc, ':')
	start, end, okRange := goRange(loc[colon+1:])
	if !okCounts || !okRange || colon < 0 {
		return "", Block{}, false
	}
	return loc[:colon], Block{StartLine: start, EndLine: end, Units: counts[0], Hits: counts[1]}, true
}

// goRange reads the line halves of "<sl>.<sc>,<el>.<ec>".
func goRange(rng string) (start, end int, ok bool) {
	startPart, endPart, _ := strings.Cut(rng, ",")
	lines, ok := atois([]string{lineNumber(startPart), lineNumber(endPart)})
	if !ok {
		return 0, 0, false
	}
	return lines[0], lines[1], true
}

func lineNumber(part string) string {
	line, _, _ := strings.Cut(part, ".")
	return line
}

func atois(fields []string) ([]int, bool) {
	out := make([]int, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// parseLCOV reads an LCOV tracefile's SF and DA records, each DA line one
// unit. Paths are as the tracefile spells them; [Rebase] places them. A DA
// line outside an SF record, or one that does not parse, is skipped.
func parseLCOV(trace []byte) []File {
	var fs files
	current := ""
	for _, raw := range strings.Split(string(trace), "\n") {
		line := strings.TrimSpace(raw)
		if sf, ok := strings.CutPrefix(line, "SF:"); ok {
			current = sf
			continue
		}
		if b, ok := parseDA(line); ok && current != "" {
			fs.add(current, b)
		}
	}
	return fs.sorted()
}

// parseDA decodes "DA:<line>,<hits>[,<checksum>]".
func parseDA(line string) (Block, bool) {
	rest, ok := strings.CutPrefix(line, "DA:")
	fields := strings.Split(rest, ",")
	if !ok || len(fields) < 2 {
		return Block{}, false
	}
	n, ok := atois(fields[:2])
	if !ok {
		return Block{}, false
	}
	return Block{StartLine: n[0], EndLine: n[0], Units: 1, Hits: n[1]}, true
}
