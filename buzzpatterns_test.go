package magus_test

// A source-level check over the Buzz this repository ships, in the spirit of
// cmd/magus's TestEveryCommandBindsDisplayFlags: a text scan, because the thing
// it prevents cannot be observed at runtime without rendering the whole site and
// watching memory.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scanLoopRe matches `while (<expr>.indexOf(...) != null)`, the shape of a loop
// that rescans a string it is also rewriting.
var scanLoopRe = regexp.MustCompile(`while\s*\([^)]*\.indexOf\([^)]*\)\s*!=\s*null`)

// buzzScanOptOut lets a genuine case through, and demands a reason in the same
// breath - the same shape as the repo's other acknowledged suppressions.
const buzzScanOptOut = "buzz-scan-ok:"

// TestNoRescanningStringLoops keeps a quadratic, memory-retaining idiom out of
// the Buzz sources.
//
// `while (s.indexOf(x) != null) { s = s.replace(x, y) }` is the natural way to
// write replace-all in Buzz, because str.replace substitutes only the FIRST
// occurrence. It is also the most expensive way: each pass copies the whole
// string, and on the default VM build every copy is a distinct string interned
// for the life of the process and never freed (see libs/gopherbuzz/vm/value.go -
// the intern table has no eviction, and it is what bounds the never-freed heap,
// so the fix cannot be eviction). Removing three of these from the docs render
// cut its measured peak from 5806MB to 4259MB.
//
// The replacements:
//
//	s.split(x).join(y)                 replace-all, one pass
//	s.split(x).len() - 1               count occurrences, one pass
//
// Neither is a drop-in for collapsing RUNS of a substring: splitting on two
// spaces leaves the odd one behind. Split on one and drop the empty parts.
func TestNoRescanningStringLoops(t *testing.T) {
	var findings []string

	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree is not this gate's business: it scans the
			// sources that ARE readable and says nothing about the rest, rather
			// than failing a lint over a permissions quirk.
			return nil //nolint:nilerr // deliberate: skip, do not abort the walk
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "worktrees", "gen", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".buzz" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil //nolint:nilerr // same: an unreadable file is skipped, not fatal
		}
		lines := strings.Split(string(body), "\n")
		for i, line := range lines {
			if !scanLoopRe.MatchString(line) {
				continue
			}
			// An opt-out on the loop or the line above it.
			if strings.Contains(line, buzzScanOptOut) ||
				(i > 0 && strings.Contains(lines[i-1], buzzScanOptOut)) {
				continue
			}
			findings = append(findings, path+":"+itoa(i+1)+": "+strings.TrimSpace(line))
		}
		return nil
	})
	require.NoError(t, err)

	assert.Emptyf(t, findings,
		"a string loop that rescans what it rewrites copies the whole string per pass,\n"+
			"and every copy is interned for the life of the process:\n  %s\n\n"+
			"Use s.split(x).join(y) to replace all, or s.split(x).len()-1 to count.\n"+
			"To collapse RUNS, split on ONE separator and drop the empty parts.\n"+
			"If a loop genuinely has to rescan, put `%s <reason>` on it.",
		strings.Join(findings, "\n  "), buzzScanOptOut)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
