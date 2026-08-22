package sessionjournal

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"unicode/utf8"

	json "github.com/egladman/magus/internal/json"
)

// Kinds carrying the attention queue: an agent raises a block, a person closes it.
//
// Nothing else closes one. There is no expiry, no severity inference and no
// auto-triage, because an event whose whole meaning is "blocked on a human" stops
// meaning that the moment the tool answers it for them - see docs/doctrine.md,
// "Manual on purpose".
const (
	KindAttentionOpen    = "attention_open"
	KindAttentionDispose = "attention_dispose"
)

// AttentionOpen is the payload of a raised block: one normalized attention event,
// plus the request id it stays addressable by.
//
// The event is flattened to strings rather than embedded as a types.Event. A journal
// is read by binaries older than the one that wrote it, and nesting an envelope that
// carries its own schema version would leave such a reader two versions to reconcile
// for one fact.
// Unit is the work-ledger unit the RAISING session was launched under, which is what lets a
// person reading the queue see which slice of a fleet's work is blocked. It is attribution and
// not identity: see [RequestID] for why it stays out of the id.
type AttentionOpen struct {
	Request  string `json:"request"`
	Outcome  string `json:"outcome"`
	Severity string `json:"severity,omitempty"`
	Source   string `json:"source,omitempty"`
	Where    string `json:"where,omitempty"`
	Unit     string `json:"unit,omitempty"`
	Message  string `json:"message"` // clamped to MaxMessageBytes when written
}

// MaxMessageBytes bounds the Message one [AttentionOpen] may carry into the store.
//
// The message is whatever a producer piped to `magus notify` on stdin, and in
// practice that is a hook forwarding an agent's text - a prompt, a tool argument, a
// transcript tail. The journal is grow-only and every reader folds the WHOLE store
// into memory, so one unbounded message is a cost every later read of this repository
// pays. 4 KiB is far past what a person can act on from a queue row and far short of
// a line that makes the fold expensive.
const MaxMessageBytes = 4 << 10

// messageTruncated ends a message that was cut, so a reader can tell a short message
// from a long one nobody kept.
const messageTruncated = "... [truncated]"

// bounded clamps Message to [MaxMessageBytes], cutting on a rune boundary so the
// stored line stays valid UTF-8. The marker is appended past the bound rather than
// carved out of it: the constant says what a producer may send, and a marker
// competing for that budget would make the two harder to reason about than either.
//
// The request id is derived from the message the producer SENT (see [RequestID]), not
// from this copy, so two long blocks that share a 4 KiB prefix stay two requests.
func (o AttentionOpen) bounded() AttentionOpen {
	if len(o.Message) <= MaxMessageBytes {
		return o
	}
	cut := MaxMessageBytes
	for cut > 0 && !utf8.RuneStart(o.Message[cut]) {
		cut--
	}
	o.Message = o.Message[:cut] + messageTruncated
	return o
}

// AttentionDispose is the payload of a person closing a request.
//
// The disposing session is the enclosing [Record.Session]; repeating it in the
// payload would be a second copy of one fact, free to disagree with the envelope.
type AttentionDispose struct {
	Request string `json:"request"`
	Note    string `json:"note,omitempty"`
}

// RequestID derives the identity of one blocked request from the session that raised
// it and what it says. The result is stable: the same four inputs always name the
// same request, on any machine and in any worktree.
//
// That determinism is the whole dedupe mechanism. An agent re-fires a block freely -
// a hook that runs on every prompt, a retried tool call - and each re-fire has to
// land on the id already in the queue rather than adding another row a person has to
// dispose of separately.
//
// The outcome class is deliberately NOT an input. An agent that reports one block
// first as "waiting" and then as "permission" is describing the same wait, and two
// rows for it would be two interruptions for one event.
//
// The UNIT is not an input either, for a different reason. A unit says which slice of work the
// raising session belongs to - it is attribution, and identity here is "which block is this".
// Feeding it in would re-key every open request the moment a fleet re-partitioned its units: the
// row a person was about to dispose of would vanish and an identical one would appear under a new
// id, from a change that did not touch the block at all.
//
// Fields are joined with NUL, which none of them can contain, so no pair of values
// can concatenate into another pair's digest.
func RequestID(session, source, where, message string) string {
	sum := sha256.Sum256([]byte(session + "\x00" + source + "\x00" + where + "\x00" + message))
	return "att-" + hex.EncodeToString(sum[:])[:12]
}

