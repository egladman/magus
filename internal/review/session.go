// Package review holds the shared review session: the one object a console tab, an MCP
// agent, and the CLI all read while a change is being reviewed.
//
// One object rather than three, because the daemon already multiplexes those transports over
// one workspace and three privately-rebuilt reviews would be three diverging opinions of the
// same changeset. Sharing it is what makes pairing work at all: an agent that can see where
// the human is looking can be useful about it, instead of narrating into the void.
//
// State is split by lifetime, deliberately:
//
//   - COORDINATION (cursor, comments, suggestions) lives in memory for the daemon's life. It
//     is about a conversation happening right now; outliving the conversation would resurrect
//     stale suggestions into a review nobody is having.
//   - PROGRESS (which hunks the human has read) is persisted, because it is the one piece
//     whose whole value is surviving an interruption. It is keyed by CONTENT DIGEST, so the
//     mark survives a rebase that did not touch the hunk - which is the failing of every
//     viewed-checkbox that resets on force-push.
package review

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// Store holds the live sessions and the persisted viewed set.
//
// One session per workspace root rather than an unbounded map keyed by an opaque id: a review
// is about a working tree, and two sessions over one tree would let a console tab and an
// agent mark progress on different objects while both believed they were paired. Attaching
// twice returns the same session, which is what every client actually wants.
type Store struct {
	mu       sync.Mutex
	sessions map[string]*types.ReviewSession
	// viewedPath is where the digest set is persisted, empty to disable persistence (tests).
	viewedPath string
	nextID     int
}

// NewStore returns a session store persisting viewed state under stateDir. An empty stateDir
// keeps everything in memory, which is what a test wants and what a workspace-less daemon
// gets.
func NewStore(stateDir string) *Store {
	s := &Store{sessions: map[string]*types.ReviewSession{}}
	if stateDir != "" {
		s.viewedPath = filepath.Join(stateDir, "review", "viewed.json")
	}
	return s
}

