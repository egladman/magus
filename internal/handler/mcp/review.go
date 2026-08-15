package mcp

import (
	"context"
	"errors"
	"strings"

	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// reviewTool is the AGENT's half of a paired review: read the shared session, say something
// about a hunk, and ask for the human's attention.
//
// It reads and writes the same *review.Store the console route does, which is the whole point
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
type reviewTool struct {
	sessions *review.Store
	root     string
}

func (t *reviewTool) Name() string { return ToolReview.String() }

func (t *reviewTool) Invoke(_ context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
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
			"mcp: no review session is open. The person drives that: they open the console's " +
				"Review surface (or run magus review), and this tool joins the session they started")
	}

	switch op {
	case "state":
		// The whole session: the annotated changeset, where the human is, what they have read,
		// and the conversation so far.
		return spells.InvokeResponse{Data: sess}, nil

	case "comment":
		body := strings.TrimSpace(paramString(req.Params, "body", ""))
		path := strings.TrimSpace(paramString(req.Params, "path", ""))
		if body == "" || path == "" {
			return spells.InvokeResponse{}, errors.New("mcp: comment needs path and body")
		}
		out := t.sessions.AddComment(t.root, types.ReviewComment{
			Path: path,
			Hunk: int(paramFloat(req.Params, "hunk", -1)),
			Body: body,
		}, types.ReviewAuthorAgent)
		return spells.InvokeResponse{Data: out}, nil

	case "suggest":
		path := strings.TrimSpace(paramString(req.Params, "path", ""))
		reason := strings.TrimSpace(paramString(req.Params, "reason", ""))
		if path == "" || reason == "" {
			// The reason is required because the suggestion is an INTERRUPTION, and one that
			// cannot say why it earned the reader's attention should not have been made.
			return spells.InvokeResponse{}, errors.New("mcp: suggest needs path and reason")
		}
		out := t.sessions.Suggest(t.root, types.ReviewSuggestion{
			Path:      path,
			Hunk:      int(paramFloat(req.Params, "hunk", -1)),
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

var _ spells.Driver = (*reviewTool)(nil)
