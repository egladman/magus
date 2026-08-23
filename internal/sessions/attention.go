package sessions

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
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
// The event is flattened to strings rather than embedded as a types.Event. A session
// file is read by binaries older than the one that wrote it, and nesting an envelope
// that carries its own schema version would leave such a reader two versions to
// reconcile for one fact.
// Delegation is the delegation the RAISING session was launched under, which is what lets a
// person reading the queue see which slice of a fleet's work is blocked. It is attribution and
// not identity: see [RequestID] for why it stays out of the id.
type AttentionOpen struct {
	Request    string `json:"request"`
	Outcome    string `json:"outcome"`
	Severity   string `json:"severity,omitempty"`
	Source     string `json:"source,omitempty"`
	Where      string `json:"where,omitempty"`
	Delegation string `json:"delegation,omitempty"`
	Message    string `json:"message"` // clamped to MaxMessageBytes when written
}

// MaxMessageBytes bounds the Message one [AttentionOpen] may carry into the store.
//
// The message is whatever a producer piped to `magus session notify` on stdin, and in
// practice that is a hook forwarding an agent's text - a prompt, a tool argument, a
// transcript tail. The store is grow-only and every reader folds the WHOLE store
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
// it and what o says. Exactly three of o's fields are read - Source, Where and
// Message - and the result is stable: the same session and the same three values
// always name the same request, on any machine and in any worktree.
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
// The DELEGATION is not an input either, for a different reason. A delegation says which slice of work the
// raising session belongs to - it is attribution, and identity here is "which block is this".
// Feeding it in would re-key every open request the moment a fleet re-partitioned its delegations: the
// row a person was about to dispose of would vanish and an identical one would appear under a new
// id, from a change that did not touch the block at all.
//
// Fields are joined with NUL, which none of them can contain, so no pair of values
// can concatenate into another pair's digest.
func RequestID(session string, o AttentionOpen) string {
	sum := sha256.Sum256([]byte(session + "\x00" + o.Source + "\x00" + o.Where + "\x00" + o.Message))
	return "att-" + hex.EncodeToString(sum[:])[:12]
}

