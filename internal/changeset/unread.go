package changeset

import (
	"fmt"
	"io"

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

// UnreadState says whether an UnreadReport could be computed.
type UnreadState string

const (
	// UnreadKnown means the read marks were read, so Unread is the answer.
	UnreadKnown UnreadState = "known"
	// UnreadUnknown means they could not be, so Unread is empty and says nothing.
	UnreadUnknown UnreadState = "unknown"
)

// UnreadReport is what `magus diff --unread` emits. State is UnreadUnknown when the read marks
// could not be read, and Unread is then empty: an unreadable store says nothing about what is
// unread.
type UnreadReport struct {
	Source string              `json:"source"`
	State  UnreadState         `json:"state"`
	Reason string              `json:"reason,omitempty"`
	Hunks  int                 `json:"hunks"`
	Unread []types.DiffHunkRef `json:"unread"`
}

// BuildUnreadReport sets the hunks of patch against the digests viewed holds. source names
// what was reviewed, and loadErr is the error from reading the digests, if any.
func BuildUnreadReport(source, patch string, viewed []string, loadErr error) UnreadReport {
	parsed := ParseHunks(patch)
	rep := UnreadReport{Source: source, State: UnreadKnown, Unread: []types.DiffHunkRef{}}
	for _, f := range parsed {
		rep.Hunks += len(f.Hunks)
	}
	if loadErr != nil {
		rep.State, rep.Reason = UnreadUnknown, loadErr.Error()
		return rep
	}
	rep.Unread = append(rep.Unread, UnreadHunks(parsed, viewed)...)
	return rep
}

// WriteUnread renders rep for a person, one line per hunk. The ranges come from patch, since a
// report names hunks by digest and a reader finds them by line. next is the command that opens
// the same changeset in the viewer.
func WriteUnread(w io.Writer, rep UnreadReport, patch, next string) error {
	for _, line := range unreadLines(rep, patch, next) {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func unreadLines(rep UnreadReport, patch, next string) []string {
	if rep.State == UnreadUnknown {
		return []string{
			fmt.Sprintf("read state unknown for %s: the read marks could not be read (%s)", rep.Source, rep.Reason),
			fmt.Sprintf("%d hunks in the range; none is called unread", rep.Hunks),
		}
	}
	if len(rep.Unread) == 0 {
		return []string{fmt.Sprintf("every hunk of %s is marked read (%d hunks)", rep.Source, rep.Hunks)}
	}
	ranges := map[string]string{}
	for _, f := range ParseHunks(patch) {
		for _, h := range f.Hunks {
			ranges[types.DiffHunkRef{Path: f.Path, Index: h.Index}.Key()] = NewRange(f.Path, h.NewStart, h.NewCount)
		}
	}
	lines := []string{fmt.Sprintf("%d of %d hunks in %s are not marked read", len(rep.Unread), rep.Hunks, rep.Source)}
	for _, r := range rep.Unread {
		at := ranges[r.Key()]
		if at == "" {
			at = r.Path
		}
		lines = append(lines, "  "+at)
	}
	return append(lines, "mark them read in the viewer: "+next)
}

// NewRange is path:first-last, or path:line (deleted) for a hunk that leaves no line behind.
func NewRange(path string, start, count int) string {
	switch {
	case count == 0:
		return fmt.Sprintf("%s:%d (deleted)", path, start)
	case count == 1:
		return fmt.Sprintf("%s:%d", path, start)
	}
	return fmt.Sprintf("%s:%d-%d", path, start, start+count-1)
}
