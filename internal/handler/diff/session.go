// Package diff serves the review session's plain-JSON routes under /api/v1/diff.
//
// Separate from handler/status, which maps one thing (the live status report) onto
// StatusService's two RPCs. These routes ride no proto service at all, and every constructor
// here was named NewDiff* while living there, which is the package boundary announcing itself.
package diff

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/internal/interp/bindings"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// patchSource is the narrow consumer contract the diff handler needs from the console
// service: the working tree's uncommitted unified diff. It is satisfied by *console.Service;
// this package holds no concrete service, matching insightSource in insight.go.
type patchSource interface {
	WorkingDiff(ctx context.Context, paths []string) (string, error)
}

// diffSource is the annotation half, kept a SEPARATE interface from patchSource because the
// two routes have genuinely different costs and a caller should be able to mount the cheap
// one without the expensive one.
type diffSource interface {
	patchSource
	Diff(ctx context.Context, paths []string) (types.Diff, error)
}

// Handler serves GET /api/v1/diff: the changed files annotated with what the
// workspace knows: role (generated or not), owning project, changed-symbol reach, observed
// coverage, in the order magus recommends reading them.
//
// It is the differentiated half of the Diff app and a SECOND round trip on purpose.
// /api/v1/diff/patch returns the patch in milliseconds; this one loads the symbol shards and walks
// a reverse closure. Folding them together would hold the whole diff behind the slowest
// overlay for no reading benefit.
//
// The caller passes the paths it is actually reviewing, repeated as `path`. That is not an
// optimization: re-deriving the changed set here would race an edit made since the patch was
// read and annotate a file the reader cannot see.
type Handler struct {
	handler.Base
	src      diffSource
	sessions *changeset.Store
	root     string
}

// NewHandler returns the GET /api/v1/diff handler reading from src. sessions and root
// may be nil/empty, which serves a session-less review: the shape is identical, so a client
// needs no branch for a server that is not pairing.
func NewHandler(src diffSource, sessions *changeset.Store, root string, log *slog.Logger) *Handler {
	h := &Handler{src: src, sessions: sessions, root: root}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *Handler) serve(w http.ResponseWriter, r *http.Request) {
	if !handler.AllowGet(w, r) {
		return
	}
	paths := scopePaths(r)
	if len(paths) == 0 {
		// No paths is a caller BUG, not an empty review: annotating nothing would render as
		// "this change touches nothing", which is the most misleading answer available.
		handler.Refuse(w, r, rpcerr.Invalid("review requires at least one path parameter"))
		return
	}
	out, err := h.src.Diff(r.Context(), paths)
	if err != nil {
		if errors.Is(err, console.ErrNoWorkspace) {
			handler.Refuse(w, r, rpcerr.WorkspaceNotWired())
			return
		}
		h.Fail(w, r, "review", err)
		return
	}
	// Attaching here rather than on a separate route means a client that can read a review is
	// already paired: the console's first fetch is what makes the session an agent can find,
	// so pairing needs no setup step anyone has to remember.
	if h.sessions != nil && h.root != "" {
		// Best-effort: a session with an unknown snapshot id is worse than one with a correct
		// one, but far better than refusing to pair because a second git call failed.
		asOf := ""
		if patch, perr := h.src.WorkingDiff(r.Context(), nil); perr == nil {
			asOf = changeset.PatchDigest(patch)
			// The same read also gives the hunk-to-file mapping, which is the only thing
			// that lets a later viewed mark say it FINISHED a file rather than just landing
			// somewhere.
			//
			// Fingerprints each changed file HERE, at the content the reader is about to
			// look at, so a receipt minted later attests to what they saw. See
			// Store.ContentAt for why the file moving mid-session is the expected case.
			h.sessions.TrackHunks(h.root, changeset.ParseHunks(patch), func(p string) string {
				return review.DigestFile(filepath.Join(h.root, filepath.FromSlash(p)))
			})
		}
		handler.WriteJSON(w, r, h.sessions.Attach(h.root, out.Base, out, asOf))
		return
	}
	handler.WriteJSON(w, r, types.DiffReview{Base: out.Base, Diff: out, Cursor: types.DiffCursor{Hunk: -1}})
}

// PatchHandler serves GET /api/v1/diff/patch: the working tree's uncommitted changes as one unified
// patch, for the console's Diff app.
//
// The changeset ships PARSED, and the raw patch travels beside it for a caller that wants the
// interchange format itself. Parsing here rather than in the browser is what keeps one reader
// in the product; see internal/diff/parse.go for why a second one must not be written.
//
// Optional `path` query parameters scope the diff, repeated once per path. Absent, the whole
// repository is diffed. A service with no workspace yields 503, not 500, the same posture
// the insight route takes, because "no workspace yet" is a state the console renders rather
// than an error it reports.
type PatchHandler struct {
	handler.Base
	src patchSource
}

// ContextHandler serves bounded, snapshot-paired file context.
type ContextHandler struct {
	handler.Base
	root string
	src  patchSource
}

