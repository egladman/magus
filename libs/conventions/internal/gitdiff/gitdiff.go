package gitdiff

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Span is a closed range of line numbers.
type Span struct{ From, To int }

// Overlaps reports whether any of spans meets the closed range from..to.
func Overlaps(spans []Span, from, to int) bool {
	for _, s := range spans {
		if s.From <= to && from <= s.To {
			return true
		}
	}
	return false
}

// Changes measures the working copy against the merge base of a base revision
// and HEAD. The merge base is resolved once, on first use. It is safe for
// concurrent use.
type Changes struct {
	base string

	once sync.Once
	rev  string
	err  error
}

// New returns Changes measured against base, such as origin/main.
func New(base string) *Changes { return &Changes{base: base} }

// Rev returns the base revision as given to [New].
func (c *Changes) Rev() string { return c.base }

// Lines returns the changed lines of the file at path. A file the base lacks
// changed on every line.
func (c *Changes) Lines(path string) ([]Span, error) {
	spans, _, err := c.lines(path)
	return spans, err
}

// File returns the changed lines of the file at path and its text at the
// base, nil when the base lacks the file.
func (c *Changes) File(path string) ([]Span, []byte, error) {
	spans, rev, err := c.lines(path)
	if err != nil || rev == "" {
		return spans, nil, err
	}
	dir, name := filepath.Split(path)
	base, err := git(dir, "show", rev+":./"+name)
	if err != nil {
		return nil, nil, err
	}
	return spans, []byte(base), nil
}

// lines returns the changed spans and the merge base, empty when the base
// lacks the file.
func (c *Changes) lines(path string) ([]Span, string, error) {
	dir, name := filepath.Split(path)
	rev, err := c.merge(dir)
	if err != nil {
		return nil, "", err
	}
	if _, err := git(dir, "cat-file", "-e", rev+":./"+name); err != nil {
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, "", readErr
		}
		return []Span{{1, bytes.Count(src, []byte("\n")) + 1}}, "", nil
	}
	diff, err := git(dir, "diff", "--unified=0", "--no-color", "--no-ext-diff", rev, "--", name)
	if err != nil {
		return nil, "", err
	}
	return hunks(diff), rev, nil
}

func (c *Changes) merge(dir string) (string, error) {
	c.once.Do(func() {
		out, err := git(dir, "merge-base", "HEAD", c.base)
		if err != nil {
			c.err = fmt.Errorf("base %q: %w", c.base, err)
			return
		}
		c.rev = strings.TrimSpace(out)
	})
	return c.rev, c.err
}

var hunkHeader = regexp.MustCompile(`(?m)^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// hunks returns the working-copy line ranges of a zero-context diff. A pure
// deletion at line n spans n and the line after it, where the removal shows.
func hunks(diff string) []Span {
	var spans []Span
	for _, m := range hunkHeader.FindAllStringSubmatch(diff, -1) {
		start, _ := strconv.Atoi(m[1])
		count := 1
		if m[2] != "" {
			count, _ = strconv.Atoi(m[2])
		}
		if count == 0 {
			spans = append(spans, Span{start, start + 1})
			continue
		}
		spans = append(spans, Span{start, start + count - 1})
	}
	return spans
}

func git(dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
