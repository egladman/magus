package eval

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Change is one file a commit after the cutoff changed: the rows the next-edit and
// commit-replay test sets are built from in later stages.
type Change struct {
	Commit  string `json:"commit"`
	Date    string `json:"date"`
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// Temporal lists the non-merge commits after the cutoff, up to rev, with each changed file
// that keep accepts. Training reads the tree at the cutoff; these are the hunks no file
// split can leak into.
func Temporal(root, cutoff, rev string, keep func(path string) bool) ([]Change, int, error) {
	cmd := exec.Command("git", "-C", root, "log", "--no-merges", "--reverse", "--numstat",
		"--format=%x01%H%x00%cI", cutoff+".."+rev)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, 0, fmt.Errorf("git log %s..%s: %s: %w", cutoff, rev, strings.TrimSpace(errb.String()), err)
	}
	var changes []Change
	commits := 0
	var commit, date string
	sc := bufio.NewScanner(&out)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "\x01"):
			parts := strings.SplitN(line[1:], "\x00", 2)
			if len(parts) != 2 {
				return nil, 0, fmt.Errorf("git log: unexpected header %q", line)
			}
			commit, date = parts[0], parts[1]
			commits++
		case line == "":
		default:
			f := strings.SplitN(line, "\t", 3)
			if len(f) != 3 || !keep(f[2]) {
				continue
			}
			added, err1 := strconv.Atoi(f[0])
			removed, err2 := strconv.Atoi(f[1])
			if err1 != nil || err2 != nil {
				continue
			}
			changes = append(changes, Change{Commit: commit, Date: date, Path: f[2], Added: added, Removed: removed})
		}
	}
	return changes, commits, sc.Err()
}
