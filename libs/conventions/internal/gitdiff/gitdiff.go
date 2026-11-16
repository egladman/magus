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
// and HEAD. The merge base and the renames git detects since it are resolved
// once, on first use, so a renamed file is measured against its old path. It
// is safe for concurrent use.
type Changes struct {
	base string

	once    sync.Once
	rev     string
	renames map[string]string // repo-relative new path to old path
	err     error

	prefixes sync.Map // dir to its repo-relative prefix
}

// New returns Changes measured against base, such as origin/main.
func New(base string) *Changes { return &Changes{base: base} }

// Rev returns the base revision as given to [New].
func (c *Changes) Rev() string { return c.base }

// Lines returns the changed lines of the file at path. A file the base lacks
// changed on every line; a renamed file is compared with its old path.
func (c *Changes) Lines(path string) ([]Span, error) {
	spans, _, _, err := c.lines(path)
	return spans, err
}

// File returns the changed lines of the file at path and its text at the
// base, nil when the base lacks the file. A renamed file's base text is the
// text at its old path.
func (c *Changes) File(path string) ([]Span, []byte, error) {
	spans, rev, old, err := c.lines(path)
	if err != nil || rev == "" {
		return spans, nil, err
	}
	dir, _ := filepath.Split(path)
	base, err := git(dir, "show", rev+":"+old)
	if err != nil {
		return nil, nil, err
	}
	return spans, []byte(base), nil
}

// paths returns the repo-relative path of path and the path it had at the
// merge base, which differs after a rename.
func (c *Changes) paths(dir, path string) (cur, old string, err error) {
	if _, err := c.merge(dir); err != nil {
		return "", "", err
	}
	prefix, ok := c.prefixes.Load(dir)
	if !ok {
		out, err := git(dir, "rev-parse", "--show-prefix")
		if err != nil {
			return "", "", err
		}
		prefix, _ = c.prefixes.LoadOrStore(dir, strings.TrimSpace(out))
	}
	cur = prefix.(string) + filepath.Base(path)
	if from, renamed := c.renames[cur]; renamed {
		return cur, from, nil
	}
	return cur, cur, nil
}

// lines returns the changed spans, the merge base and the path at the merge
// base. The merge base is empty when the base lacks the file.
func (c *Changes) lines(path string) ([]Span, string, string, error) {
	dir, _ := filepath.Split(path)
	rev, err := c.merge(dir)
	if err != nil {
		return nil, "", "", err
	}
	cur, old, err := c.paths(dir, path)
	if err != nil {
		return nil, "", "", err
	}
	if _, err := git(dir, "cat-file", "-e", rev+":"+old); err != nil {
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, "", "", readErr
		}
		return []Span{{1, bytes.Count(src, []byte("\n")) + 1}}, "", "", nil
	}
	diff, err := git(dir, "diff", "--find-renames", "--unified=0", "--no-color", "--no-ext-diff", rev, "--", top(old), top(cur))
	if err != nil {
		return nil, "", "", err
	}
	return hunks(diff), rev, old, nil
}

func (c *Changes) merge(dir string) (string, error) {
	c.once.Do(func() {
		out, err := git(dir, "merge-base", "HEAD", c.base)
		if err != nil {
			c.err = fmt.Errorf("base %q: %w", c.base, err)
			return
		}
		c.rev = strings.TrimSpace(out)
		c.renames, c.err = renamesSince(dir, c.rev)
	})
	return c.rev, c.err
}

// top is a literal pathspec rooted at the repository, so it means the same
// from any directory.
func top(path string) string { return ":(top,literal)" + path }

// renamesSince maps each path git detects as renamed since rev to its old
// path, both repo-relative.
func renamesSince(dir, rev string) (map[string]string, error) {
	out, err := git(dir, "diff", "--find-renames", "--name-status", "-z", "--no-color", "--no-ext-diff", rev)
	if err != nil {
		return nil, err
	}
	renames := map[string]string{}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		status := fields[i]
		switch {
		case strings.HasPrefix(status, "R"):
			if i+2 >= len(fields) {
				return nil, fmt.Errorf("git diff --name-status: truncated rename entry %q", status)
			}
			renames[fields[i+2]] = fields[i+1]
			i += 2
		case strings.HasPrefix(status, "C"):
			i += 2
		case status != "":
			i++
		}
	}
	return renames, nil
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
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(stderr.String()), err)
	}
	return string(out), nil
}
