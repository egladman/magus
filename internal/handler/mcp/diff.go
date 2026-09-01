package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// diffTool is the AGENT's half of a paired review: read the shared session, say something
// about a hunk, and ask for the human's attention.
//
// It reads and writes the same *changeset.Store the console route does, which is the whole point
// - three transports over one object, so an agent can see where the person is looking and be
// useful about it instead of narrating blindly.
//
// TWO THINGS IT DELIBERATELY CANNOT DO, and the omissions are the design:
//
//   - It cannot move the cursor. `suggest` enqueues a proposal the human accepts with one
//     key; nothing here reaches the viewport. An agent that could scroll the reader's screen
//     would make them stop trusting their own place in the diff, and a lost place cannot be
//     restored because it was in their head. This is the review reading of the guard's rule:
//     deny what cannot be undone, explain everything else.
//   - It cannot claim to be the human. Author is stamped from the transport - this file
//     always stamps agent - so a body that says otherwise is ignored. Same reasoning the
//     notes store uses: a self-attested author is forgeable by whatever wrote the file.
//
// It also cannot mark a hunk viewed, for a quieter reason: "read" is a claim only the reader
// can make, and an agent ticking it off would erase the human's own account of what they have
// actually looked at.
type diffTool struct {
	sessions *changeset.Store
	root     string
	// src recomputes the changeset. Without it the tool can only replay whatever a browser
	// last attached, which is how an agent came to comment on a file that had no uncommitted
	// changes, in a tree the CLI reported clean, with nothing objecting.
	src workspaceSource
}

// reviewLookupTimeout bounds the two forge calls op=state makes. An agent asked for the
// changeset; it must not wait on a stranger's outage to get one.
const reviewLookupTimeout = 5 * time.Second

// workspaceSource is what this tool reads from the workspace, satisfied by *console.Service.
// Named for the workspace rather than the diff because it reads three unrelated facts, and
// because internal/handler/status has its own diffSource meaning something narrower.
type workspaceSource interface {
	Diff(ctx context.Context, paths []string) (types.Diff, error)
	WorkingDiff(ctx context.Context, paths []string) (string, error)
	// ReviewOrigin says where this tree's changes are discussed, so the threads on that review
	// can be read.
	ReviewOrigin(ctx context.Context) types.ReviewOrigin
}

// diffState is what op=state returns: the session, plus the change it describes.
//
// The session is EMBEDDED, so every field it carries sits at the top level of the response.
// The fields beside it are what make the rest of the surface usable: comment and suggest ask
// an agent for a 0-based hunk index, so a response that showed none would leave the coordinate
// to be guessed with nothing checking the guess. Hunks also make Viewed joinable, which is
// what its own description promises ("it tells you what they have already seen, so you can
// skip it").
type diffState struct {
	*types.DiffSession
	// Patch is the unified diff the hunks below index into.
	Patch string `json:"patch"`
	// Hunks are the addressable coordinates, with the same content digests Viewed holds.
	Hunks []changeset.FileHunks `json:"hunks"`
	// Recomputed reports that the tree had moved since the session was attached and this
	// answer is freshly computed rather than replayed.
	Recomputed bool `json:"recomputed,omitempty"`
	// Threads are the remarks already on the host's review, each placed on the hunk holding
	// its line (or -1). An agent pairing on a change should know what a reviewer has already
	// asked for - otherwise it re-raises a point somebody settled yesterday, or works on
	// something the review has moved past.
	//
	// READ-ONLY here, deliberately: an agent may draft a comment into the session, which a
	// person then sends, and nothing on this surface can put words on a review under the
	// person's name.
	Threads []types.ReviewThread `json:"threads,omitempty"`
}

// diffSummary is op=state's projection=summary shape: the session's identity plus counts,
// with none of the heavy bodies. It answers "has anything happened here" at a fraction of the
// cost of the full session, whose annotated changeset alone can dwarf everything else in it.
type diffSummary struct {
	ID         string           `json:"id"`
	Base       string           `json:"base"`
	AsOf       string           `json:"as_of,omitempty"`
	Recomputed bool             `json:"recomputed,omitempty"`
	Cursor     types.DiffCursor `json:"cursor"`
	Counts     diffCounts       `json:"counts"`
}

