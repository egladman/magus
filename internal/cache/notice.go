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
// A structuralFailureLine match outranks a diagnosticLine one when the budget is
// oversubscribed, rather than being exempt from it. Rank, not exemption, is the whole
// subtlety: internal/cache's own suite verifies magus's failure reporting, so it is
// dense with lines like "[fail]" and "cause:" that are EXPECTED, passing output and
// match diagnosticLine exactly as well as a real failure does. Ranking keeps the one
// real "--- FAIL: TestX" from being evicted by that noise while still honoring limit,
// so a broadly-failing `go test ./...` cannot dump its whole log to the console.
func failureExcerpt(data []byte, limit int) (excerpt []byte, omitted int) {
	if limit <= 0 {
		return data, 0
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return nil, 0
	}

	// Interest tiers, weakest first. A line's rank is the strongest reason to show it:
	// its own match, or being within one line of a stronger one.
	const (
		uninteresting = iota
		keywordMatch
		structuralFailure
	)
	rank := make([]int, len(lines))
	mark := func(i, tier int) {
		for j := max(0, i-1); j <= min(len(lines)-1, i+1); j++ {
			rank[j] = max(rank[j], tier)
		}
	}
	matched := false
	for i, line := range lines {
		switch {
		case structuralFailureLine.MatchString(line):
			mark(i, structuralFailure)
			matched = true
		case diagnosticLine.MatchString(line):
			mark(i, keywordMatch)
			matched = true
		}
	}
	if !matched {
		start := max(0, len(lines)-limit)
		return []byte(strings.Join(lines[start:], "\n") + "\n"), start
	}

	// Spend the budget by tier, and within a tier keep the LAST lines: a tool prints
	// its setup before it prints what went wrong, so filling from the top spends the
	// budget on whatever merely looked diagnostic early on and drops the actual
	// failure - the one line the reader opened the output for.
	keep := make([]bool, len(lines))
	room := limit
	for _, tier := range []int{structuralFailure, keywordMatch} {
		for i := len(lines) - 1; i >= 0 && room > 0; i-- {
			if rank[i] == tier {
				keep[i] = true
				room--
			}
		}
	}

	out := make([]string, 0, limit)
	for i, line := range lines {
		if keep[i] {
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

// structuralFailureLine matches a test tool's own STRUCTURAL failure marker, as
// opposed to diagnosticLine's loose keyword match. `go test` writes these for
// exactly this purpose - "this is the one that failed" - so unlike an incidental
// "fail"/"cause" elsewhere in the log, a match here is never budget-evicted; see
// failureExcerpt. Anchored to (indented) line start so it cannot fire mid-sentence,
// and covers a top-level or nested subtest ("--- FAIL: ", indented under its
// parent), a panic, and go test's own terminal "FAIL" line (bare, or the
// package-and-duration summary "FAIL\t<pkg>\t<dur>").
var structuralFailureLine = regexp.MustCompile(`^\s*(--- FAIL: |panic: |FAIL(\s|$))`)
