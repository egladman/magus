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
