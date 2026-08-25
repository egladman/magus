package status

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/egladman/magus/internal/diff"
	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/internal/interp/bindings"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/types"
)

// DiffSessionHandler serves the live paired-review session.
//
// Every write here is stamped DiffAuthorHuman, because this route is only reachable from
// the console and the CLI. The agent's half lives on the MCP surface and is stamped
// DiffAuthorAgent there. Authorship is decided by WHICH ROUTE the write arrived on and
// never by the payload, which is what makes it unforgeable: an agent cannot reach this
// handler, so it cannot post as the person.
//
// It is one route with an `op` rather than five, because these are all small mutations of one
// object and a client applies them from one place - a keypress handler. Five routes would be
// five fetch wrappers for no gain in clarity.
type DiffSessionHandler struct {
	handler.Base
	sessions *diff.Store
	origin   originSource
	root     string
	// cacheDir is where read receipts live. Empty disables minting, which is what a
	// workspace-less daemon and this package's tests get.
	cacheDir string
}

// originSource is the narrow contract publishing needs: where this tree's changes would be
// discussed. Satisfied by *console.Service, matching the other source interfaces in this
// package.
//
// The daemon answers this, never the client. A browser knows the paths it is reviewing and
// nothing about remotes, and a client that supplied one could point a review at a repository
// this workspace is not in.
type originSource interface {
	ReviewOrigin(ctx context.Context) types.ReviewOrigin
}

// NewDiffSessionHandler returns the paired-review handler. cacheDir may be empty, which
// serves the session without recording read receipts; origin may be nil, which serves
// everything except publishing.
func NewDiffSessionHandler(sessions *diff.Store, origin originSource, root, cacheDir string, log *slog.Logger) *DiffSessionHandler {
	h := &DiffSessionHandler{sessions: sessions, origin: origin, root: root, cacheDir: cacheDir}
	h.Base = handler.New(h.serve, log)
	return h
}

// reviewSessionRequest is the wire shape. Op names the mutation; the rest are its arguments,
// and which ones matter depends on Op.
type reviewSessionRequest struct {
	// Op is one of: cursor, viewed, comment, resolve, answer, publish, reply.
	Op string `json:"op"`
	// cursor
	Path string `json:"path,omitempty"`
	Hunk int    `json:"hunk,omitempty"`
	// viewed
	Digest string `json:"digest,omitempty"`
	On     bool   `json:"on,omitempty"`
	// comment
	Body   string `json:"body,omitempty"`
	Anchor string `json:"anchor,omitempty"`
	// publish: the summary heading the review. The branch and remote are NOT here - the
	// daemon resolves those itself, so a client cannot aim a review at another repository.
	Summary string `json:"summary,omitempty"`
	// Line is the position an inline comment anchors to on the new side. A hunk index cannot
	// serve: it means nothing outside the session that produced it.
	Line int `json:"line,omitempty"`
	// resolve / answer, and the THREAD for reply. One field because they are the same
	// question - which one - and never asked together.
	ID string `json:"id,omitempty"`
}

