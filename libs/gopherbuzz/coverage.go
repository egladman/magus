package buzz

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	vmpackage "github.com/egladman/magus/libs/gopherbuzz/vm"
)

// LineCoverage accumulates executable-line hits for Buzz source under
// CompileOptions.DebugLines. The denominator is every distinct source line that
// emitted bytecode in a stamped chunk; the numerator is the subset the step
// hook observed. Report writes LCOV so magusfile targets can feed lcov\percent
// the same way the TypeScript suite does.
//
// Not safe for concurrent use across sessions; one collector per process is the
// intended shape (magus buzz -t --coverprofile).
type LineCoverage struct {
	mu    sync.Mutex
	found map[string]map[int]struct{} // file -> lines that have bytecode
	hits  map[string]map[int]int      // file -> line -> hit count
}

// NewLineCoverage returns an empty collector.
func NewLineCoverage() *LineCoverage {
	return &LineCoverage{
		found: map[string]map[int]struct{}{},
		hits:  map[string]map[int]int{},
	}
}

// ObserveChunk registers every distinct source line in ch (and nested Funs)
// that carries debug line info and a non-empty SourceFile. Lines without a
// SourceFile are ignored: imports clear the stamp so an entry-file coverprofile
// is not diluted by modules measured when THEY are the -t subject.
func (lc *LineCoverage) ObserveChunk(ch *vmpackage.Chunk) {
	if lc == nil || ch == nil {
		return
	}
	lc.mu.Lock()
	defer lc.mu.Unlock()
	observeChunkLocked(lc, ch)
}

func observeChunkLocked(lc *LineCoverage, ch *vmpackage.Chunk) {
	if ch.SourceFile != "" && len(ch.Lines) > 0 {
		lines := lc.found[ch.SourceFile]
		if lines == nil {
			lines = map[int]struct{}{}
			lc.found[ch.SourceFile] = lines
		}
		for _, ln := range ch.Lines {
			if ln > 0 {
				lines[int(ln)] = struct{}{}
			}
		}
	}
	for _, child := range ch.Funs {
		observeChunkLocked(lc, child)
	}
}

// Hit records one execution of file:line. No-op when file is empty or line < 1.
func (lc *LineCoverage) Hit(file string, line int) {
	if lc == nil || file == "" || line < 1 {
		return
	}
	lc.mu.Lock()
	defer lc.mu.Unlock()
	byLine := lc.hits[file]
	if byLine == nil {
		byLine = map[int]int{}
		lc.hits[file] = byLine
	}
	byLine[line]++
}

// StepHook returns a MaskLine callback that records hits from DebugFrame.
// Only stamped SourceFile paths count: Source falls back to a function name
// for the debugger, and those labels must not become LCOV SF: records.
func (lc *LineCoverage) StepHook() (vmpackage.StepMask, func(vmpackage.StepEvent, vmpackage.DebugFrame)) {
	return vmpackage.MaskLine, func(ev vmpackage.StepEvent, f vmpackage.DebugFrame) {
		if ev != vmpackage.StepLine {
			return
		}
		lc.Hit(f.SourceFile, f.Line)
	}
}

// Report returns an LCOV document covering every observed file. Files are sorted
// and lines within each file are sorted so a committed badge is byte-stable.
// Only files registered by ObserveChunk appear: hit-only keys (impossible once
// StepHook ignores empty SourceFile) are not invented here.
func (lc *LineCoverage) Report() string {
	if lc == nil {
		return ""
	}
	lc.mu.Lock()
	defer lc.mu.Unlock()

	files := make([]string, 0, len(lc.found))
	for f := range lc.found {
		files = append(files, f)
	}
	sort.Strings(files)

	var b strings.Builder
	b.WriteString("TN:\n")
	for _, file := range files {
		found := lc.found[file]
		hits := lc.hits[file]
		lines := make([]int, 0, len(found))
		for ln := range found {
			lines = append(lines, ln)
		}
		sort.Ints(lines)

		fmt.Fprintf(&b, "SF:%s\n", file)
		lh := 0
		for _, ln := range lines {
			n := 0
			if hits != nil {
				n = hits[ln]
			}
			if n > 0 {
				lh++
			}
			fmt.Fprintf(&b, "DA:%d,%d\n", ln, n)
		}
		fmt.Fprintf(&b, "LF:%d\n", len(lines))
		fmt.Fprintf(&b, "LH:%d\n", lh)
		b.WriteString("end_of_record\n")
	}
	return b.String()
}
