package cache

import (
	"bufio"
	"bytes"
	"os"
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

// tailLines returns the last n lines of data and the count of lines omitted before them.
// n <= 0 returns all of data with zero omitted.
func tailLines(data []byte, n int) (tail []byte, omitted int) {
	if n <= 0 {
		return data, 0
	}
	lines := bytes.Count(data, []byte{'\n'})
	if !bytes.HasSuffix(data, []byte{'\n'}) && len(data) > 0 {
		lines++ // trailing partial line
	}
	if lines <= n {
		return data, 0
	}
	// Walk back from the end past n newline-terminated lines.
	cut := len(data)
	if bytes.HasSuffix(data, []byte{'\n'}) {
		cut-- // ignore the final newline so it isn't counted as a line boundary
	}
	for seen := 0; cut > 0; cut-- {
		if data[cut-1] == '\n' {
			seen++
			if seen == n {
				break
			}
		}
	}
	return data[cut:], lines - n
}

// failureExcerpt selects a compact set of diagnostic windows from a captured log.
// Build tools commonly print the decisive error well before their final FAIL footer,
// so a tail alone is often not actionable. Each matching line keeps one line of
// context either side; windows are merged and capped. If no diagnostic is recognized,
// fall back to the final lines, which still include the tool's terminal outcome.
func failureExcerpt(data []byte, limit int) (excerpt []byte, omitted int) {
	if limit <= 0 {
		return data, 0
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return nil, 0
	}

	important := func(line string) bool {
		line = strings.ToLower(strings.TrimSpace(line))
		return strings.Contains(line, "error") ||
			strings.Contains(line, "fatal") ||
			strings.Contains(line, "panic") ||
			strings.Contains(line, "fail") ||
			strings.Contains(line, "mismatch") ||
			strings.Contains(line, "undefined") ||
			strings.Contains(line, "not found") ||
			strings.Contains(line, "cannot ")
	}

	selected := make([]bool, len(lines))
	count := 0
	for i, line := range lines {
		if !important(line) {
			continue
		}
		for j := max(0, i-1); j <= min(len(lines)-1, i+1); j++ {
			if !selected[j] {
				selected[j] = true
				count++
			}
		}
	}
	if count == 0 {
		start := max(0, len(lines)-limit)
		return []byte(strings.Join(lines[start:], "\n") + "\n"), start
	}

	var out []string
	for i, line := range lines {
		if !selected[i] {
			continue
		}
		if len(out) == limit {
			break
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n") + "\n"), len(lines) - len(out)
}