// diffCounts is how many of each thing diffSummary elides.
type diffCounts struct {
	Files       int `json:"files"`
	Hunks       int `json:"hunks"`
	Comments    int `json:"comments"`
	Suggestions int `json:"suggestions"`
	Viewed      int `json:"viewed"`
}

// diffConversation is op=state's projection=conversation shape: where the human is and what
// has been said about the change, without the changeset body that made those coordinates
// checkable in the first place - a client asking for this shape already has the change and
// wants the talk about it.
type diffConversation struct {
	ID          string                 `json:"id"`
	Base        string                 `json:"base"`
	AsOf        string                 `json:"as_of,omitempty"`
	Cursor      types.DiffCursor       `json:"cursor"`
	Viewed      []string               `json:"viewed,omitempty"`
	Comments    []types.DiffComment    `json:"comments,omitempty"`
	Suggestions []types.DiffSuggestion `json:"suggestions,omitempty"`
	// The conversation is not only the local half. A projection called "conversation" that
	// omitted what a reviewer said would be the surface's worst possible lie.
	Threads []types.ReviewThread `json:"threads,omitempty"`
}

// diffPatch is op=state's projection=patch shape: the unified diff and its addressable hunks,
// without the annotated changeset or the conversation.
type diffPatch struct {
	ID         string                `json:"id"`
	Base       string                `json:"base"`
	AsOf       string                `json:"as_of,omitempty"`
	Recomputed bool                  `json:"recomputed,omitempty"`
	Patch      string                `json:"patch"`
	Hunks      []changeset.FileHunks `json:"hunks"`
}

// projectDiffState narrows st to the fields the named projection asks for. The empty string
// and "full" both return st unchanged: op=state predates this parameter, and a caller that
// omits it must see exactly what it always has.
//
// It shapes op=state only. comment, suggest, and resolve keep returning the full session no
// matter what this parameter is set to - each already answers with just what it changed, so
// there is no heavy body to elide, and a second response shape would be a second contract to
// keep in sync for no reader's benefit.
func projectDiffState(st diffState, projection string) (any, error) {
	switch projection {
	case "", "full":
		return st, nil
	case "summary":
		return diffSummary{
			ID:         st.ID,
			Base:       st.Base,
			AsOf:       st.AsOf,
			Recomputed: st.Recomputed,
			Cursor:     st.Cursor,
			Counts: diffCounts{
				Files:       len(st.Diff.Files),
				Hunks:       totalHunks(st.Hunks),
				Comments:    len(st.Comments),
				Suggestions: len(st.Suggestions),
				Viewed:      len(st.Viewed),
			},
		}, nil
	case "conversation":
		return diffConversation{
			ID:          st.ID,
			Base:        st.Base,
			AsOf:        st.AsOf,
			Cursor:      st.Cursor,
			Viewed:      st.Viewed,
			Comments:    st.Comments,
			Suggestions: st.Suggestions,
			Threads:     st.Threads,
		}, nil
	case "patch":
		return diffPatch{
			ID:         st.ID,
			Base:       st.Base,
			AsOf:       st.AsOf,
			Recomputed: st.Recomputed,
			Patch:      st.Patch,
			Hunks:      st.Hunks,
		}, nil
	default:
		return nil, fmt.Errorf("mcp: unknown projection %q (use full, summary, conversation, or patch)", projection)
	}
}

// totalHunks sums the per-file hunk counts in a parsed patch, for the summary projection's
// "hunks" count - st.Hunks is one entry per FILE, each carrying its own hunk slice.
func totalHunks(files []changeset.FileHunks) int {
	n := 0
	for _, f := range files {
		n += len(f.Hunks)
	}
	return n
}

func (t *diffTool) Name() string { return hint.ToolDiff.String() }