const maxContextFileBytes = 1 << 20

// NewContextHandler returns the bounded loopback context handler.
func NewContextHandler(root string, src patchSource, log *slog.Logger) *ContextHandler {
	h := &ContextHandler{root: root, src: src}
	h.Base = handler.New(h.serve, log)
	return h
}

type contextResponse struct {
	Path  string   `json:"path"`
	AsOf  string   `json:"as_of"`
	Start int      `json:"start"`
	Lines []string `json:"lines"`
}

func (h *ContextHandler) serve(w http.ResponseWriter, r *http.Request) {
	if !handler.AllowGet(w, r) {
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" || filepath.IsAbs(path) || path != filepath.Clean(path) || path == "." || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		handler.Refuse(w, r, rpcerr.Invalid("context requires a workspace-relative path"))
		return
	}
	start, _ := strconv.Atoi(r.URL.Query().Get("start"))
	end, _ := strconv.Atoi(r.URL.Query().Get("end"))
	if start < 1 || end < start {
		handler.Refuse(w, r, rpcerr.Invalid("context requires a valid line range"))
		return
	}
	radius, _ := strconv.Atoi(r.URL.Query().Get("radius"))
	if radius < 0 {
		radius = 0
	}
	if radius > 32 {
		radius = 32
	}
	asOf := strings.TrimSpace(r.URL.Query().Get("as_of"))
	if asOf == "" {
		handler.Refuse(w, r, rpcerr.Invalid("context requires the review snapshot identity"))
		return
	}
	patch, err := h.src.WorkingDiff(r.Context(), nil)
	if err != nil {
		if errors.Is(err, console.ErrNoWorkspace) {
			handler.Refuse(w, r, rpcerr.WorkspaceNotWired())
			return
		}
		h.Fail(w, r, "context snapshot", err)
		return
	}
	if changeset.PatchDigest(patch) != asOf {
		handler.Refuse(w, r, rpcerr.Conflict("review snapshot is stale; refresh the diff"))
		return
	}
	changed := false
	for _, file := range changeset.ParseHunks(patch) {
		if file.Path == path {
			changed = true
			break
		}
	}
	if !changed {
		handler.Refuse(w, r, rpcerr.NotFound("context path is not in the reviewed patch"))
		return
	}
	root, err := filepath.EvalSymlinks(h.root)
	if err != nil {
		// err names the root's path, so it stays out of the body.
		gone := rpcerr.WorkspaceNotWired()
		gone.Message = "the workspace root no longer resolves"
		handler.Refuse(w, r, gone)
		return
	}
	target, err := filepath.EvalSymlinks(filepath.Join(root, path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			handler.Refuse(w, r, rpcerr.NotFound("file is not present in the working tree"))
			return
		}
		h.Fail(w, r, "context", err)
		return
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		handler.Refuse(w, r, rpcerr.Invalid("context path escapes workspace"))
		return
	}
	info, err := os.Stat(target)
	if err != nil {
		h.Fail(w, r, "context", err)
		return
	}
	if !info.Mode().IsRegular() {
		handler.Refuse(w, r, rpcerr.Invalid("context requires a regular file"))
		return
	}
	if info.Size() > maxContextFileBytes {
		tooLarge := rpcerr.Invalid("context is unavailable for files larger than 1 MiB")
		tooLarge.HTTPStatus = http.StatusRequestEntityTooLarge
		handler.Refuse(w, r, tooLarge)
		return
	}
	body, err := os.ReadFile(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			handler.Refuse(w, r, rpcerr.NotFound("file is not present in the working tree"))
			return
		}
		h.Fail(w, r, "context", err)
		return
	}
	lines := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	from := max(1, start-radius)
	to := min(len(lines), end+radius)
	if from > len(lines) {
		handler.WriteJSON(w, r, contextResponse{Path: path, AsOf: asOf, Start: from, Lines: []string{}})
		return
	}
	handler.WriteJSON(w, r, contextResponse{Path: path, AsOf: asOf, Start: from, Lines: lines[from-1 : to]})
}

// NewPatchHandler returns the GET /api/v1/diff/patch handler reading from src.
func NewPatchHandler(src patchSource, log *slog.Logger) *PatchHandler {
	h := &PatchHandler{src: src}
	h.Base = handler.New(h.serve, log)
	return h
}

// diffResponse is the wire shape. Patch carries the whole unified body; Clean says the tree
// had nothing to review, which is DISTINCT from an empty patch the reader failed to parse;
// a console that cannot tell those apart renders "no changes" over a bug.
type diffResponse struct {
	// Files is the changeset already parsed. The console renders from this and does not read
	// Patch at all; see PatchHandler for why the server parses rather than the browser.
	Files []changeset.File `json:"files"`
	// Patch is the same changeset as raw text, kept for a caller that wants the interchange
	// format itself: a script piping it onward, or a reader diffing it against another tool's.
	Patch string `json:"patch"`
	// Digest identifies the changeset as a whole, so a client that joined later can tell a
	// current answer from a frozen one. Computed here for the same reason the hunk digests are:
	// the console would otherwise hash the patch itself to reach the same number.
	Digest string `json:"digest"`
	Clean  bool   `json:"clean"`
}

