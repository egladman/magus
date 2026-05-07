package interp

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/fatih/color"
)

func pryColorize(useColor bool, s string, attrs ...color.Attribute) string {
	if !useColor {
		return s
	}
	return color.New(attrs...).Sprint(s)
}

// ColorEnabledForFile reports whether ANSI color should be used when writing
// to f. Returns false when f is not a terminal or NO_COLOR is set.
func ColorEnabledForFile(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if f == nil {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// PrintSourceContext prints radius lines on each side of line from path, with
// the target line highlighted.
func PrintSourceContext(w io.Writer, path string, line, radius int, useColor bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(w, "  (cannot read source: %v)\n", err)
		return
	}
	lines := strings.Split(string(data), "\n")
	start := line - radius
	if start < 1 {
		start = 1
	}
	end := line + radius
	if end > len(lines) {
		end = len(lines)
	}
	for i := start; i <= end; i++ {
		prefix := "  "
		if i == line {
			prefix = pryColorize(useColor, "=>", color.FgYellow, color.Bold)
		}
		fmt.Fprintf(w, "%s %4d: %s\n", prefix, i, lines[i-1])
	}
}

// PrintHistory writes pry REPL history to w; rest is the .history argument
// (empty = last 50, "!N" = Nth-most-recent, "N" = last N lines).
func PrintHistory(w io.Writer, hist *History, rest string) {
	if hist == nil {
		fmt.Fprintln(w, "(history unavailable)")
		return
	}
	if strings.HasPrefix(rest, "!") {
		n, err := strconv.Atoi(strings.TrimPrefix(rest, "!"))
		if err != nil || n <= 0 {
			fmt.Fprintln(w, "usage: .history!<n>  (1 = most recent)")
			return
		}
		line := hist.Recall(n)
		if line == "" {
			fmt.Fprintln(w, "(out of range)")
			return
		}
		fmt.Fprintln(w, line)
		return
	}
	limit := 50
	if rest != "" {
		n, err := strconv.Atoi(rest)
		if err == nil && n > 0 {
			limit = n
		}
	}
	lines := hist.Lines()
	start := 0
	if len(lines) > limit {
		start = len(lines) - limit
	}
	for i := start; i < len(lines); i++ {
		fmt.Fprintf(w, "%4d: %s\n", len(lines)-i, lines[i])
	}
}
