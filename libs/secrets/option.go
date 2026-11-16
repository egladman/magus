package secrets

import (
	"context"
	"log/slog"
	"time"
)

// Option configures a Resolver at construction. Options rather than setters because
// every field they touch is immutable afterwards, and a setter on a shared, mutex-guarded
// object invites callers to mutate it once it is in use.
type Option func(*Resolver)

// Timeouts bounds how long a provider read may take. A struct rather than two duration
// arguments: they are the same type and adjacent, so a transposed pair would compile, run,
// and wait six times too long or give up six times too early.
type Timeouts struct {
	// Interactive applies when the caller can prompt a person: long enough to find a
	// phone for a biometric.
	Interactive time.Duration
	// Unattended applies with nobody to prompt. Short, because a provider that would
	// prompt cannot, so waiting past the point a cached session would have answered only
	// delays a failure that is already certain.
	Unattended time.Duration
}

// DefaultTimeouts are the budgets a Resolver reports when no option sets them.
var DefaultTimeouts = Timeouts{Interactive: 60 * time.Second, Unattended: 10 * time.Second}

// WithTimeouts sets the provider-read budgets. A zero field keeps [DefaultTimeouts], so
// a partly specified configuration still works. The resolver only reports them through
// [Resolver.Timeouts]; a provider applies them.
func WithTimeouts(t Timeouts) Option {
	return func(r *Resolver) {
		if t.Interactive > 0 {
			r.timeouts.Interactive = t.Interactive
		}
		if t.Unattended > 0 {
			r.timeouts.Unattended = t.Unattended
		}
	}
}

// Opener builds the provider a caller selected by name with [Resolver.SetProviderName].
type Opener func(ctx context.Context, name string) (Provider, error)

// WithOpener sets the opener a named provider is built through. Without one, a read under
// a named provider fails; the built-in environment provider needs none.
func WithOpener(open Opener) Option {
	return func(r *Resolver) { r.open = open }
}

// WithShortSecret sets the function told about each resolved value shorter than
// [MinMaskLen]. Such a value is returned to the caller but never masked, because masking
// a two-character string would shred ordinary output; the caller has no other way to
// learn that the value is unprotected, so a caller that records output should say so.
// The function runs once per resolved reference, inside the read, and must not block.
func WithShortSecret(notice func(ctx context.Context, ref string)) Option {
	return func(r *Resolver) { r.short = notice }
}

// WithLogger sets where the endpoint forwarder reports what it cannot put on the socket:
// an upstream or provider failure, a server error. Without one, those reports are
// discarded. Records are logged with the request's run context, so a handler from
// [NewRedactingHandler] masks them.
func WithLogger(l *slog.Logger) Option {
	return func(r *Resolver) {
		if l != nil {
			r.log = l
		}
	}
}
