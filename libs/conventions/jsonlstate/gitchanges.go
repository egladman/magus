package jsonlstate

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

// gitChanges copies blankerror's on purpose: each package has its own span
// type and File signature, and sharing would couple two checks.
type gitChanges struct {
	base string

	once sync.Once
	rev  string
	err  error
}

func (g *gitChanges) merge(dir string) (string, error) {
	g.once.Do(func() {
		out, err := git(dir, "merge-base", "HEAD", g.base)
		if err != nil {
			g.err = fmt.Errorf("base %q: %w", g.base, err)
			return
		}
		g.rev = strings.TrimSpace(out)
	})
	return g.rev, g.err
}

func (g *gitChanges) File(path string) ([]span, error) {
	dir, name := filepath.Split(path)
	rev, err := g.merge(dir)
	if err != nil {
		return nil, err
	}
	if _, err := git(dir, "cat-file", "-e", rev+":./"+name); err != nil {
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil, readErr
		}
		return []span{{1, bytes.Count(src, []byte("\n")) + 1}}, nil
	}
	diff, err := git(dir, "diff", "--unified=0", "--no-color", "--no-ext-diff", rev, "--", name)
	if err != nil {
		return nil, err
	}
	return hunks(diff), nil
}

var hunkHeader = regexp.MustCompile(`(?m)^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// hunks returns the working-copy line ranges of a zero-context diff. A pure
// deletion at line n spans n and the line after it, where the removal shows.
func hunks(diff string) []span {
	var spans []span
	for _, m := range hunkHeader.FindAllStringSubmatch(diff, -1) {
		start, _ := strconv.Atoi(m[1])
		count := 1
		if m[2] != "" {
			count, _ = strconv.Atoi(m[2])
		}
		if count == 0 {
			spans = append(spans, span{start, start + 1})
			continue
		}
		spans = append(spans, span{start, start + count - 1})
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