// AttentionRequest is one request as a reader meets it: what was raised, and whether
// anybody has disposed of it.
type AttentionRequest struct {
	ID         string `json:"id"`
	Session    string `json:"session"` // the session that raised it
	OpenedMs   int64  `json:"opened_ms"`
	Outcome    string `json:"outcome"`
	Severity   string `json:"severity,omitempty"`
	Source     string `json:"source,omitempty"`
	Where      string `json:"where,omitempty"`
	Delegation string `json:"delegation,omitempty"`
	Message    string `json:"message"`

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

// Attention folds a store into one entry per request id, oldest first.
//
// Three rules, all decided HERE rather than at write time, because the producers are
// separate processes in separate worktrees and nothing serializes them:
//
//   - An open of an id that is already open collapses into the request the earlier
//     open created. An open of an id that has been disposed raises it again: the
//     block came back, and a queue that swallowed it would be hiding a live wait
//     behind an answer that was given to a different one.
//   - The first dispose closes the request. A later dispose of the same id is still a
//     record in the store - nothing here rewrites one - but it only advances
//     Disposes, and cannot change who closed the request or when.
//   - A dispose naming an id with no open counts for nothing. It is not corruption:
//     the open may sit in a file another process has not finished writing, or in a
//     kind this build does not know.
//
// "First" is well defined because [ReadAll] has already ordered the fold by
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
				ID:         open.Request,
				Session:    rec.Session,
				OpenedMs:   rec.Ts,
				Outcome:    open.Outcome,
				Severity:   open.Severity,
				Source:     open.Source,
				Where:      open.Where,
				Delegation: open.Delegation,
				Message:    open.Message,
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

// AttentionQueue is [Attention] narrowed to the requests nobody has disposed of - the
// queue itself. Oldest first, so whatever has been waiting longest reads at the top.
func AttentionQueue(fold Fold) []AttentionRequest {
	all := Attention(fold)
	out := make([]AttentionRequest, 0, len(all))
	for _, req := range all {
		if !req.Disposed {
			out = append(out, req)
		}
	}
	return out
}

// SourceLabel renders an event's producer as one addressable label.
//
// It lives beside [RequestID] because it feeds it: the label is one of the three digest
// inputs, so the queue's SOURCE column and a request's identity are the same string by
// construction. Splitting the two apart is how they would come to disagree, and a
// disagreement there re-keys every open request without touching a block.
func SourceLabel(kind, sub string) string {
	if sub == "" {
		return kind
	}
	return kind + "/" + sub
}

// ErrNoRequest reports a request reference that names nothing in the store.
var ErrNoRequest = errors.New("no such attention request")

// AmbiguousRequestError reports a prefix matching more than one request. It names every
// candidate, because the next thing the caller has to do is pick one and the ids differ
// only past the prefix they typed.
type AmbiguousRequestError struct {
	Prefix     string
	Candidates []string
}

func (e *AmbiguousRequestError) Error() string {
	return fmt.Sprintf("%q matches %d requests (%s)", e.Prefix, len(e.Candidates), strings.Join(e.Candidates, ", "))
}

// DisposedError reports a request that was already closed, and by whom. A request closes
// once: a second dispose is recorded but changes nothing, so refusing here is what keeps
// the caller from reporting a closure it did not perform.
type DisposedError struct {
	ID         string
	DisposedBy string
	DisposedMs int64
}

func (e *DisposedError) Error() string {
	return fmt.Sprintf("request %s was already disposed by session %s at %s",
		e.ID, e.DisposedBy, time.UnixMilli(e.DisposedMs).Format(time.RFC3339))
}

// ResolveRequestID resolves ref - a full request id or an unambiguous prefix of one -
// against every request in fold, disposed ones included.
//
// The prefix form exists because the id is a truncated digest: it is unguessable, so it
// has to be copied off a listing, and typing twelve hex characters to close a queue of
// three is friction with no safety in it. Git settled this convention and a person
// already reads short hashes that way.
//
// Ambiguity is an error naming the candidates rather than a pick. An id addresses a
// person's decision to close a block, and resolving a tie by any rule this function
// could invent - oldest, first sorted - closes a request nobody chose.
//
// Disposed requests are candidates on purpose: a prefix that resolves to a closed
// request has to report THAT (see [DisposeRequest]), not read as a typo.
func ResolveRequestID(fold Fold, ref string) (string, error) {
	return resolveRequestID(Attention(fold), ref)
}

// resolveRequestID resolves against an already-folded queue, so a caller holding one
// does not fold it twice.
func resolveRequestID(all []AttentionRequest, ref string) (string, error) {
	if ref == "" {
		return "", ErrNoRequest
	}
	// An exact id wins over the prefix scan. Without that, a request whose id is a
	// prefix of another's could never be addressed at all.
	if slices.ContainsFunc(all, func(r AttentionRequest) bool { return r.ID == ref }) {
		return ref, nil
	}
	var matches []string
	for _, req := range all {
		if strings.HasPrefix(req.ID, ref) {
			matches = append(matches, req.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", ErrNoRequest
	case 1:
		return matches[0], nil
	default:
		slices.Sort(matches)
		return "", &AmbiguousRequestError{Prefix: ref, Candidates: matches}
	}
}

// OpenRequest files a raised block under dir as a durable open request, so a
// notification nobody happened to be looking at is still answerable afterwards, from any
// worktree of the repository.
//
// agentSession is the AGENT's session id, not this magus invocation's: a re-fire is a
// second magus process, and keying on that would mint a fresh id every time. It fills
// open.Request via [RequestID], so the caller must not set that field.
//
// Re-filing a block that is already open is a no-op reporting opened=false. An agent
// hook may fire on every prompt, and the queue has to hold one row per block rather than
// one per attempt. A block that was raised, disposed, and has come back opens again -
// see [Attention] for why.
//
// start describes the writing invocation; its Command is set here, and the session id is
// minted by [NewID]. The returned id is the request's, addressable whether or not this
// call wrote anything.
func OpenRequest(dir, agentSession string, open AttentionOpen, start SessionStart) (id string, opened bool, err error) {
	open.Request = RequestID(agentSession, open)
	fold, err := ReadAll(dir)
	if err != nil {
		return open.Request, false, err
	}
	if slices.ContainsFunc(AttentionQueue(fold), func(req AttentionRequest) bool { return req.ID == open.Request }) {
		return open.Request, false, nil
	}
	start.Command = "session notify"
	w, err := Open(dir, NewID(), start)
	if err != nil {
		return open.Request, false, err
	}
	if err := w.Append(KindAttentionOpen, open); err != nil {
		return open.Request, false, err
	}
	return open.Request, true, nil
}

// DisposeRequest closes the request ref names under dir and returns it as the store now
// reads it. ref is a full id or an unambiguous prefix ([ResolveRequestID]).
//
// A request closes once. An already-closed one is a [DisposedError] rather than a second
// closure: [Attention] would record the extra dispose and keep the first one's answer, so
// reporting success here would credit this caller with a closure it did not perform.
// Unresolvable refs surface as [ErrNoRequest] or [AmbiguousRequestError].
//
// The disposal writes its OWN session, because a disposal IS its own invocation: it is
// what makes the store say which run of magus closed the request, and by extension which
// person was at the keyboard. start describes it; its Command is set here.
//
// The returned request is re-read from the store rather than patched in memory: the
// disposal's timestamp and session are whatever landed on disk, and a hand-assembled
// answer could disagree with what every other worktree is about to read.
func DisposeRequest(dir, ref, reason string, start SessionStart) (AttentionRequest, error) {
	fold, err := ReadAll(dir)
	if err != nil {
		return AttentionRequest{}, err
	}
	all := Attention(fold)
	id, err := resolveRequestID(all, ref)
	if err != nil {
		return AttentionRequest{}, err
	}
	req := all[slices.IndexFunc(all, func(r AttentionRequest) bool { return r.ID == id })]
	if req.Disposed {
		return req, &DisposedError{ID: id, DisposedBy: req.DisposedBy, DisposedMs: req.DisposedMs}
	}

	start.Command = "session dispose " + id
	w, err := Open(dir, NewID(), start)
	if err != nil {
		return AttentionRequest{}, err
	}
	if err := w.Append(KindAttentionDispose, AttentionDispose{Request: id, Note: reason}); err != nil {
		return AttentionRequest{}, err
	}

	if fold, err = ReadAll(dir); err != nil {
		return AttentionRequest{}, err
	}
	all = Attention(fold)
	if i := slices.IndexFunc(all, func(r AttentionRequest) bool { return r.ID == id }); i >= 0 {
		req = all[i]
	}
	return req, nil
}
