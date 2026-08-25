// Package diff holds the shared diff session: the one object a console tab, an MCP
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
package diff

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
	sessions map[string]*types.DiffSession
	// viewedPath is where the digest set is persisted, empty to disable persistence (tests).
	viewedPath string
	// hunks maps a root to the file each of its hunk digests belongs to, and to how many
	// hunks each file has. It is what lets MarkViewed answer "did that finish a file", which
	// a bare digest cannot.
	//
	// Server-side only, deliberately off the wire: the console computes these digests itself
	// from the patch it fetched, so sending the mapping back would be shipping a large map
	// nobody reads. Populated by TrackHunks at attach, where the patch is already in hand.
	hunks  map[string]map[string]string
	counts map[string]map[string]int
	nextID int
}

// NewStore returns a session store persisting viewed state under stateDir. An empty stateDir
// keeps everything in memory, which is what a test wants and what a workspace-less daemon
// gets.
func NewStore(stateDir string) *Store {
	s := &Store{
		sessions: map[string]*types.DiffSession{},
		hunks:    map[string]map[string]string{},
		counts:   map[string]map[string]int{},
	}
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
// behavior this exists to avoid.
func HunkDigest(path string, lines []string) string {
	// hash.Hash.Write is documented never to return an error, so the returns are discarded
	// explicitly rather than checked - a branch that cannot be taken is untestable, and
	// pretending otherwise would put unreachable error handling in a hot path.
	h := sha256.New()
	_, _ = h.Write([]byte(path))
	_, _ = h.Write([]byte{0})
	for _, l := range lines {
		_, _ = h.Write([]byte(l))
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// Attach returns the session for root, creating it and adopting any persisted viewed set on
// first use. review is the freshly computed annotated changeset; an existing session takes it
// as an update, so a client that recomputes does not clobber the conversation.
func (s *Store) Attach(root string, base string, rev types.Diff, asOf string) *types.DiffSession {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[root]
	if !ok {
		s.nextID++
		sess = &types.DiffSession{
			ID:     fmt.Sprintf("rev%d", s.nextID),
			Base:   base,
			Cursor: types.DiffCursor{Hunk: -1},
			Viewed: s.loadViewed(),
		}
		s.sessions[root] = sess
	}
	sess.Base = base
	sess.Diff = rev
	// The snapshot identity travels with the changeset it describes, so no client can hold one
	// without the other and believe a stale answer is current.
	sess.AsOf = asOf
	return clone(sess)
}

// TrackHunks records which file each hunk digest belongs to, so a later MarkViewed can say
// whether the mark finished a file.
//
// Called at attach, where the patch has already been read for the snapshot id. Replaces the
// previous mapping wholesale: the changeset it describes has just been recomputed, and a
// digest from the old one no longer names anything a reader can mark.
func (s *Store) TrackHunks(root string, files []FileHunks) {
	s.mu.Lock()
	defer s.mu.Unlock()

	byDigest := make(map[string]string)
	counts := make(map[string]int)
	for _, f := range files {
		for _, h := range f.Hunks {
			byDigest[h.Digest] = f.Path
			counts[f.Path]++
		}
	}
	s.hunks[root] = byDigest
	s.counts[root] = counts
}

// Get returns the session for root, or nil when none is attached.
func (s *Store) Get(root string) *types.DiffSession {
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
func (s *Store) SetCursor(root string, c types.DiffCursor) *types.DiffSession {
	return s.mutate(root, func(sess *types.DiffSession) {
		sess.Cursor = c
	})
}

// MarkViewed adds or removes a hunk digest from the human's progress set and persists it.
//
// finished names the file this mark just completed, empty when it completed none. That is
// what a caller mints a read receipt from, and the reason it is reported HERE rather than
// computed by the caller: a mark arriving on this route is live by construction - a person
// pressed something - which is the property a receipt rests on. The persisted viewed set is
// an unauthenticated file, so a file that merely LOOKS complete after a reload must never
// mint one on its own.
func (s *Store) MarkViewed(root, digest string, viewed bool) (sess *types.DiffSession, finished string) {
	sess = s.mutate(root, func(sess *types.DiffSession) {
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
		if viewed {
			finished = s.completedBy(root, digest, sess.Viewed)
		}
	})
	return sess, finished
}

// completedBy reports the file digest belongs to when every one of that file's hunks is now
// marked, and "" otherwise. Callers hold the lock.
//
// An untracked digest completes nothing rather than completing a file of one hunk: it means
// the patch moved under the session, and guessing there would mint a receipt for a file the
// reader never finished.
func (s *Store) completedBy(root, digest string, viewed []string) string {
	path, ok := s.hunks[root][digest]
	if !ok {
		return ""
	}
	total := s.counts[root][path]
	if total == 0 {
		return ""
	}
	marked := 0
	for _, d := range viewed {
		if s.hunks[root][d] == path {
			marked++
		}
	}
	if marked < total {
		return ""
	}
	return path
}

// AddComment attaches a remark. author is stamped by the CALLER from the transport the write
// arrived on - never from the request body - which is what stops an agent posting as the
// human. See types.DiffAuthor.
func (s *Store) AddComment(root string, c types.DiffComment, author types.DiffAuthor) *types.DiffSession {
	return s.mutate(root, func(sess *types.DiffSession) {
		c.Author = author
		c.ID = fmt.Sprintf("c%d", len(sess.Comments)+1)
		sess.Comments = append(sess.Comments, c)
	})
}

// ResolveComment marks a comment resolved. Either party may resolve: a human closing an
// agent's point and an agent closing its own after fixing it are both normal, and requiring
// the author to do it would strand comments whose author has gone away.
func (s *Store) ResolveComment(root, id string, resolved bool) *types.DiffSession {
	return s.mutate(root, func(sess *types.DiffSession) {
		for i := range sess.Comments {
			if sess.Comments[i].ID == id {
				sess.Comments[i].Resolved = resolved
				return
			}
		}
	})
}

// Suggest enqueues an agent's request for attention. It does NOT move the cursor, and that
// omission is the design - see types.DiffSuggestion.
func (s *Store) Suggest(root string, sug types.DiffSuggestion) *types.DiffSession {
	return s.mutate(root, func(sess *types.DiffSession) {
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
func (s *Store) AnswerSuggestion(root, id string, accept bool) *types.DiffSession {
	return s.mutate(root, func(sess *types.DiffSession) {
		for i := range sess.Suggestions {
			if sess.Suggestions[i].ID != id {
				continue
			}
			sess.Suggestions[i].Accepted = accept
			sess.Suggestions[i].Declined = !accept
			if accept {
				sess.Cursor = types.DiffCursor{Path: sess.Suggestions[i].Path, Hunk: sess.Suggestions[i].Hunk}
			}
			return
		}
	})
}

// mutate applies fn under the lock and returns a copy, or nil when no session is attached.
func (s *Store) mutate(root string, fn func(*types.DiffSession)) *types.DiffSession {
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
func clone(s *types.DiffSession) *types.DiffSession {
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