// AttentionRequest is one request as a reader meets it: what was raised, and whether
// anybody has disposed of it.
type AttentionRequest struct {
	ID       string `json:"id"`
	Session  string `json:"session"` // the session that raised it
	OpenedMs int64  `json:"opened_ms"`
	Outcome  string `json:"outcome"`
	Severity string `json:"severity,omitempty"`
	Source   string `json:"source,omitempty"`
	Where    string `json:"where,omitempty"`
	Unit     string `json:"unit,omitempty"`
	Message  string `json:"message"`

	Disposed   bool   `json:"disposed"`
	DisposedMs int64  `json:"disposed_ms,omitempty"`
	DisposedBy string `json:"disposed_by,omitempty"` // the session that closed it
	Note       string `json:"note,omitempty"`
	// Disposes counts the dispose records naming this id since it was last raised.
	// The FIRST one closed the request and is the one reported above; a count over 1
	// means two people answered the same request, which is worth showing rather than
	// discarding.
	Disposes int `json:"disposes,omitempty"`
}

// Attention folds a journal into one entry per request id, oldest first.
//
// Three rules, all decided HERE rather than at write time, because the producers are
// separate processes in separate worktrees and nothing serializes them:
//
//   - An open of an id that is already open collapses into the request the earlier
//     open created. An open of an id that has been disposed raises it again: the
//     block came back, and a queue that swallowed it would be hiding a live wait
//     behind an answer that was given to a different one.
//   - The first dispose closes the request. A later dispose of the same id is still a
//     record in the journal - nothing here rewrites one - but it only advances
//     Disposes, and cannot change who closed the request or when.
//   - A dispose naming an id with no open counts for nothing. It is not corruption:
//     the open may sit in a file another process has not finished writing, or in a
//     kind this build does not know.
//
// "First" is well defined because [Read] has already ordered the fold by
// (Ts, Session, Seq). Records are consumed in that order and must not be handed to
// this function out of it; the returned slice is sorted separately, by when each
// request was last raised.
func Attention(fold Fold) []AttentionRequest {
	var order []string
	byID := make(map[string]*AttentionRequest)

	for _, rec := range fold.Records {
		switch rec.Kind {
		case KindAttentionOpen:
			var open AttentionOpen
			if json.Unmarshal(rec.Payload, &open) != nil || open.Request == "" {
				continue
			}
			req := byID[open.Request]
			if req != nil && !req.Disposed {
				continue
			}
			if req == nil {
				req = &AttentionRequest{}
				byID[open.Request] = req
				order = append(order, open.Request)
			}
			*req = AttentionRequest{
				ID:       open.Request,
				Session:  rec.Session,
				OpenedMs: rec.Ts,
				Outcome:  open.Outcome,
				Severity: open.Severity,
				Source:   open.Source,
				Where:    open.Where,
				Unit:     open.Unit,
				Message:  open.Message,
			}
		case KindAttentionDispose:
			var dispose AttentionDispose
			if json.Unmarshal(rec.Payload, &dispose) != nil || dispose.Request == "" {
				continue
			}
			req := byID[dispose.Request]
			if req == nil {
				continue
			}
			req.Disposes++
			if req.Disposed {
				continue
			}
			req.Disposed = true
			req.DisposedMs = rec.Ts
			req.DisposedBy = rec.Session
			req.Note = dispose.Note
		}
	}

	out := make([]AttentionRequest, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	// By OpenedMs rather than by first appearance: a request raised again after being
	// disposed carries the NEW open's timestamp, and leaving it at its original
	// position would put a fresh block above requests that have waited longer.
	slices.SortStableFunc(out, func(a, b AttentionRequest) int {
		if c := cmp.Compare(a.OpenedMs, b.OpenedMs); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// OpenAttention is [Attention] narrowed to the requests nobody has disposed of - the
// queue itself. Oldest first, so whatever has been waiting longest reads at the top.
func OpenAttention(fold Fold) []AttentionRequest {
	all := Attention(fold)
	out := make([]AttentionRequest, 0, len(all))
	for _, req := range all {
		if !req.Disposed {
			out = append(out, req)
		}
	}
	return out
}