// publish sends every unpublished human draft as one review and marks exactly the ones that
// left. It is the only write on this route that can fail in a way the reader must hear about.
//
// UNPUBLISHED and HUMAN, both filtered here rather than trusted from the request: an agent
// reaches this session through MCP and may draft, and a request naming ids would let a caller
// re-send something already sent. The set is derived from the session, so there is nothing for
// a caller to get wrong.
//
// Marked one at a time after the send, deliberately. A host that accepts four of five drafts
// has still sent four, and a retry that re-posted them would put duplicates in a colleague's
// inbox - the failure this whole path exists to avoid.
func (h *DiffSessionHandler) publish(ctx context.Context, req reviewSessionRequest) (*types.DiffSession, error) {
	sess := h.sessions.Get(h.root)
	if sess == nil {
		return nil, errors.New("no review session attached")
	}
	drafts := make([]types.DiffComment, 0, len(sess.Comments))
	for _, c := range sess.Comments {
		if c.Author == types.DiffAuthorHuman && !c.Published {
			drafts = append(drafts, c)
		}
	}
	if len(drafts) == 0 {
		// Not an error: a reader who publishes twice, or who has nothing drafted, gets the
		// session back unchanged rather than a failure about a mistake they did not make.
		return sess, nil
	}

	at, err := h.openReview(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := bindings.PublishReview(ctx, at, req.Summary, drafts); err != nil {
		return nil, err
	}
	for _, d := range drafts {
		sess = h.sessions.MarkPublished(h.root, d.ID, "")
	}
	return sess, nil
}

// reply answers one thread on the host's review.
//
// Loud like publish, and for the same reason: it is a sentence a colleague is waiting for.
// It touches no session state at all - a reply belongs to the host's record, and a local copy
// would be a second version of a conversation that already has one. The client re-reads the
// review to see it, which is also how it finds out what everyone ELSE said meanwhile.
func (h *DiffSessionHandler) reply(ctx context.Context, req reviewSessionRequest) (*types.DiffSession, error) {
	at, err := h.openReview(ctx)
	if err != nil {
		return nil, err
	}
	if err := bindings.ReplyReview(ctx, at, req.ID, req.Body); err != nil {
		return nil, err
	}
	return h.sessions.Get(h.root), nil
}

// openReview resolves the review both outward-facing ops need, or the reason there is none.
//
// The reason TRAVELS: "no provider wired", "no pull request for this branch" and "the host was
// unreachable" are different sentences for the reader even though none is their fault, and a
// bare "cannot publish" would leave them guessing which.
func (h *DiffSessionHandler) openReview(ctx context.Context) (types.ReviewTarget, error) {
	if h.origin == nil {
		return types.ReviewTarget{}, errors.New("this daemon has no workspace to publish from")
	}
	from := h.origin.ReviewOrigin(ctx)
	at := bindings.OpenReview(ctx, from.Branch, from.Remote)
	if !at.Open() {
		return types.ReviewTarget{}, errors.New(at.Reason)
	}
	return at, nil
}

func (h *DiffSessionHandler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodGet {
		sess := h.sessions.Get(h.root)
		if sess == nil {
			http.Error(w, "no review session attached; GET /api/v1/diff first", http.StatusConflict)
			return
		}
		writeJSON(w, sess)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req reviewSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
		return
	}

	var sess *types.DiffSession
	switch req.Op {
	case "cursor":
		sess = h.sessions.SetCursor(h.root, types.DiffCursor{Path: req.Path, Hunk: req.Hunk})
	case "viewed":
		var finished string
		sess, finished = h.sessions.MarkViewed(h.root, req.Digest, req.On)
		// Finishing a file in the console earns a read receipt, exactly as stepping its last
		// hunk in the terminal viewer does. One rule, two surfaces: the reader chooses where
		// to read and magus does not care which they picked.
		//
		// Only a mark arriving HERE mints one. This route is the human's - the MCP surface
		// has no way to write it, by design - and the persisted viewed set is an
		// unauthenticated file, so a session that merely LOOKS complete after a reload must
		// never produce a receipt on its own.
		h.mintReceipt(r.Context(), finished)
	case "comment":
		sess = h.sessions.AddComment(h.root, types.DiffComment{
			Path: req.Path, Hunk: req.Hunk, Line: req.Line, Body: req.Body, Anchor: req.Anchor,
		}, types.DiffAuthorHuman)
	case "publish":
		// The ONE op here that fails loudly. Every other write is bookkeeping the reader did
		// not ask about, so the handler answers with the session and lets a stale cursor sort
		// itself out. This one sends sentences to colleagues: reporting success it did not
		// have would leave a reader believing their review landed when it never left.
		var err error
		sess, err = h.publish(r.Context(), req)
		if err != nil {
			http.Error(w, "publish: "+err.Error(), http.StatusBadGateway)
			return
		}
	case "reply":
		// Checked HERE, so an incomplete request is a 400 and not a 502. "You sent nothing to
		// say" and "the host refused" are different failures, and answering the first as a bad
		// gateway sends the reader to look at their network for a mistake in their own call.
		if req.ID == "" || strings.TrimSpace(req.Body) == "" {
			http.Error(w, "reply: a reply needs a thread and something to say", http.StatusBadRequest)
			return
		}
		// Outward-facing, so it fails the same way publish does. ID names a THREAD here rather
		// than a local comment: the two id spaces never meet, because one belongs to the host
		// and the other to this session.
		var err error
		sess, err = h.reply(r.Context(), req)
		if err != nil {
			http.Error(w, "reply: "+err.Error(), http.StatusBadGateway)
			return
		}
	case "resolve":
		sess = h.sessions.ResolveComment(h.root, req.ID, req.On)
	case "answer":
		sess = h.sessions.AnswerSuggestion(h.root, req.ID, req.On)
	default:
		http.Error(w, "unknown op "+req.Op, http.StatusBadRequest)
		return
	}
	if sess == nil {
		// No session attached yet. 409 rather than 404: the ROUTE exists and the workspace is
		// fine, the client just has not read a review yet, and the fix is to fetch one.
		http.Error(w, "no review session attached; GET /api/v1/diff first", http.StatusConflict)
		return
	}
	writeJSON(w, sess)
}

// mintReceipt records that a person read path, at the content it holds right now.
//
// Best-effort and silent: this is a side effect of reading, and a reader who just finished a
// file should not meet an error about bookkeeping on their next keypress. A path that cannot
// be fingerprinted - deleted since the patch was read - records nothing rather than recording
// a receipt against no content.
func (h *DiffSessionHandler) mintReceipt(ctx context.Context, path string) {
	if path == "" || h.cacheDir == "" || h.root == "" {
		return
	}
	// The content as of when this changeset was tracked, not as of now: a receipt attests to
	// the bytes the reader saw, and in a paired review an agent may have edited the file
	// while they were reading it. Minting the current bytes would stamp somebody else's edit
	// as read and defeat the staleness it exists to detect.
	digest := h.sessions.ContentAt(h.root, path)
	if digest == "" {
		return
	}
	if err := review.Record(h.cacheDir, []review.Receipt{{Path: path, Digest: digest, At: time.Now()}}); err != nil {
		h.Log.DebugContext(ctx, "diff session: could not record a read receipt",
			slog.String("path", path), slog.String("error", err.Error()))
	}
}

// DiffReviewHandler serves GET /api/v1/diff/review: which review is open for this tree, and
// the comment threads already on it.
//
// Beside the session handler because they are two halves of one conversation, but a SEPARATE
// route because they cost different amounts: the session is local state and returns in
// microseconds, while this one crosses the network to a forge and can hang for as long as that
// forge feels like taking. Serving them together would hold the diff behind somebody else's
// outage.
//
// It never fails. No provider wired, no pull request, an unreachable host: all of them are a
// closed target with a reason, because the reader's options are identical in every case and a
// surface that rendered them as errors would be accusing them of something they did not do.
type DiffReviewHandler struct {
	handler.Base
	origin originSource
}

// NewDiffReviewHandler returns the review-lookup handler. origin may be nil, which reports no
// review rather than failing - a daemon with no workspace has no branch to look one up for.
func NewDiffReviewHandler(origin originSource, log *slog.Logger) *DiffReviewHandler {
	h := &DiffReviewHandler{origin: origin}
	h.Base = handler.New(h.serve, log)
	return h
}

// diffReviewResponse is the wire shape: the target, flattened, plus its threads.
//
// Threads is always an array, never null. A client rendering "what colleagues said" iterates
// it, and a null would make every caller write the same guard for a state that means exactly
// what an empty list means.
type diffReviewResponse struct {
	Number  int                  `json:"number"`
	Repo    string               `json:"repo,omitempty"`
	Reason  string               `json:"reason,omitempty"`
	Threads []types.ReviewThread `json:"threads"`
}

func (h *DiffReviewHandler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	at := h.lookup(r.Context())
	out := diffReviewResponse{
		Number:  at.Number,
		Repo:    at.Repo,
		Reason:  at.Reason,
		Threads: []types.ReviewThread{},
	}
	if at.Open() {
		threads, err := bindings.ReviewThreads(r.Context(), at)
		out.Threads = append(out.Threads, threads...)
		if err != nil {
			// The threads that DID decode still travel, and the reason rides beside them.
			// Answering 502 here would hide a readable conversation behind one malformed
			// remark, and dropping the remark silently would say a colleague said nothing.
			out.Reason = err.Error()
		}
	}
	writeJSON(w, out)
}

func (h *DiffReviewHandler) lookup(ctx context.Context) types.ReviewTarget {
	if h.origin == nil {
		return types.ReviewTarget{Reason: "no workspace"}
	}
	from := h.origin.ReviewOrigin(ctx)
	return bindings.OpenReview(ctx, from.Branch, from.Remote)
}
