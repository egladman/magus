// Package trailrpc is the audit interceptor for the daemon's Connect services: a connect.Interceptor
// that records MUTATING unary RPCs to the activity trail by construction, so auditing a state change is a
// structural default of the mount rather than a per-handler line a developer must remember to add.
//
// WHY AN INTERCEPTOR. The trail's other producers are hand-placed trail.Append calls at scattered
// callsites; a new mutating handler that forgets the call is silently unaudited (the gap this closes for
// the token service, whose RevokeToken recorded nothing). Wrapping the SERVICE means every method on it -
// including one added later - passes through here. Two properties matter for an audit boundary a hostile
// contributor cannot quietly sidestep:
//   - The actor is SERVER-STAMPED at the mount (the tier the guard already enforces), never read from a
//     caller-supplied field, so a handler cannot forge who acted.
//   - Classification is fail-CLOSED: a method whose verb this package does not recognize is treated as
//     mutating and recorded, so a novel RPC is over-audited, never silently skipped. TestKnownVerbs
//     (the arch ratchet) additionally fails CI if any mounted service grows a method with an unclassified
//     verb, forcing the author to place it in one bucket or the other.
//
// It is deliberately NARROW today: mounted only on TokenService (whose RevokeToken mutation lacked any
// producer). The audit-trail assessment (session plans) tracks extending it to the other mutating
// services and the reconciliation that needs (jobs already record on completion; memory edits would need
// a new wire Kind) - kept out of this doc so it does not rot against current method names.
//
// KNOWN LIMIT of verb classification: a method's leading word is matched EXACTLY, so "Listen" is not
// mistaken for the read verb "List" (it falls to unclassified -> recorded -> flagged by the arch test).
// What exact-word matching still cannot catch is a COMPOUND verb whose first word is a read verb
// but whose action mutates (a hypothetical "ExportAndReset"): it would classify read and skip. The
// convention this codebase follows - one leading verb per method - keeps that out of reach, and the arch
// test surfaces any novel verb; but a reviewer adding a compound method must place it deliberately.
package trailrpc

import (
	"context"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/egladman/magus/internal/trail"
)

// mutatingVerbs and readVerbs are the leading-word buckets every Connect method name falls into. They are
// the single source of truth classify() and the arch ratchet share: a method whose leading word is in
// neither is UNCLASSIFIED - recorded at runtime (fail-closed) and rejected by TestKnownVerbs at build.
var (
	mutatingVerbs = []string{"Clear", "Delete", "Put", "Revoke", "Rotate", "Sync", "Create", "Update", "Set", "Submit", "Remove", "Mint"}
	readVerbs     = []string{"Get", "List", "Stream", "Watch", "Describe", "Export", "Query", "Explain"}
)

// leadingWord returns a PascalCase method's first word: the run up to (not including) the second uppercase
// letter ("RevokeToken" -> "Revoke", "ListTokens" -> "List", "Listen" -> "Listen"). Matching this EXACTLY
// against the verb sets - rather than HasPrefix - is what stops "Listen" being read as the verb "List".
func leadingWord(method string) string {
	for i := 1; i < len(method); i++ {
		if method[i] >= 'A' && method[i] <= 'Z' {
			return method[:i]
		}
	}
	return method
}

// classify reports whether a bare method name (e.g. "RevokeToken") names a mutation, and whether its
// leading word was recognized at all. An unknown verb returns (true, false): audit it, but flag it as
// unclassified so the arch test can force a human decision before it ships.
func classify(method string) (mutating, known bool) {
	word := leadingWord(method)
	if slices.Contains(mutatingVerbs, word) {
		return true, true
	}
	if slices.Contains(readVerbs, word) {
		return false, true
	}
	return true, false
}

// methodName extracts the bare method from a Connect procedure path ("/magus.token.v1.TokenService/
// RevokeToken" -> "RevokeToken"). A path with no slash is returned unchanged.
func methodName(procedure string) string {
	if i := strings.LastIndex(procedure, "/"); i >= 0 {
		return procedure[i+1:]
	}
	return procedure
}

// options holds the interceptor's opt-in switches, set through Option values so the common
// mutation-only mount stays a three-argument call.
type options struct {
	auditReads bool // when true, read verbs are recorded too, not just mutations
}

// Option configures an Interceptor. See WithAuditReads.
type Option func(*options)

// WithAuditReads makes the interceptor record READ calls (Get/List/...) in addition to mutations.
// It is off by default because the trail is for consequential actions, not queries - but the memory
// service opts in, since a read of the agent's own working notes is itself worth auditing there. The
// token service does NOT set it, so its ListTokens stays unrecorded.
func WithAuditReads() Option {
	return func(o *options) { o.auditReads = true }
}

// Interceptor records every MUTATING unary call on the service it wraps to the trail under trailDir, with
// the server-stamped actor and the given kind (one wire Kind per mounted service - the token service is
// all token_lifecycle). Reads are not recorded by default (the trail is for consequential actions, not
// queries); pass WithAuditReads to also record read verbs, as the memory service does.
// Recording is best-effort and post-hoc: it never blocks or fails the RPC - trail.Append swallows I/O
// errors, matching the trail's "never a precondition for the action it records" contract - and a failed
// mutation is still recorded, with its error, because an attempted revoke is itself worth auditing.
func Interceptor(trailDir, actor string, kind trail.Kind, opts ...Option) connect.Interceptor {
	var cfg options
	for _, o := range opts {
		o(&cfg)
	}
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()
			resp, err := next(ctx, req)
			method := methodName(req.Spec().Procedure)
			if mutating, _ := classify(method); mutating || cfg.auditReads {
				ev := trail.Event{
					Ts:      start.UnixMilli(),
					Kind:    kind,
					Actor:   actor,
					Action:  method,
					Outcome: trail.OutcomeOK,
					DurMs:   time.Since(start).Milliseconds(),
				}
				if err != nil {
					ev.Outcome = trail.OutcomeError
					ev.Error = err.Error()
				}
				trail.Append(trailDir, ev)
			}
			return resp, err
		}
	})
}
