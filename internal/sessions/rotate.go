package sessions

import (
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/safetyscissors/session"
)

// AttentionTie keeps pruning from splitting an attention request, for [session.WithTie].
//
// A request is raised in one invocation's file and disposed of in another's (see
// [Attention]), so the records of one request routinely straddle two files that age out
// at different times. Deleting one of the pair changes what the queue says:
//
//   - Delete the file holding the dispose while the open survives, and
//     [AttentionQueue] RESURRECTS a request a person already answered.
//   - Delete the file holding a still-open request, and the queue silently loses a
//     wait nobody has answered: an expiry, which this package does not have.
//
// The tie holds every request still OPEN, at any age, since a request is closed by a
// person or not at all, and [session.Prune] deletes a request's files as a unit.
func AttentionTie() session.Tie {
	return session.Tie{
		Key: attentionID,
		Held: func(fold session.Fold) []string {
			var open []string
			for _, req := range AttentionQueue(fold) {
				open = append(open, req.ID)
			}
			return open
		},
	}
}

// attentionID returns the request a record names, or "" for a record that names none.
//
// The payload is decoded through a narrow struct shared by both kinds rather than
// through [AttentionOpen] and [AttentionDispose]: pruning needs the id and nothing
// else, and a decode that ignores the rest cannot start disagreeing with [Attention]
// about a field neither of them reads. A record whose id fails to decode is one
// [Attention] also skips, so it holds no file back.
func attentionID(rec session.Record) string {
	if rec.Kind != KindAttentionOpen && rec.Kind != KindAttentionDispose {
		return ""
	}
	var named struct {
		Request string `json:"request"`
	}
	if json.Unmarshal(rec.Payload, &named) != nil {
		return ""
	}
	return named.Request
}
