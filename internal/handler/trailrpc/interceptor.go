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
// It is deliberately NARROW today: mounted only on the services whose mutations lacked a completion-time
// producer (TokenService). Extending it to the other mutating services (notably MemoryService's
// PutMemory/DeleteMemory, which are also unaudited today) is the recommended next step; doing so must
// reconcile with producers that already record on completion (jobs) so a mutation is not double-recorded,
// and may need a new wire Kind for memory edits.
package trailrpc

import (
	"context"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/egladman/magus/internal/trail"
)

// mutatingVerbs and readVerbs are the leading-word buckets every Connect method name falls into. They are
// the single source of truth classify() and the arch ratchet share: a method whose first word is in
// neither is UNCLASSIFIED - recorded at runtime (fail-closed) and rejected by TestKnownVerbs at build.
var (
	mutatingVerbs = []string{"Clear", "Delete", "Put", "Revoke", "Rotate", "Sync", "Create", "Update", "Set", "Submit", "Remove", "Mint"}
	readVerbs     = []string{"Get", "List", "Stream", "Watch", "Describe", "Export", "Query", "Explain"}
)

// classify reports whether a bare method name (e.g. "RevokeToken") names a mutation, and whether its verb
// was recognized at all. An unknown verb returns (true, false): audit it, but flag it as unclassified so
// the arch test can force a human decision before it ships.
func classify(method string) (mutating, known bool) {
	for _, v := range mutatingVerbs {
		if strings.HasPrefix(method, v) {
			return true, true
		}
	}
	for _, v := range readVerbs {
		if strings.HasPrefix(method, v) {
			return false, true
		}
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

// Interceptor records every MUTATING unary call on the service it wraps to the trail under trailDir, with
// the server-stamped actor and the given kind (one wire Kind per mounted service - the token service is
// all token_lifecycle). Reads are not recorded (the trail is for consequential actions, not queries).
// Recording is best-effort and post-hoc: it never blocks or fails the RPC - trail.Append swallows I/O
// errors, matching the trail's "never a precondition for the action it records" contract - and a failed
// mutation is still recorded, with its error, because an attempted revoke is itself worth auditing.
func Interceptor(trailDir, actor, kind string) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()
			resp, err := next(ctx, req)
			method := methodName(req.Spec().Procedure)
			if mutating, _ := classify(method); mutating {
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
