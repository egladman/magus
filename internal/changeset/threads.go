package changeset

import (
	"errors"
	"fmt"
	"strings"

	"github.com/egladman/magus/types"
)

// ErrNoThread reports that an id names no thread on the review. Callers test for it with
// errors.Is to tell a mistyped id from a review that could not be read.
var ErrNoThread = errors.New("no review thread has that id")

// Thread is a top-level review comment and the replies made to it, in the order the host listed
// them. Its id is the Head's ID, which is what [types.ReviewComment].Root and
// [types.DiffOutline].Thread carry.
type Thread struct {
	Head    types.ReviewComment
	Replies []types.ReviewComment
}

// ID is the thread id.
func (t Thread) ID() string { return t.Head.ID }

// Comments returns the head followed by its replies.
func (t Thread) Comments() []types.ReviewComment {
	return append([]types.ReviewComment{t.Head}, t.Replies...)
}

// GroupThreads groups the host's flat comments into threads, in the order their heads first
// appear. It is the one definition of what a thread is; every client that draws, places or
// addresses a thread goes through it so they cannot disagree.
//
// A comment is a reply only when its Root names a top-level comment (empty Root) in comments.
// Anything else heads a thread of its own: a reply whose head the host trimmed, a comment rooted
// at itself, comments rooted at each other, or a reply to a reply. Such a comment is shown rather
// than dropped or attached somewhere it was not said, because "a colleague said nothing" is the
// one thing a review reader must not be told by accident.
func GroupThreads(comments []types.ReviewComment) []Thread {
	if len(comments) == 0 {
		return nil
	}
	heads := headIndexes(comments)
	var out []Thread
	at := make(map[int]int, len(comments))
	for i, c := range comments {
		if heads[i] == i {
			at[i] = len(out)
			out = append(out, Thread{Head: c})
		}
	}
	// A second pass, because a reply can precede its head in the order the host sent.
	for i, c := range comments {
		if heads[i] != i {
			t := &out[at[heads[i]]]
			t.Replies = append(t.Replies, c)
		}
	}
	return out
}

// FindThread returns the thread that holds the comment id names, whether that is its head or one
// of its replies. It returns an error wrapping [ErrNoThread] when comments hold no such comment.
func FindThread(comments []types.ReviewComment, id string) (Thread, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Thread{}, fmt.Errorf("%w: the id is empty", ErrNoThread)
	}
	for _, t := range GroupThreads(comments) {
		if t.Head.ID == id {
			return t, nil
		}
		for _, r := range t.Replies {
			if r.ID == id {
				return t, nil
			}
		}
	}
	return Thread{}, fmt.Errorf("%w: %q", ErrNoThread, id)
}

// headIndexes returns, for each comment, the index of the comment that heads its thread: its own
// index unless it is a reply in the [GroupThreads] sense. When two top-level comments share an
// id, the first one heads the replies that name it.
func headIndexes(comments []types.ReviewComment) []int {
	topLevel := make(map[string]int, len(comments))
	for i, c := range comments {
		if c.Root != "" || c.ID == "" {
			continue
		}
		if _, dup := topLevel[c.ID]; !dup {
			topLevel[c.ID] = i
		}
	}
	heads := make([]int, len(comments))
	for i, c := range comments {
		heads[i] = i
		if c.Root == "" || c.Root == c.ID {
			continue
		}
		if h, ok := topLevel[c.Root]; ok {
			heads[i] = h
		}
	}
	return heads
}