func (h *PatchHandler) serve(w http.ResponseWriter, r *http.Request) {
	if !handler.AllowGet(w, r) {
		return
	}
	patch, err := h.src.WorkingDiff(r.Context(), scopePaths(r))
	if err != nil {
		if errors.Is(err, console.ErrNoWorkspace) {
			handler.Refuse(w, r, rpcerr.WorkspaceNotWired())
			return
		}
		h.Fail(w, r, "diff", err)
		return
	}
	handler.WriteJSON(w, r, diffResponse{
		Files:  changeset.Parse(patch),
		Patch:  patch,
		Digest: changeset.PatchDigest(patch),
		Clean:  strings.TrimSpace(patch) == "",
	})
}

// scopePaths reads the repeated `path` query parameter, dropping empties so a stray `?path=`
// scopes to nothing rather than to the empty pathspec, which every backend reads as "the
// whole repository", the opposite of what the caller asked for.
func scopePaths(r *http.Request) []string {
	raw := r.URL.Query()["path"]
	paths := make([]string, 0, len(raw))
	for _, p := range raw {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// ReviewHandler serves the live paired review (a [types.DiffReview]) on the review route.
//
// Every write here is stamped DiffAuthorUnattributed with the request's origin: the OS
// account and the credential the bearer guard verified. The agent's half lives on the MCP
// tools and is stamped DiffAuthorAgent there. The stamp is decided by WHICH ROUTE the
// write arrived on and never by the payload, but the route does not prove a person: any
// process of the account can mint a console token, so the origin records which
// credential wrote the remark rather than claiming who held it.
//
// It is one route with an `op` rather than five, because these are all small mutations of one
// object and a client applies them from one place: a keypress handler. Five routes would be
// five fetch wrappers for no gain in clarity.
type ReviewHandler struct {
	handler.Base
	ReviewOptions
}

// ReviewOptions is what both review routes need from the server.
//
// A struct because the alternative was five positional arguments with Root and CacheDir
// adjacent and both string.
type ReviewOptions struct {
	Sessions *changeset.Store
	// Workspace answers where this tree's changes are discussed and reads the working patch.
	// Nil serves everything except the review.
	Workspace reviewSource
	Root      string
	// CacheDir is where read receipts live. Empty records none.
	CacheDir string
	// Telemetry records the review families; nil records none.
	Telemetry observability.Provider
}

// reviewSource is what the review routes read. The server answers it, never the client: a
// browser knows the paths it is reviewing and nothing about remotes, and one that supplied a
// remote could aim a review at another repository.
type reviewSource interface {
	ReviewOrigin(ctx context.Context) types.ReviewOrigin
	WorkingDiff(ctx context.Context, paths []string) (string, error)
}

// NewReviewHandler returns the paired-review handler.
func NewReviewHandler(opts ReviewOptions, log *slog.Logger) *ReviewHandler {
	h := &ReviewHandler{ReviewOptions: opts}
	h.Base = handler.New(h.serve, log)
	return h
}

// reviewSessionRequest is the wire shape. Op names the mutation; the rest are its arguments,
// and which ones matter depends on Op.
type reviewSessionRequest struct {
	// Op is one of: cursor, viewed, comment, discard, resolve, answer, publish, reply, seen,
	// reading.
	Op string `json:"op"`
	// cursor
	Path string `json:"path,omitempty"`
	Hunk int    `json:"hunk,omitempty"`
	// viewed. On is also reading's switch: true marks the review as being read now, false
	// clears the mark.
	Digest string `json:"digest,omitempty"`
	On     bool   `json:"on,omitempty"`
	// comment. No anchor field: the server captures it from the patch it tracked, so a client
	// cannot supply one and cannot get it wrong.
	Body string `json:"body,omitempty"`
	// publish: the summary heading the review. The branch and remote are NOT here: the
	// server resolves those itself, so a client cannot aim a review at another repository.
	Summary string `json:"summary,omitempty"`
	// Line is the position an inline comment anchors to on the new side. A hunk index cannot
	// serve: it means nothing outside the session that produced it.
	Line int `json:"line,omitempty"`
	// resolve / answer, and the review comment a reply answers. One field because they are the
	// same question (which one) and never asked together.
	ID string `json:"id,omitempty"`
	// Thread is, for reply, the thread to answer: the [types.ReviewComment].Root of any reply in
	// it, or the ID of its top-level comment. It wins over ID, so a client holding a reply can
	// name its thread without finding the top-level comment, and may leave ID empty. The wire
	// name stays "root", the name a reply's own Root field has.
	Thread string `json:"root,omitempty"`
	// Verdict is what the published review should SAY: "comment" (the default), "approve", or
	// "request_changes". It is a REQUEST, not a decision: the server resolves it against who
	// opened the review, and a self-review is always a comment however this is set.
	Verdict string `json:"verdict,omitempty"`
	// seen: the review threads the client has just put in front of the reader.
	//
	// The CLIENT says this, rather than the review lookup assuming it. Serving a response is not
	// the same as rendering one: an aborted fetch, a refresh mid-flight or a second tab would
	// otherwise consume the watermark and eat the "new" marks, and with them the notification,
	// which compares against the same watermark.
	IDs []string `json:"ids,omitempty"`
}

// publish sends the human's unsent drafts as one review.
//
// The batch is derived from the session, never named by the request, so no caller can widen it
// to an agent's draft or re-send one already sent.
//
// A draft with no line stays a draft. A provider anchors an inline comment to a line and drops
// one that has none, so including it would mark it published against a send that never
// happened, and publish only considers unpublished drafts, so it could never go again.
func (h *ReviewHandler) publish(ctx context.Context, req reviewSessionRequest) (*types.DiffReview, error) {
	sess := h.Sessions.Get(h.Root)
	if sess == nil {
		return nil, errors.New("no review session attached")
	}
	var drafts, unplaceable []types.DiffComment
	for _, c := range sess.Comments {
		if c.Author != types.DiffAuthorUnattributed || c.Published {
			continue
		}
		if c.Line == 0 {
			unplaceable = append(unplaceable, c)
			continue
		}
		drafts = append(drafts, c)
	}
	if len(drafts) == 0 {
		if len(unplaceable) > 0 {
			noun := "drafts have"
			if len(unplaceable) == 1 {
				noun = "draft has"
			}
			return nil, fmt.Errorf("%d %s no line to anchor to, so nothing can be sent",
				len(unplaceable), noun)
		}
		// Publishing twice, or with nothing drafted, is no mistake to report.
		return sess, nil
	}

	at, err := h.findReview(ctx)
	if err != nil {
		return nil, err
	}
	want := types.ReviewVerdict(req.Verdict)
	// Unchecked conversion on purpose: PermittedVerdict treats anything that is not one of the
	// two asserting words as remarks, so a client cannot spell its way into an approval.
	got, err := bindings.PublishReview(ctx, at, req.Summary, want, drafts)
	if err != nil {
		return nil, err
	}
	if got != want && want.Asserts() {
		// Said out loud rather than swallowed. The remarks DID go, so this is not an error, but
		// a person who asked to approve and was silently given a comment would believe they had
		// approved, which is the one outcome worse than refusing.
		h.Log.InfoContext(ctx, "review published as remarks: a review cannot approve a change its own credential opened",
			"review", at.ID, "asked", string(want), "published", string(got))
	}
	// The verdict that LANDED, not the one asked for, plus whether those two differ: a
	// downgrade is invisible in the forge's record and this is the only place it is known.
	// got is PermittedVerdict's answer, so it is one of the three declared verdicts and
	// never the caller's spelling.
	if h.Telemetry != nil {
		h.Telemetry.RecordReviewPublish(ctx, string(got), got != want)
	}
	// All or none: a review posts as one request, so reaching here means the host took it.
	for _, d := range drafts {
		sess = h.Sessions.MarkPublished(h.Root, d.ID)
	}
	return sess, nil
}

// reply answers one thread on the host's review. It writes no session state: the reply belongs
// to the host's record, and the client re-reads the review to see it.
func (h *ReviewHandler) reply(ctx context.Context, req reviewSessionRequest) (*types.DiffReview, error) {
	at, err := h.findReview(ctx)
	if err != nil {
		return nil, err
	}
	if err := bindings.ReplyReview(ctx, at, replyThread(ctx, at, req), req.Body); err != nil {
		return nil, err
	}
	// A reply needs a review, not an attached session, so a nil session here is not a failure.
	// Reporting one would 409 a reply that already reached a colleague, and the answer to a 409
	// is to send it again.
	if sess := h.Sessions.Get(h.Root); sess != nil {
		return sess, nil
	}
	return &types.DiffReview{Cursor: types.DiffCursor{Hunk: -1}}, nil
}

// replyThread is the thread id a reply goes to. Hosts attach a reply to a thread by its
// top-level comment, so answering a reply's own id would open nothing: the id the client named
// (its Thread, else its ID) is looked up on the review and resolved to its thread's, the way
// every other reader of the review does.
//
// A review that cannot be read, or that holds no such comment, sends the name as given. The
// host knows its own ids and refuses one it does not; this lookup only corrects the one mistake
// a client can make from what it was shown.
func replyThread(ctx context.Context, at types.ReviewTarget, req reviewSessionRequest) string {
	name := requestedThread(req)
	comments, _, _ := bindings.ReviewCommentsReached(ctx, at)
	if th, err := changeset.FindThread(comments, name); err == nil {
		return th.ID()
	}
	return name
}

// requestedThread is the thread a reply names before the review is consulted: its Thread when
// the client sent one, else the comment it addressed.
func requestedThread(req reviewSessionRequest) string {
	if req.Thread != "" {
		return req.Thread
	}
	return req.ID
}

// findReview resolves the review publish and reply both need, or the reason there is none.
// The reason travels, because "no provider wired" and "no pull request for this branch" send
// the reader to different places.
func (h *ReviewHandler) findReview(ctx context.Context) (types.ReviewTarget, error) {
	if h.Workspace == nil {
		return types.ReviewTarget{}, errors.New("this server has no workspace to publish from")
	}
	from := h.Workspace.ReviewOrigin(ctx)
	at := bindings.FindReview(ctx, from.Branch, from.Remote)
	if !at.Open() {
		if at.Reason == "" {
			// A provider may close a target without saying why. "publish: " with nothing after
			// the colon tells the reader less than nothing.
			return types.ReviewTarget{}, errors.New("no review is open for this branch")
		}
		return types.ReviewTarget{}, errors.New(at.Reason)
	}
	return at, nil
}

// readingResponse is what the reading op answers.
type readingResponse struct {
	// Reading is whether a mark is set after this request.
	Reading bool `json:"reading"`
	// Since is when the reading began, in unix milliseconds. Zero when Reading is false.
	Since int64 `json:"since,omitzero"`
	// Command is the one line the person may run to tell the forge, or empty when magus does not
	// know how to say it on this one. magus prints it and never runs it.
	Command string `json:"command,omitempty"`
}

// reading marks, or unmarks, the review as being read right now.
//
// The mark is local: it makes a merge that lands under the reader reportable (see the
// check-review job) and measurable. It is never sent to the forge by magus. The command in the
// response is for the person to run if they want colleagues to see it, so that the one sentence
// that leaves the machine is typed by a person, like every other.
//
// Marking a review that has already merged is refused rather than recorded: the next job tick
// would report a merge "under" a reader who started after it.
func (h *ReviewHandler) reading(w http.ResponseWriter, r *http.Request, req reviewSessionRequest) {
	ctx := r.Context()
	if !req.On {
		if err := h.Sessions.ClearReading(ctx); err != nil {
			h.Log.WarnContext(ctx, "diff session: could not clear the reading mark", slog.String("error", err.Error()))
			handler.Refuse(w, r, rpcerr.Internal("clearing the reading mark"))
			return
		}
		handler.WriteJSON(w, r, readingResponse{})
		return
	}
	at, err := h.findReview(ctx)
	if err != nil {
		handler.Refuse(w, r, rpcerr.ReviewHostFailed("reading: "+err.Error()))
		return
	}
	if at.Merged() {
		handler.Refuse(w, r, rpcerr.Conflict("review "+at.ID+" has already merged, so there is nothing left to hold"))
		return
	}
	mark, err := h.Sessions.SetReading(ctx, at, time.Now())
	if err != nil {
		h.Log.WarnContext(ctx, "diff session: could not record the reading mark", slog.String("error", err.Error()))
		handler.Refuse(w, r, rpcerr.Internal("recording the reading mark"))
		return
	}
	handler.WriteJSON(w, r, readingResponse{
		Reading: true,
		Since:   mark.Since,
		Command: readingCommand(at, h.Workspace.ReviewOrigin(ctx).Remote),
	})
}

var (
	// A GitHub owner/name, the only repo shape the command below is built for.
	githubRepo = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	// A login, which for the command below must never carry a quote or a space.
	githubLogin  = regexp.MustCompile(`^[A-Za-z0-9_.-]+(\[bot\])?$`)
	reviewNumber = regexp.MustCompile(`^[0-9]+$`)
)

// readingCommand is the gh command that tells a GitHub pull request's participants the reader is
// on it, or "" for any other forge: only GitHub's shape is known, and a guessed command for a
// forge magus has not seen would be one the person had to debug before running.
//
// Every part is checked against the shape GitHub gives it before it reaches a shell line the
// person will paste, because the repo and the login arrive from a provider and a remote.
func readingCommand(at types.ReviewTarget, remote string) string {
	if remoteHost(remote) != "github.com" ||
		!reviewNumber.MatchString(at.ID) || !githubRepo.MatchString(at.Repo) {
		return ""
	}
	who := "A reviewer"
	if githubLogin.MatchString(at.Viewer) {
		who = at.Viewer
	}
	return "gh pr comment " + at.ID + " --repo " + at.Repo + " --body '" + who + " is reading this now'"
}

func (h *ReviewHandler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodGet {
		sess := h.Sessions.Get(h.Root)
		if sess == nil {
			handler.Refuse(w, r, rpcerr.Conflict(noSessionAttached))
			return
		}
		handler.WriteJSON(w, r, sess)
		return
	}
	if r.Method != http.MethodPost {
		handler.RefuseMethod(w, r, http.MethodGet, http.MethodPost)
		return
	}
	handler.LimitRequestBody(w, r)
	var req reviewSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		handler.Refuse(w, r, rpcerr.Invalid("the request body is not the JSON a review op takes: "+err.Error()))
		return
	}

	var sess *types.DiffReview
	switch req.Op {
	case "reading":
		// Answers with its own shape rather than the session, and needs no attached session: the
		// mark belongs to the review on the host, which a reader may hold open before any hunk is
		// fetched.
		h.reading(w, r, req)
		return
	case "cursor":
		sess = h.Sessions.SetCursor(h.Root, types.DiffCursor{Path: req.Path, Hunk: req.Hunk})
	case "viewed":
		var finished string
		sess, finished = h.Sessions.MarkViewed(h.Root, req.Digest, req.On)
		// Finishing a file in the console earns a read receipt, exactly as stepping its last
		// hunk in the terminal viewer does. One rule, two clients: the reader chooses where
		// to read and magus does not care which they picked.
		//
		// Only a mark arriving HERE mints one. This route is the review route, and the MCP
		// tools have no way to write it; see Store.MarkViewed for why a restored session
		// must not mint on its own.
		h.mintReceipt(r.Context(), finished)
	case "comment":
		// The anchor is CAPTURED here rather than accepted from the request. The server holds the
		// patch this session tracked, so it can record what the reader was actually shown; a
		// client-supplied anchor would be three implementations of one thing, each able to be
		// wrong in its own way. The wire field is gone for the same reason.
		sess = h.Sessions.AddComment(h.Root, types.DiffComment{
			Path: req.Path, Hunk: req.Hunk, Line: req.Line, Body: req.Body,
			Anchor: h.Sessions.Anchor(h.Root, req.Path, req.Line),
			Origin: trail.StampOrigin(r.Context(), types.Origin{}),
		}, types.DiffAuthorUnattributed)
		if h.Telemetry != nil {
			h.Telemetry.RecordReviewRemark(r.Context(), string(types.DiffAuthorUnattributed))
		}
	case "seen":
		// The reader's claim that these threads were put in front of them, which is the ONLY
		// thing that advances the watermark. It arrives on this route because it is the human's
		// half of the session: an agent reaching the session over MCP cannot make it, exactly
		// as it cannot mark a hunk read.
		sess = h.Sessions.MarkThreadsSeen(h.Root, req.IDs)
	case "publish":
		// publish and reply fail loudly; every other op is bookkeeping nobody asked about.
		// These two put sentences in front of colleagues, and a reader told a send succeeded
		// when it did not will never find out.
		var err error
		sess, err = h.publish(r.Context(), req)
		if err != nil {
			handler.Refuse(w, r, rpcerr.ReviewHostFailed("publish: "+err.Error()))
			return
		}
	case "reply":
		// 400 rather than 502: an incomplete request is the caller's mistake, and reporting it
		// as a bad gateway sends them to look at their network.
		if requestedThread(req) == "" || strings.TrimSpace(req.Body) == "" {
			handler.Refuse(w, r, rpcerr.Invalid("reply needs a thread and something to say"))
			return
		}
		// ID and Thread are HOST comment ids here, not local comment ids.
		var err error
		sess, err = h.reply(r.Context(), req)
		if err != nil {
			handler.Refuse(w, r, rpcerr.ReviewHostFailed("reply: "+err.Error()))
			return
		}
	case "discard":
		// Local, and the only op that removes anything. It refuses a published remark in the
		// store, so backing out of a draft can never be confused with unsaying something a
		// colleague has already read.
		sess = h.Sessions.DiscardDraft(h.Root, req.ID)
	case "resolve":
		sess = h.Sessions.ResolveComment(h.Root, req.ID, req.On)
	case "answer":
		sess = h.Sessions.AnswerSuggestion(h.Root, req.ID, req.On)
	default:
		handler.Refuse(w, r, rpcerr.Invalid("unknown op "+req.Op+" (one of: cursor, viewed, comment, seen, reading, publish, reply, discard, resolve, answer)"))
		return
	}
	if sess == nil {
		// 409 rather than 404: the ROUTE exists and the workspace is fine, the client just
		// has not read a review yet, and the fix is to fetch one.
		handler.Refuse(w, r, rpcerr.Conflict(noSessionAttached))
		return
	}
	handler.WriteJSON(w, r, sess)
}

const noSessionAttached = "no review session attached; GET /api/v1/diff first"

// mintReceipt records that a person read path, at the content it holds right now.
//
// Best-effort and silent: this is a side effect of reading, and a reader who just finished a
// file should not meet an error about bookkeeping on their next keypress. A path that cannot
// be fingerprinted (deleted since the patch was read) records nothing rather than recording
// a receipt against no content.
func (h *ReviewHandler) mintReceipt(ctx context.Context, path string) {
	if path == "" || h.CacheDir == "" || h.Root == "" {
		return
	}
	// The content as of when this changeset was tracked, not as of now: a receipt attests to
	// the bytes the reader saw, and in a paired review an agent may have edited the file
	// while they were reading it. Minting the current bytes would stamp somebody else's edit
	// as read and defeat the staleness it exists to detect.
	digest := h.Sessions.ContentAt(h.Root, path)
	if digest == "" {
		return
	}
	if err := review.Record(h.CacheDir, []review.Receipt{{Path: path, Digest: digest, At: time.Now()}}); err != nil {
		h.Log.DebugContext(ctx, "diff session: could not record a read receipt",
			slog.String("path", path), slog.String("error", err.Error()))
	}
}

// ReviewLookupHandler serves GET /api/v1/diff/review: which review is open for this tree,
// and the comment threads already on it.
//
// Beside [ReviewHandler] because they are two halves of one conversation, but a SEPARATE
// route because they cost different amounts: the session is local state and returns in
// microseconds, while this one crosses the network to a forge and can hang for as long as that
// forge feels like taking. Serving them together would hold the diff behind somebody else's
// outage.
//
// It never fails. No provider wired, no pull request, an unreachable host: all of them are a
// closed target with a reason, because the reader's options are identical in every case and a
// client that rendered them as errors would be accusing them of something they did not do.
type ReviewLookupHandler struct {
	handler.Base
	workspace reviewSource
	// Sessions and Root are OPTIONAL, set by the server wiring after construction: with them the
	// handler can say which threads the reader has not seen before, and without them it serves
	// the conversation unmarked. A caller that has no session store is not a caller with an
	// empty one, so the marking is skipped rather than every thread being called new.
	Sessions *changeset.Store
	Root     string
}

// NewReviewLookupHandler returns the review-lookup handler. A nil workspace reports no
// review, which is what a server with no workspace has.
func NewReviewLookupHandler(workspace reviewSource, log *slog.Logger) *ReviewLookupHandler {
	h := &ReviewLookupHandler{workspace: workspace}
	h.Base = handler.New(h.serve, log)
	return h
}

// place resolves each comment onto the hunk holding its line, so the terminal viewer and the
// console read one answer instead of computing it twice. An unreadable patch leaves them at -1,
// which renders against the file rather than against the wrong hunk.
func (h *ReviewLookupHandler) place(ctx context.Context, comments []types.ReviewComment) []types.ReviewComment {
	if len(comments) == 0 {
		return comments
	}
	patch, err := h.workspace.WorkingDiff(ctx, nil)
	if err != nil {
		return comments
	}
	return changeset.PlaceThreads(changeset.ParseHunks(patch), comments)
}

// diffReviewResponse is the wire shape: the target, flattened, plus the review's comments.
//
// Comments is always an array, never null, and travels as "threads" for the clients that already
// read it. A client rendering "what colleagues said" iterates it, and a null would make every
// caller write the same guard for a state that means exactly what an empty list means.
type diffReviewResponse struct {
	ID   string `json:"id"`
	Repo string `json:"repo,omitempty"`
	// Host is where publishing would send to, named so a client can say it out loud before
	// anything leaves. An Enterprise appliance and github.com are the same feature and very
	// different destinations, and the reader is the only one who can tell whether the one on
	// screen is the one they meant.
	Host string `json:"host,omitempty"`
	// State is what the host says became of the review: "open", "merged" or "closed". Empty when
	// the provider does not answer, which reads as open.
	State  string `json:"state,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Verdicts are the verdicts this reviewer may publish, and VerdictLimit says why when the
	// set is only remarks.
	//
	// The server sends the ANSWER rather than the author and viewer names, so a client renders
	// the choices magus allows and has no rule of its own to get wrong. Always populated, so an
	// absent field is a magus too old to have an opinion rather than a review nobody may remark
	// on.
	Verdicts     []types.ReviewVerdict `json:"verdicts"`
	VerdictLimit string                `json:"verdict_limit,omitempty"`
	Comments     []types.ReviewComment `json:"threads"`
}

// remoteHost reduces a git remote URL to the host a reader would recognize. Empty when it is
// not a URL this understands; a client then names the repo alone rather than guessing.
func remoteHost(remote string) string {
	s := remote
	for _, prefix := range []string{"https://", "http://", "ssh://"} {
		s = strings.TrimPrefix(s, prefix)
	}
	if at := strings.Index(s, "@"); at >= 0 {
		s = s[at+1:]
	}
	s = strings.TrimSuffix(s, ".git")
	if cut := strings.IndexAny(s, ":/"); cut >= 0 {
		s = s[:cut]
	}
	if !strings.Contains(s, ".") {
		return ""
	}
	return s
}

func (h *ReviewLookupHandler) serve(w http.ResponseWriter, r *http.Request) {
	if !handler.AllowGet(w, r) {
		return
	}
	at := h.lookup(r.Context())
	out := diffReviewResponse{
		ID:           at.ID,
		Repo:         at.Repo,
		State:        at.State,
		Reason:       at.Reason,
		Verdicts:     at.AllowedVerdicts(),
		VerdictLimit: at.VerdictLimit(),
		Comments:     []types.ReviewComment{},
	}
	if at.Open() && h.workspace != nil {
		out.Host = remoteHost(h.workspace.ReviewOrigin(r.Context()).Remote)
	}
	if at.Open() {
		comments, err := bindings.ReviewComments(r.Context(), at)
		out.Comments = append(out.Comments, h.place(r.Context(), comments)...)
		h.markNew(out.Comments)
		if err != nil {
			// The comments that DID decode still travel, and the reason rides beside them.
			// Answering 502 here would hide a readable review behind one malformed remark, and
			// dropping the remark silently would say a colleague said nothing.
			out.Reason = err.Error()
		}
	}
	handler.WriteJSON(w, r, out)
}

// markNew flags the comments the reader has not had on screen before. It READS the watermark and
// never moves it.
//
// Serving a response is not rendering one. Advancing here meant an aborted fetch, a refresh
// mid-flight, or a second tab silently consumed the marks, and the notification with them, since
// the job that raises it compares against this same watermark. The client says when it has shown
// them, through the session's `seen` op; until it does, the same comments keep arriving marked.
func (h *ReviewLookupHandler) markNew(comments []types.ReviewComment) {
	if h.Sessions == nil || len(comments) == 0 {
		return
	}
	sess := h.Sessions.Get(h.Root)
	if sess == nil {
		return
	}
	unseen := sess.UnseenThreads(comments)
	if len(unseen) == 0 {
		return
	}
	fresh := make(map[string]struct{}, len(unseen))
	for _, id := range unseen {
		fresh[id] = struct{}{}
	}
	for i := range comments {
		if _, ok := fresh[comments[i].ID]; ok {
			comments[i].New = true
		}
	}
}

func (h *ReviewLookupHandler) lookup(ctx context.Context) types.ReviewTarget {
	if h.workspace == nil {
		return types.ReviewTarget{Reason: "no workspace"}
	}
	from := h.workspace.ReviewOrigin(ctx)
	return bindings.FindReview(ctx, from.Branch, from.Remote)
}

// branchSource is the workspace half a branch lookup needs: what other lines of work are
// changing. Narrow on purpose, so a test can answer it without a repository.
type branchSource interface {
	BranchChanges(ctx context.Context, limit int) ([]types.BranchChange, error)
}

// BranchesHandler serves GET /api/v1/diff/branches: the other branches changing the files
// this changeset changes, so a reader learns about a collision before the merge does.
//
// Its own route rather than a field on the changeset, for the reason the review lookup has one:
// it costs a fork per branch, and the patch has to paint before anything that forks is allowed
// to hold it up. This ARRIVES, like the conversation does.
//
// It reads what has already been fetched and never fetches. The answer is therefore as fresh as
// the reader's last fetch and no fresher, which the client says out loud rather than implying
// it is live.
type BranchesHandler struct {
	handler.Base
	workspace branchSource
}

// branchLimit caps how many branches are examined, and so how many forks one request costs.
// Production `magus affected ci` spends about 31 forks in total, so a bound here is not
// decoration: an unbounded version would make a diff route the most expensive thing in the
// server on a repository with a hundred stale branches.
const branchLimit = 20

// NewBranchesHandler returns the branch-overlap handler. A nil workspace reports none, which
// is what a server with no workspace has.
func NewBranchesHandler(workspace branchSource, log *slog.Logger) *BranchesHandler {
	h := &BranchesHandler{workspace: workspace}
	h.Base = handler.New(h.serve, log)
	return h
}

// diffBranchesResponse is the wire shape. Branches is always an array, never null: a client
// iterates it, and a null would make every caller guard a state that means what empty means.
type diffBranchesResponse struct {
	Branches []types.BranchChange `json:"branches"`
	// Unsupported names the backend that cannot answer, empty when one did.
	//
	// It exists so an empty list is never ambiguous. "No branch competes" is reassurance; "this
	// backend has not implemented the lookup" is a gap in magus. Rendering both as silence tells
	// the reader the first when the truth is the second, and the whole point of the marker is
	// that it can be trusted.
	Unsupported string `json:"unsupported,omitempty"`
}

func (h *BranchesHandler) serve(w http.ResponseWriter, r *http.Request) {
	if !handler.AllowGet(w, r) {
		return
	}
	out := diffBranchesResponse{Branches: []types.BranchChange{}}
	if h.workspace != nil {
		got, err := h.workspace.BranchChanges(r.Context(), branchLimit)
		switch {
		case errors.Is(err, types.ErrVCSUnsupported):
			// 200 with the gap named, not an error status: the reader did nothing wrong and
			// their diff must still open. What they are owed is the DIFFERENCE between "nobody
			// else is touching this" and "magus cannot tell you on this backend yet".
			out.Unsupported = err.Error()
		case err != nil:
			out.Unsupported = "branch lookup failed: " + err.Error()
		default:
			out.Branches = append(out.Branches, got...)
		}
	}
	handler.WriteJSON(w, r, out)
}
