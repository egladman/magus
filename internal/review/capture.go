package review

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/notes"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/text/plural"
	"github.com/egladman/magus/types/diff"
)

// ServerGet reads path from the running server and returns the body of a 200 answer. ok is
// false for every other outcome (no server, no token, a refusal, a transport error): the
// callers have one message for all of them, and it is about the review rather than the
// transport. The caller owns the credential, which is why it is passed in.
type ServerGet func(ctx context.Context, path string) (body []byte, ok bool)

// NoteResolverLoader loads the knowledge graph for root and returns a resolver over it. The
// caller binds a scope with ForScope before checking a store's notes.
type NoteResolverLoader func(ctx context.Context, root string) (knowledge.NoteResolver, error)

// ResolveNoteStores resolves every declared location, in the order a reader should see them.
//
// Neither being declared is an error with instructions rather than an empty listing: an
// empty list would say "you have no notes" when the truth is "this workspace has nowhere
// to put one", and those call for completely different next actions.
func ResolveNoteStores(root string, dirs NoteDirs, only string) ([]NoteStore, error) {
	found, err := NoteStores(root, dirs)
	if err != nil {
		return nil, fmt.Errorf("magus notes: %w", err)
	}
	var out []NoteStore
	for _, s := range found {
		if only == "" || only == string(s.Scope) {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		if only != "" {
			return nil, fmt.Errorf("magus notes: this workspace declares no %s notes store, set knowledge.notes.%s in magus.yaml", only, only)
		}
		return nil, errors.New("magus notes: this workspace declares no notes store, set knowledge.notes.shared " +
			"(a directory IN the repo: your team gets these, and git records who wrote each one) or knowledge.notes.private " +
			"(a directory anywhere: only this machine has them, and nothing attributes them)")
	}
	return out, nil
}

// FindNote locates one note across the declared stores. A name present in both is an
// error rather than a silent preference: the two mean different things to a reader, so
// guessing which was meant is the one thing that must not happen.
func FindNote(stores []NoteStore, name string) (notes.Note, NoteStore, error) {
	var found []NoteStore
	var note notes.Note
	for _, st := range stores {
		n, err := notes.Get(st.Dir, name)
		switch {
		case err == nil:
			note, found = n, append(found, st)
		case errors.Is(err, os.ErrNotExist):
			// Genuinely absent here; keep looking in the other store.
		default:
			// The note EXISTS and could not be read. Reporting that as "no such note"
			// sends the reader hunting for a missing file, and lets `notes edit` scaffold
			// a second copy in the other store on top of the broken one.
			return notes.Note{}, NoteStore{}, fmt.Errorf("magus notes: %q (%s store): %w", name, st.Scope, unreadableNoteError{err})
		}
	}
	switch len(found) {
	case 0:
		return notes.Note{}, NoteStore{}, fmt.Errorf("magus notes: no note named %q", name)
	case 1:
		return note, found[0], nil
	default:
		return notes.Note{}, NoteStore{}, fmt.Errorf("magus notes: %q: %w", name, ErrAmbiguousNote)
	}
}

// ErrAmbiguousNote marks a name present in both stores, so a caller can tell it apart from
// "not found" instead of treating both as "nothing here". It carries the fix as well as
// the diagnosis, because it is the text every wrapping caller prints and a reader told
// the name is ambiguous still needs the flag that resolves it.
var ErrAmbiguousNote = errors.New("exists in both stores, say which with --shared or --private")

// ErrUnreadableNote marks a note whose file is THERE and unreadable, so a caller can
// tell it apart from "not found" the way ErrAmbiguousNote separates out "in both". It
// exists for `notes edit`: without a sentinel to match, an unreadable note fell past
// every case of that switch and was handled as if the name were free, scaffolding a
// second copy in the other store on top of the broken one.
var ErrUnreadableNote = errors.New("exists but could not be read")

// unreadableNoteError reports why a note could not be read and still matches
// ErrUnreadableNote, so the reason follows the sentinel without a second %w in the
// wrapping format.
type unreadableNoteError struct{ cause error }

func (e unreadableNoteError) Error() string {
	return fmt.Sprintf("%s: %s", ErrUnreadableNote.Error(), e.cause.Error())
}

func (e unreadableNoteError) Unwrap() []error { return []error{ErrUnreadableNote, e.cause} }

// ListNotes gathers every note in stores that carries all of tags, with the repair warnings of
// each store and the stores themselves. Under reproducible the values that are real but
// unstable (file mtimes, absolute paths) are left out, so the result can be committed.
func ListNotes(root string, stores []NoteStore, tags []string, reproducible bool) (found []notes.ScopedNote, issues []notes.Issue, listed []notes.StoreOutput, err error) {
	listed = make([]notes.StoreOutput, 0, len(stores))
	for _, st := range stores {
		dir := st.Dir
		if reproducible {
			dir = notes.RelativeToRoot(root, dir)
		}
		listed = append(listed, notes.StoreOutput{Scope: string(st.Scope), Path: dir})
		ns, is, err := notes.Inspect(st.Dir)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, n := range ns {
			if !n.HasTags(tags) {
				continue
			}
			found = append(found, notes.ScopedNote{
				Note:     n,
				Scope:    string(st.Scope),
				Path:     notes.Locate(root, st.Scope, n),
				Modified: notes.NoteModified(n, reproducible),
			})
		}
		if reproducible {
			// An issue names the file it is about by absolute path, which identifies the
			// machine that ran this as much as the note. Relative to the workspace it
			// still points at the same file for whoever reads the export.
			for i := range is {
				is[i].Path = notes.RelativeToRoot(root, is[i].Path)
			}
		}
		issues = append(issues, is...)
	}
	return found, issues, listed, nil
}

// CaptureEnv is what a capture needs from the workspace: where it is, which notes stores it
// declares, and how to load the graph its anchors resolve against.
type CaptureEnv struct {
	Root string
	Dirs NoteDirs
	Load NoteResolverLoader
}

// Store picks the store a capture is written to: private by default, because
// committing a capture puts quoted, half-finished words in front of review under your
// name. --shared is how the team gets it.
func (e CaptureEnv) Store(shared, private bool) (NoteStore, error) {
	scope, err := notes.ScopeFromFlags(shared, private)
	if err != nil {
		return NoteStore{}, err
	}
	defaulted := scope == ""
	if defaulted {
		scope = string(notes.ScopePrivate)
	}
	stores, err := ResolveNoteStores(e.Root, e.Dirs, scope)
	if err != nil {
		// Never a quiet fall back to shared: that turns a safety default into a surprise
		// commit. A shared-only workspace is common, so the error names --shared.
		if defaulted {
			return NoteStore{}, fmt.Errorf("pass --shared to put this capture in the repository, where review sees it: %w", err)
		}
		return NoteStore{}, err
	}
	return stores[0], nil
}

// Gap records a harness gap. Every anchor is resolved against the graph before
// anything is written, so a gap that could not be found again is refused rather than stored.
func (e CaptureEnv) Gap(ctx context.Context, g notes.Gap, name string, shared, private bool) error {
	target, err := e.Store(shared, private)
	if err != nil {
		return err
	}
	res, err := e.Load(ctx, e.Root)
	if err != nil {
		return fmt.Errorf("magus notes capture --gap: its anchors cannot be resolved because the knowledge graph would not load: %w", err)
	}
	scoped := res.ForScope(string(target.Scope))
	rev := notes.Revision(ctx, e.Root)
	g.AsOf, g.Captured = rev, time.Now()
	n, err := g.Note(ctx, name, scoped)
	if err != nil {
		return fmt.Errorf("magus notes capture --gap: %w", err)
	}
	if _, err := notes.Get(target.Dir, n.Name); err == nil {
		return fmt.Errorf("magus notes capture --gap: %q already exists in the %s store, pass --name to keep both", n.Name, target.Scope)
	}
	if err := notes.Save(target.Dir, n); err != nil {
		return fmt.Errorf("magus notes capture --gap: %w", err)
	}
	if _, err := notes.RecordDigests(ctx, target.Dir, n.Name, rev, scoped); err != nil {
		return fmt.Errorf("magus notes capture --gap: captured %q, but recording its anchors failed: %w", n.Name, err)
	}
	saved, err := notes.Get(target.Dir, n.Name)
	if err != nil {
		return fmt.Errorf("magus notes capture --gap: wrote %q, but it does not read back as a note: %w", n.Name, err)
	}
	fmt.Printf("Captured gap %s into %s [%s] (%s).\n", saved.Name, notes.Locate(e.Root, target.Scope, saved), target.Scope, notes.AnchorSummary(saved))
	for _, line := range notes.FindBlock(ctx, scoped, saved) {
		fmt.Println(line)
	}
	return nil
}

// CaptureFromSession maps a review session onto the store's source-agnostic capture shape.
// The mapping lives here rather than in internal/notes so the store never has to know what a
// diff is, and so a second source can be added without it learning.
//
// comments are the remarks already on the host's review, and they are captured ALONGSIDE the
// session's own. A transcript holding only your half of a conversation is not a transcript of
// the conversation: the question a reader has months later is what was decided, and the answer
// is nearly always in what somebody else said back.
func CaptureFromSession(sess *diff.Review, comments []diff.ReviewComment, title string, tags []string) notes.Capture {
	// Grouped by file, because that is how a reviewer looks for a thread later ("what did we
	// say about key.go"). Comment order within a file is left alone: it is the order the
	// conversation happened in, and sorting it would break the replies.
	byPath := map[string][]notes.CaptureEntry{}
	paths := []string{}
	add := func(path string, e notes.CaptureEntry) {
		if _, seen := byPath[path]; !seen {
			paths = append(paths, path)
		}
		byPath[path] = append(byPath[path], e)
	}
	// The host's comments first, per file. What a colleague said usually came before the
	// remark it provoked, and a transcript that opened with the reply reads backwards.
	for _, t := range comments {
		add(t.Path, notes.CaptureEntry{
			Subject: t.Path,
			Locator: notes.LineLocator(t.Line),
			// The author the host reported, verbatim. Nothing here verifies it, and nothing
			// should: a capture records who a conversation says spoke, and a store that
			// second-guessed that would be making the authorship claim it exists to avoid.
			Author: t.Author,
			Body:   t.Body,
		})
	}
	for _, c := range sess.Comments {
		add(c.Path, notes.CaptureEntry{
			Subject:  c.Path,
			Locator:  notes.HunkLocator(c.Hunk),
			Author:   commentAuthor(c),
			Body:     c.Body,
			Resolved: c.Resolved,
		})
	}
	sort.Strings(paths)

	entries := []notes.CaptureEntry{}
	for _, p := range paths {
		entries = append(entries, byPath[p]...)
	}
	return notes.Capture{
		Title: notes.FirstNonEmpty(title, "Review thread on "+notes.FirstNonEmpty(sess.Base, "the working tree")),
		Tags:  tags,
		Source: notes.Source{
			Kind:     notes.SourceReviewThread,
			Ref:      sess.ID,
			AsOf:     sess.AsOf,
			Captured: time.Now(),
		},
		Entries: entries,
	}
}

// commentAuthor renders who said something. An agent is named as one: a transcript that let a
// tool's output read as a colleague's opinion would be the capture telling the reader the one
// thing it must not.
func commentAuthor(c diff.Comment) string {
	if c.Author != diff.AuthorAgent {
		// "reviewer", not a name. The session records which route and credential wrote this,
		// never which person, so a name would be invented; and "You" would be a lie to
		// everyone except the one reader who captured it, which is exactly the wrong reader to
		// optimize a committed note for.
		return "reviewer"
	}
	if c.AgentName == "" {
		return "agent"
	}
	return c.AgentName + " (agent)"
}

// CaptureName derives a note name from the patch the thread was written against, so two
// captures from different reviews cannot collide and a repeat of the SAME review is caught by
// the exists check rather than silently overwriting.
func CaptureName(sess *diff.Review) string {
	digest := sess.AsOf
	if len(digest) > 12 {
		digest = digest[:12]
	}
	if digest == "" {
		return "review-thread"
	}
	return "review-" + digest
}

// CaptureSession is the local half of the conversation to transcribe, and whether it came from
// a running server.
//
// The server is preferred because it holds strictly more: an agent's remarks, and remarks
// already published to the review, neither of which the store keeps. But the store keeps the
// unsent human ones, which is what a person writing alone in `magus diff` produces, so the
// absence of a server is a smaller transcript rather than no transcript. The caller says which
// one it got; see the capture command's output.
//
// patch is asked for only when there is no server, since the server already holds the session.
func CaptureSession(ctx context.Context, get ServerGet, patch func(context.Context) string, cacheDir string) (*diff.Review, bool, error) {
	if sess := serverDiffReview(ctx, get); sess != nil {
		return sess, true, nil
	}
	sess, err := storedDiffReview(cacheDir, patch(ctx))
	return sess, false, err
}

// storedDiffReview rebuilds what a capture needs from the files the store persists.
//
// AsOf is the patch digest attachDiffReview would have stamped with no server in the picture,
// so the note this capture names is the one either path would have named for this tree. No
// patch leaves it EMPTY rather than digesting the empty string, which would name every such
// capture the same note and make the second one collide with the first.
func storedDiffReview(cacheDir, patch string) (*diff.Review, error) {
	drafts, err := changeset.NewStore(cacheDir).LoadDrafts()
	if err != nil {
		return nil, err
	}
	sess := &diff.Review{Comments: drafts}
	if patch != "" {
		sess.AsOf = changeset.PatchDigest(patch)
	}
	return sess, nil
}

// serverDiffReview reads the running server's review session, or nil when there is no server,
// no token, or nothing attached. Every failure is nil rather than an error: the caller has one
// message to print for all of them, and it is about the session rather than the transport.
//
// /api/v1/diff/session, NOT /api/v1/diff. The latter is the annotated changeset: it loads the
// symbol shards and walks a reverse closure, and it wants the paths under review. This one
// hands back the attached session as it stands, which is all a transcript needs, and answers
// 409 when nothing is attached.
func serverDiffReview(ctx context.Context, get ServerGet) *diff.Review {
	body, ok := get(ctx, "/api/v1/diff/session")
	if !ok {
		return nil
	}
	var sess diff.Review
	if err := codec.Unmarshal(body, &sess); err != nil {
		return nil
	}
	return &sess
}

// Remarks reads what colleagues said on the review this branch has open, and the reason
// the read was incomplete when there is one.
//
// The server answers when one is running, because its session also knows which threads the
// reader has already had on screen. Without one the forge is asked directly: a colleague's
// remark is a fact about the review, not about whether a background process happens to be up,
// and the same patch on the same branch must not show a different conversation either way.
func Remarks(ctx context.Context, get ServerGet, from diff.ReviewOrigin, cacheDir string) ([]diff.ReviewComment, string) {
	if comments, reason, served := ServerReviewComments(ctx, get); served {
		return comments, reason
	}
	return localReviewComments(ctx, from, cacheDir)
}

// ServerReviewComments reads the comments from a running server. served says whether the server
// answered at all, which is what separates "no server, ask the forge yourself" from "the server
// looked and there is no review open".
//
// The reason is separate from the emptiness, and only non-empty when magus READ the review and
// could not understand part of it. That is the one case a caller must not pass over quietly: a
// transcript silently missing a colleague's remark is worse than no transcript.
func ServerReviewComments(ctx context.Context, get ServerGet) (comments []diff.ReviewComment, reason string, served bool) {
	raw, ok := get(ctx, "/api/v1/diff/review")
	if !ok {
		return nil, "", false
	}
	var body struct {
		ID       string               `json:"id"`
		Comments []diff.ReviewComment `json:"threads"`
		Reason   string               `json:"reason"`
	}
	if err := codec.Unmarshal(raw, &body); err != nil {
		return nil, "", false
	}
	if body.ID == "" {
		// No review open. The reason names which ordinary situation that is, and none of them
		// is worth a line during a capture: there is simply no second half.
		return nil, "", true
	}
	return body.Comments, body.Reason, true
}

// localReviewComments asks the forge itself, the way the check-review job and the server's own
// review handler do. Placement is left to the caller: the server resolves comments against the
// working tree, and a caller showing some other patch has to place them against that one.
//
// The origin and the cache dir are passed rather than a workspace, so a test can answer them
// without a repository, the narrowing the server's own review source uses.
func localReviewComments(ctx context.Context, from diff.ReviewOrigin, cacheDir string) ([]diff.ReviewComment, string) {
	at := bindings.FindReview(ctx, from.Branch, from.Remote)
	if !at.Open() {
		return nil, ""
	}
	comments, err := bindings.ReviewComments(ctx, at)
	// The watermark is PERSISTED, so the new-comment mark survives without the server that
	// normally applies it. Reading it here never moves it, for the reason the handler gives.
	// An unreadable watermark marks nothing new rather than everything, and its error rides
	// with the forge's.
	seen, seenErr := changeset.NewStore(cacheDir).LoadSeenComments()
	err = errors.Join(err, seenErr)
	watermark := diff.Review{SeenComments: seen}
	fresh := make(map[string]struct{})
	for _, id := range watermark.UnseenComments(comments) {
		if seenErr == nil {
			fresh[id] = struct{}{}
		}
	}
	for i := range comments {
		if _, ok := fresh[comments[i].ID]; ok {
			comments[i].New = true
		}
	}
	if err != nil {
		// The reason rides beside whatever came back: none on a malformed answer, which decodes
		// strictly, and every comment when only the watermark failed.
		return comments, err.Error()
	}
	return comments, ""
}

// NewRemarkLine says how much of the captured conversation this reader had never had in front
// of them, or "" when none of it is new to them.
//
// Printed rather than written into the note, which is the whole reason it is a line and not a
// field. New belongs to the READER's history with the review, not the
// conversation (see diff.ReviewComment.New). A colleague reading the transcript
// next year would see it describe somebody else's morning. Said to the person
// taking the capture, it is the one moment it is worth knowing.
func NewRemarkLine(comments []diff.ReviewComment) string {
	fresh := 0
	for _, t := range comments {
		if t.New {
			fresh++
		}
	}
	if fresh == 0 {
		return ""
	}
	return fmt.Sprintf("%d remark%s on the review had not been in front of you before; `%s` marks them new.",
		fresh, plural.Word(fresh, "", "s"), hint.Diff.String())
}

// NotesFinder loads the graph at most once, and only for a note with find lines, so
// listing plain notes costs no graph load.
type NotesFinder struct {
	Root string
	Load NoteResolverLoader

	loaded bool
	res    *knowledge.NoteResolver
	err    error
}

// Block renders the find lines of n against the graph scoped to scope.
func (f *NotesFinder) Block(ctx context.Context, scope string, n notes.Note) []string {
	if len(n.Find) == 0 {
		return nil
	}
	if !f.loaded {
		f.loaded = true
		res, err := f.Load(ctx, f.Root)
		if err != nil {
			f.err = err
		} else {
			f.res = &res
		}
	}
	if f.res == nil {
		return append(notes.FindBlock(ctx, nil, n), "  because: "+f.err.Error())
	}
	return notes.FindBlock(ctx, f.res.ForScope(scope), n)
}
