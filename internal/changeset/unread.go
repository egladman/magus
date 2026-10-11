package changeset

import (
	"fmt"

	"github.com/egladman/magus/types"
)

// UnreadHunks lists the hunks of files that no read mark covers, in patch order. A mark
// covers a hunk by digest, so an edit that changes a hunk's lines or its context makes it
// unread again, and the same hunk body in two files is two digests.
func UnreadHunks(files []FileHunks, viewed []string) []types.DiffHunkRef {
	read := make(map[string]struct{}, len(viewed))
	for _, d := range viewed {
		read[d] = struct{}{}
	}
	var out []types.DiffHunkRef
	for _, f := range files {
		for _, h := range f.Hunks {
			if _, ok := read[h.Digest]; ok {
				continue
			}
			out = append(out, types.DiffHunkRef{Path: f.Path, Index: h.Index, Digest: h.Digest})
		}
	}
	return out
}

// NewRange is path:first-last, or path:line (deleted) for a hunk that leaves no line behind.
func NewRange(path string, start, count int) string {
	switch count {
	case 0:
		return fmt.Sprintf("%s:%d (deleted)", path, start)
	case 1:
		return fmt.Sprintf("%s:%d", path, start)
	}
	return fmt.Sprintf("%s:%d-%d", path, start, start+count-1)
}
