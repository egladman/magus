package cache

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

// noticePrefix marks a line that magus bubbles up to the user in silent mode.
// Any captured output line whose trimmed text begins with this prefix is promoted;
// the remainder (trimmed) is the message. It lets a target opt specific output into
// the otherwise-silent stream — "no news is good news" unless it declares news:
//
//	echo "magus:notice: deployed api v1.2.3"
const noticePrefix = "magus:notice:"

// maxFailTailLines caps how many trailing lines of a failing project's captured log
// are echoed in silent mode; the full log is retained and its path is printed.
const maxFailTailLines = 50

// maxFailureExcerptLines bounds the default failure display. The full raw log is
// retained in the output store; this excerpt surfaces the likely cause in a
// human-readable result instead of replaying an unrelated wall of output.
const maxFailureExcerptLines = 24

// extractNotices scans the log at path for noticePrefix-marked lines and returns
// their messages in order. A missing or unreadable log yields no messages.
func extractNotices(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if msg, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), noticePrefix); ok {
			out = append(out, strings.TrimSpace(msg))
		}
	}
	return out
}

// failureExcerpt selects a compact set of diagnostic windows from a captured log.
// Build tools commonly print the decisive error well before their final FAIL footer,
// so a tail alone is often not actionable. Each matching line keeps one line of
// context either side; windows are merged and capped. If no diagnostic is recognized,
// fall back to the final lines, which still include the tool's terminal outcome.
//
// A canonicalFailureLine match is PINNED: it survives budget pressure that would
// otherwise evict it. Without this, internal/cache's own test suite - which verifies
// magus's failure reporting, so it is unusually dense with lines like "[fail]" and
// "cause:" as EXPECTED, passing output - could produce enough incidental
// diagnosticLine matches after the one real "--- FAIL: TestX" to push it out of a
// 24-line budget entirely. That happened for real: a genuine race-condition bug in
// RunAll's dependency barrier was invisible in the console excerpt for exactly this
// reason, and was only found by pulling the full uploaded log.
func failureExcerpt(data []byte, limit int) (excerpt []byte, omitted int) {
	if limit <= 0 {
		return data, 0
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return nil, 0
	}

	selected := make([]bool, len(lines))
	pinned := make([]bool, len(lines))
	mark := func(i int, pin bool) {
		for j := max(0, i-1); j <= min(len(lines)-1, i+1); j++ {
			selected[j] = true
			if pin {
				pinned[j] = true
			}
		}
	}
	anyMatch := false
	for i, line := range lines {
		switch {
		case canonicalFailureLine.MatchString(line):
			mark(i, true)
			anyMatch = true
		case diagnosticLine.MatchString(line):
			mark(i, false)
			anyMatch = true
		}
	}
	if !anyMatch {
		start := max(0, len(lines)-limit)
		return []byte(strings.Join(lines[start:], "\n") + "\n"), start
	}

	// Budget goes to pinned lines first, unconditionally - a log with more pinned
	// (structurally real) failures than the limit prints all of them rather than
	// silently cutting some; that case is rare, and dropping a confirmed failure to
	// stay under a display budget is the exact bug this exists to prevent. Whatever
	// remains of the budget goes to the loosely matched lines, keeping the LAST ones:
	// a tool prints its setup before it prints what went wrong, so filling the
	// budget from the top spends it on whatever merely looked diagnostic early on.
	pinnedCount, unpinnedCount := 0, 0
	for i := range lines {
		switch {
		case pinned[i]:
			pinnedCount++
		case selected[i]:
			unpinnedCount++
		}
	}
	budget := limit - pinnedCount
	if budget < 0 {
		budget = 0
	}
	drop := unpinnedCount - budget

	kept := make([]bool, len(lines))
	skipped := 0
	for i := range lines {
		switch {
		case pinned[i]:
			kept[i] = true
		case selected[i] && skipped < drop:
			skipped++
		case selected[i]:
			kept[i] = true
		}
	}

	var out []string
	for i, line := range lines {
		if kept[i] {
			out = append(out, line)
		}
	}
	return []byte(strings.Join(out, "\n") + "\n"), len(lines) - len(out)
}

// diagnosticLine matches a line that looks like a real diagnostic.
//
// The keywords have to land as WHOLE WORDS that are not part of a path or
// identifier, which is the whole subtlety here. A substring test for "error" put
// this into the excerpt of every failing run in this repo:
//
//	{
//	    "Path": "github.com/pkg/errors",
//	    "Version": "v0.9.1",
//
// - an ordinary go.mod dependency, matched because its import path ends in
// "errors", with the surrounding JSON dragged in as context. So the character
// before and after a keyword must not be a word character, "/", "." or "-":
// that rejects "pkg/errors", "go-errors" and "errors.go" while still accepting
// "2 errors occurred", "error:" and "--- FAIL:".
var diagnosticLine = regexp.MustCompile(
	`(?i)(^|[^\w/.\-])(errors?|fatal|panics?|fail(s|ed|ure|ures)?|mismatch|undefined|not found|cannot)($|[^\w/.\-])`)

// canonicalFailureLine matches a test tool's own STRUCTURAL failure marker, as
// opposed to diagnosticLine's loose keyword match. `go test` writes these for
// exactly this purpose - "this is the one that failed" - so unlike an incidental
// "fail"/"cause" elsewhere in the log, a match here is never budget-evicted; see
// failureExcerpt. Anchored to (indented) line start so it cannot fire mid-sentence,
// and covers a top-level or nested subtest ("--- FAIL: ", indented under its
// parent), a panic, and go test's own terminal "FAIL" line (bare, or the
// package-and-duration summary "FAIL\t<pkg>\t<dur>").
var canonicalFailureLine = regexp.MustCompile(`^\s*(--- FAIL: |panic: |FAIL(\s|$))`)