func (t *diffTool) Invoke(ctx context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	if t.sessions == nil || t.root == "" {
		return spells.InvokeResponse{}, errors.New("mcp: review sessions are unavailable (no workspace)")
	}
	op := strings.TrimSpace(paramString(req.Params, "op", "state"))

	// Every op but `state` needs an attached session, and attaching is the HUMAN's act: the
	// console fetching a review is what creates one. An agent that could attach would be
	// starting a review nobody asked for and then talking into it.
	sess := t.sessions.Get(t.root)
	if sess == nil {
		return spells.InvokeResponse{}, errors.New(
			"mcp: no diff session is open. A session attaches when the console's Diff surface " +
				"is opened (GET /api/v1/diff); plain `magus diff` does not attach one. This tool " +
				"joins the session the console started")
	}

	switch op {
	case "state":
		// The whole session: the annotated changeset, where the human is, what they have read,
		// and the conversation so far - recomputed first when the tree has moved underneath it.
		projection := strings.TrimSpace(paramString(req.Params, "projection", "full"))
		st, serr := t.state(ctx, sess, wantsThreads(projection))
		if serr != nil {
			return spells.InvokeResponse{}, serr
		}
		data, perr := projectDiffState(st, projection)
		if perr != nil {
			return spells.InvokeResponse{}, perr
		}
		return spells.InvokeResponse{Data: data}, nil

	case "comment":
		body := strings.TrimSpace(paramString(req.Params, "body", ""))
		path := strings.TrimSpace(paramString(req.Params, "path", ""))
		if body == "" || path == "" {
			return spells.InvokeResponse{}, errors.New("mcp: comment needs path and body")
		}
		// agent_name is recorded here as well as on suggest, because the tool documents it
		// without an op qualifier. Reading it only on suggest would have a comment silently
		// drop it, leaving two agents in one session byte-identical in attribution with
		// neither the human nor the other agent able to tell them apart. Attribution only:
		// nothing branches on it, and it can never override the transport-stamped author below.
		hunk := int(paramFloat(req.Params, "hunk", -1))
		if verr := t.validateAnchor(ctx, path, hunk); verr != nil {
			return spells.InvokeResponse{}, verr
		}
		out := t.sessions.AddComment(t.root, types.DiffComment{
			Path:      path,
			Hunk:      hunk,
			Body:      body,
			AgentName: strings.TrimSpace(paramString(req.Params, "agent_name", "")),
		}, types.DiffAuthorAgent)
		// The author here is transport-stamped, never read off the payload, which is what makes
		// it safe as an attribute: it says which DOOR the remark came through, and the agent's
		// own agent_name label - unbounded, and a claim - stays out of the metric.
		if p := observability.FromContext(ctx); p != nil {
			p.RecordReviewRemark(ctx, string(types.DiffAuthorAgent))
		}
		return spells.InvokeResponse{Data: out}, nil

	case "suggest":
		path := strings.TrimSpace(paramString(req.Params, "path", ""))
		reason := strings.TrimSpace(paramString(req.Params, "reason", ""))
		if path == "" || reason == "" {
			// The reason is required because the suggestion is an INTERRUPTION, and one that
			// cannot say why it earned the reader's attention should not have been made.
			return spells.InvokeResponse{}, errors.New("mcp: suggest needs path and reason")
		}
		hunk := int(paramFloat(req.Params, "hunk", -1))
		if verr := t.validateAnchor(ctx, path, hunk); verr != nil {
			return spells.InvokeResponse{}, verr
		}
		out := t.sessions.Suggest(t.root, types.DiffSuggestion{
			Path:      path,
			Hunk:      hunk,
			Reason:    reason,
			AgentName: strings.TrimSpace(paramString(req.Params, "agent_name", "")),
		})
		return spells.InvokeResponse{Data: out}, nil

	case "resolve":
		id := strings.TrimSpace(paramString(req.Params, "id", ""))
		if id == "" {
			return spells.InvokeResponse{}, errors.New("mcp: resolve needs id")
		}
		return spells.InvokeResponse{Data: t.sessions.ResolveComment(t.root, id, true)}, nil

	default:
		return spells.InvokeResponse{}, errors.New(
			"mcp: unknown op " + op + " (one of: state, comment, suggest, resolve)")
	}
}

