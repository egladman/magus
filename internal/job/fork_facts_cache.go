package job

import (
	"cmp"
	"path/filepath"
	"slices"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/vcs"
)

// missingCommitsFile holds [reached] answers beside jobs.jsonl. An answer
// depends only on its two commits, so none goes stale. The last line per pair
// is its entry. Its name keeps a commit-copy answer from being read as one.
const missingCommitsFile = "reached.jsonl"

// missingAnswer is one line of missingCommitsFile: Missing of Tip's commits
// Head lacks, 0 when Head holds Tip's content.
type missingAnswer struct {
	Head    string `json:"head"`
	Tip     string `json:"tip"`
	Missing int    `json:"missing"`
}

type missingKey struct{ head, tip string }

// missingCache is missingCommitsFile as one read found it, plus what that
// read learned. A file that cannot be read or written only costs the answers.
type missingCache struct {
	path    string
	known   map[missingKey]int
	lines   int
	partial bool
	learned []missingAnswer
}

// readMissingCache reads root's answers. A line that does not parse is skipped.
func readMissingCache(root string) *missingCache {
	c := &missingCache{path: missingCommitsPath(root), known: map[missingKey]int{}}
	if c.path == "" {
		return c
	}
	read, err := codec.ReadLines(c.path)
	if err != nil {
		return c
	}
	c.lines, c.partial = len(read.Lines), read.Partial
	for _, line := range read.Lines {
		var a missingAnswer
		if codec.Unmarshal(line.Text, &a) == nil && a.Head != "" && a.Tip != "" {
			c.known[missingKey{a.Head, a.Tip}] = a.Missing
		}
	}
	return c
}

func (c *missingCache) get(head, tip string) (int, bool) {
	n, ok := c.known[missingKey{head, tip}]
	return n, ok
}

func (c *missingCache) put(head, tip string, missing int) {
	c.known[missingKey{head, tip}] = missing
	c.learned = append(c.learned, missingAnswer{Head: head, Tip: tip, Missing: missing})
}

// write appends what this read learned. A compaction keeps only the answers
// naming one of revs, the bases just read. Bases only move on, so no one asks
// about an older one again.
func (c *missingCache) write(revs ...string) error {
	if c.path == "" || len(c.learned) == 0 {
		return nil
	}
	var fresh [][]byte
	for _, a := range c.learned {
		if line, err := codec.Marshal(a); err == nil {
			fresh = append(fresh, line)
		}
	}
	if !c.partial && !codec.CompactDue(c.lines+len(fresh), len(c.known)) {
		return codec.AppendLines(c.path, fresh, 0o644)
	}
	var kept []missingAnswer
	for k, n := range c.known {
		if slices.Contains(revs, k.head) || slices.Contains(revs, k.tip) {
			kept = append(kept, missingAnswer{Head: k.head, Tip: k.tip, Missing: n})
		}
	}
	slices.SortFunc(kept, func(a, b missingAnswer) int { return cmp.Or(cmp.Compare(a.Head, b.Head), cmp.Compare(a.Tip, b.Tip)) })
	all := make([][]byte, 0, len(kept))
	for _, a := range kept {
		if line, err := codec.Marshal(a); err == nil {
			all = append(all, line)
		}
	}
	return codec.RewriteLines(c.path, all, 0o644)
}

// missingCommitsPath is "" when no state dir answers.
func missingCommitsPath(root string) string {
	base, err := xdg.StateDir()
	if err != nil {
		return ""
	}
	dir, err := vcs.StateDir(base, "jobs", root)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, missingCommitsFile)
}