// HunkDigest is the content address of one hunk: its file path and its body.
//
// The PATH is included, so the same three lines changed in two files are two marks. The hunk
// HEADER is not, because its line numbers move whenever anything above it changes - a digest
// over them would reset every mark in a file on any edit near the top, which is the exact
// behaviour this exists to avoid.
func HunkDigest(path string, lines []string) string {
	h := sha256.New()
	h.Write([]byte(path))
	h.Write([]byte{0})
	for _, l := range lines {
		h.Write([]byte(l))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Attach returns the session for root, creating it and adopting any persisted viewed set on
// first use. review is the freshly computed annotated changeset; an existing session takes it
// as an update, so a client that recomputes does not clobber the conversation.
func (s *Store) Attach(root string, base string, rev types.Review) *types.ReviewSession {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[root]
	if !ok {
		s.nextID++
		sess = &types.ReviewSession{
			ID:     fmt.Sprintf("rev%d", s.nextID),
			Base:   base,
			Cursor: types.ReviewCursor{Hunk: -1},
			Viewed: s.loadViewed(),
		}
		s.sessions[root] = sess
	}
	sess.Base = base
	sess.Review = rev
	return clone(sess)
}

// Get returns the session for root, or nil when none is attached.
func (s *Store) Get(root string) *types.ReviewSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[root]
	if !ok {
		return nil
	}
	return clone(sess)
}

// SetCursor records where the HUMAN is looking. There is no agent equivalent on purpose: an
// agent that could write this would be moving the reader's viewport, which is the one thing
// the suggestion queue exists to prevent.
func (s *Store) SetCursor(root string, c types.ReviewCursor) *types.ReviewSession {
	return s.mutate(root, func(sess *types.ReviewSession) {
		sess.Cursor = c
	})
}

// MarkViewed adds or removes a hunk digest from the human's progress set and persists it.
func (s *Store) MarkViewed(root, digest string, viewed bool) *types.ReviewSession {
	return s.mutate(root, func(sess *types.ReviewSession) {
		i := slices.Index(sess.Viewed, digest)
		switch {
		case viewed && i < 0:
			sess.Viewed = append(sess.Viewed, digest)
		case !viewed && i >= 0:
			sess.Viewed = slices.Delete(sess.Viewed, i, i+1)
		default:
			return
		}
		s.saveViewed(sess.Viewed)
	})
}

// AddComment attaches a remark. author is stamped by the CALLER from the transport the write
// arrived on - never from the request body - which is what stops an agent posting as the
// human. See types.ReviewAuthor.
func (s *Store) AddComment(root string, c types.ReviewComment, author types.ReviewAuthor) *types.ReviewSession {
	return s.mutate(root, func(sess *types.ReviewSession) {
		c.Author = author
		c.ID = fmt.Sprintf("c%d", len(sess.Comments)+1)
		sess.Comments = append(sess.Comments, c)
	})
}

// ResolveComment marks a comment resolved. Either party may resolve: a human closing an
// agent's point and an agent closing its own after fixing it are both normal, and requiring
// the author to do it would strand comments whose author has gone away.
func (s *Store) ResolveComment(root, id string, resolved bool) *types.ReviewSession {
	return s.mutate(root, func(sess *types.ReviewSession) {
		for i := range sess.Comments {
			if sess.Comments[i].ID == id {
				sess.Comments[i].Resolved = resolved
				return
			}
		}
	})
}

// Suggest enqueues an agent's request for attention. It does NOT move the cursor, and that
// omission is the design - see types.ReviewSuggestion.
func (s *Store) Suggest(root string, sug types.ReviewSuggestion) *types.ReviewSession {
	return s.mutate(root, func(sess *types.ReviewSession) {
		sug.ID = fmt.Sprintf("s%d", len(sess.Suggestions)+1)
		sug.Accepted, sug.Declined = false, false
		sess.Suggestions = append(sess.Suggestions, sug)
	})
}

// AnswerSuggestion records the human's decision AND, on acceptance, moves the cursor - which
// is the only path by which a suggestion ever reaches the viewport. The human pressed a key;
// the agent did not move anything.
//
// Declining is recorded rather than discarded so an agent can tell "not yet seen" from "seen
// and declined" and stop repeating itself.
func (s *Store) AnswerSuggestion(root, id string, accept bool) *types.ReviewSession {
	return s.mutate(root, func(sess *types.ReviewSession) {
		for i := range sess.Suggestions {
			if sess.Suggestions[i].ID != id {
				continue
			}
			sess.Suggestions[i].Accepted = accept
			sess.Suggestions[i].Declined = !accept
			if accept {
				sess.Cursor = types.ReviewCursor{Path: sess.Suggestions[i].Path, Hunk: sess.Suggestions[i].Hunk}
			}
			return
		}
	})
}

// mutate applies fn under the lock and returns a copy, or nil when no session is attached.
func (s *Store) mutate(root string, fn func(*types.ReviewSession)) *types.ReviewSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[root]
	if !ok {
		return nil
	}
	fn(sess)
	return clone(sess)
}

// clone returns a deep-enough copy that a caller cannot mutate live session state through the
// slices it was handed. The Review inside is treated as immutable (Attach replaces it whole),
// so it rides along by reference rather than being copied per read.
func clone(s *types.ReviewSession) *types.ReviewSession {
	out := *s
	out.Viewed = slices.Clone(s.Viewed)
	out.Comments = slices.Clone(s.Comments)
	out.Suggestions = slices.Clone(s.Suggestions)
	return &out
}

// loadViewed reads the persisted digest set. Every failure yields an empty set rather than an
// error: losing review progress is a nuisance, and failing to open a review because a
// progress file is corrupt would be worse than forgetting what was read.
func (s *Store) loadViewed() []string {
	if s.viewedPath == "" {
		return nil
	}
	b, err := os.ReadFile(s.viewedPath)
	if err != nil {
		return nil
	}
	var out []string
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

// saveViewed persists the digest set, best-effort for the same reason loadViewed is.
func (s *Store) saveViewed(digests []string) {
	if s.viewedPath == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.viewedPath), 0o755); err != nil {
		return
	}
	b, err := json.Marshal(digests)
	if err != nil {
		return
	}
	tmp := s.viewedPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, s.viewedPath)
}