// state returns the session with its changeset, recomputing first when the working tree has
// moved since the session was attached.
//
// Recomputing rather than reporting staleness and leaving it there: an agent cannot see the
// tree, so "this may be stale" is advice it has no way to act on, and the failure it prevents
// is the agent confidently describing a changeset that no longer exists.
// wantsThreads reports whether a projection carries the review's threads. Only these two do,
// and reading them costs a forge round trip - which a caller asking for counts or the patch
// should not wait on.
func wantsThreads(projection string) bool {
	return projection == "" || projection == "full" || projection == "conversation"
}

func (t *diffTool) state(ctx context.Context, sess *types.DiffSession, withThreads bool) (diffState, error) {
	if t.src == nil {
		// No recompute source: serve what is held rather than nothing, and say the change
		// itself is unavailable rather than implying there is none.
		return diffState{DiffSession: sess}, nil
	}
	patch, err := t.src.WorkingDiff(ctx, nil)
	if err != nil {
		return diffState{}, err
	}
	st := diffState{DiffSession: sess, Patch: patch, Hunks: changeset.ParseHunks(patch)}
	if now := changeset.PatchDigest(patch); now != sess.AsOf {
		rev, rerr := t.src.Diff(ctx, changedPaths(st.Hunks))
		if rerr != nil {
			return diffState{}, rerr
		}
		st.DiffSession = t.sessions.Attach(t.root, rev.Base, rev, now)
		st.Recomputed = true
	}
	if withThreads {
		st.Threads = t.reviewThreads(ctx, st.Hunks)
	}
	return st, nil
}

// reviewThreads reads what colleagues have already said, placed onto this changeset's hunks.
//
// Silent on failure and bounded by its own deadline. The changeset is what the agent asked
// for, and holding it behind somebody else's forge is the reason the console gave this a
// separate route.
func (t *diffTool) reviewThreads(ctx context.Context, hunks []changeset.FileHunks) []types.ReviewThread {
	ctx, cancel := context.WithTimeout(ctx, reviewLookupTimeout)
	defer cancel()
	from := t.src.ReviewOrigin(ctx)
	at := bindings.FindReview(ctx, from.Branch, from.Remote)
	if !at.Open() {
		return nil
	}
	// The error is dropped and the threads are not: ReviewThreads returns everything it could
	// read alongside it, and a malformed remark is no reason to hide the rest of a conversation
	// from the agent working on it.
	threads, _ := bindings.ReviewThreads(ctx, at)
	return changeset.PlaceThreads(hunks, threads)
}

// validateAnchor refuses a coordinate the changeset does not contain.
//
// Without it the surface enforces only the argument it can check locally (a suggestion's
// reason) and silently accepts the one that needs the changeset, so comments land at
// plausible-looking indices nothing verified, on files that are sometimes not in the change at
// all. A refusal an agent can read and correct is worth more than a stored guess.
//
// A file-level anchor (hunk < 0) is always valid: it means "this file", which needs no index.
func (t *diffTool) validateAnchor(ctx context.Context, path string, hunk int) error {
	if t.src == nil {
		return nil // nothing to check against; see state
	}
	patch, err := t.src.WorkingDiff(ctx, nil)
	if err != nil {
		return err
	}
	counts := changeset.HunkCounts(patch)
	n, ok := counts[path]
	if !ok {
		return errors.New("mcp: " + path + " has no changes in this diff; read op=state for the paths that do")
	}
	if hunk >= n {
		return fmt.Errorf("mcp: %s has %d hunk(s), so hunk %d does not exist (0-based; omit hunk to address the file)", path, n, hunk)
	}
	return nil
}

// changedPaths lists the files a parsed patch touches, in patch order.
func changedPaths(files []changeset.FileHunks) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if f.Path != "" {
			out = append(out, f.Path)
		}
	}
	return out
}

var _ spells.Driver = (*diffTool)(nil)
