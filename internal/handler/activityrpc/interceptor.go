package activityrpc

import (
	"context"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/safetyscissors/journal"
)

// mutatingVerbs and readVerbs are the leading-word buckets every Connect method name falls into. They are
// the single source of truth classify() and the arch test share: a method whose leading word is in
// neither is UNCLASSIFIED: recorded at runtime (fail-closed) and rejected by TestKnownVerbs at build.
var (
	mutatingVerbs = []string{"Clear", "Delete", "Put", "Revoke", "Rotate", "Run", "Sync", "Create", "Update", "Set", "Submit", "Remove", "Mint"}
	readVerbs     = []string{"Get", "List", "Stream", "Watch", "Describe", "Export", "Query", "Explain", "Find", "Resolve"}
)

// leadingWord returns a PascalCase method's first word: the run up to (not including) the second uppercase
// letter ("RevokeToken" -> "Revoke", "ListTokens" -> "List", "Listen" -> "Listen"). Matching this EXACTLY
// against the verb sets (rather than HasPrefix) is what stops "Listen" being read as the verb "List".
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

// methodName extracts the bare method from a Connect procedure path ("/magus.token.v1alpha1.TokenService/
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
	subject    func(connect.AnyResponse) (blob []byte, preview string)
}

// WithSubject records what a successful call acted on: fn renders the response's subject as a
// blob, stored as the event's request payload, and a one-line preview. fn must never render a
// secret; a nil blob records nothing extra. The token service uses it so a mint or revoke names
// the token's id, not only the method.
func WithSubject(fn func(connect.AnyResponse) (blob []byte, preview string)) Option {
	return func(o *options) { o.subject = fn }
}

// Option configures an Interceptor. See WithAuditReads.
type Option func(*options)

// WithAuditReads makes the interceptor record READ calls (Get/List/...) in addition to mutations.
// It is off by default because the trail is for consequential actions, not queries, but the notes
// service opts in, since its every call is a read and a note read is worth auditing. The token
// service does NOT set it, so its ListTokens stays unrecorded.
func WithAuditReads() Option {
	return func(o *options) { o.auditReads = true }
}

// Interceptor records every MUTATING unary call on the service it wraps to the trail under trailDir, with
// the given kind (one wire Kind per mounted service: the token service is all token_lifecycle) and the
// name of the credential the bearer guard verified, read from the request context rather than from any
// field the caller sent. Reads are not recorded by default (the trail is for consequential actions, not
// queries); pass WithAuditReads to also record read verbs, as the notes service does.
// Recording is best-effort and post-hoc: it never blocks or fails the RPC (trail.Append swallows I/O
// errors, matching the trail's "never a precondition for the action it records" contract), and a failed
// mutation is still recorded, with its error, because an attempted revoke is itself worth auditing.
func Interceptor(trailDir string, kind journal.CallKind, opts ...Option) connect.Interceptor { //nolint:ireturn // the observability backend is Nop or OTel, chosen at runtime
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
				ev := journal.Call{
					Time:     start,
					Kind:     kind,
					Name:     method,
					Outcome:  journal.OutcomeOK,
					Duration: time.Since(start),
				}
				if err != nil {
					ev.Outcome = journal.OutcomeError
					ev.Error = err.Error()
				}
				if cfg.subject != nil && err == nil && resp != nil {
					if blob, preview := cfg.subject(resp); len(blob) > 0 {
						ev.Request = recorder.WritePayload(ctx, trailDir, "tok", blob)
						ev.Preview = preview
					}
				}
				recorder.AppendCall(ctx, trailDir, ev)
			}
			return resp, err
		}
	})
}
