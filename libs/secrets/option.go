package secrets

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Option adjusts a [Config] before [NewResolver] validates it. An option given a nil or
// invalid value records the problem on the Config rather than ignoring it, so
// NewResolver can refuse the construction: a dropped option would leave the Resolver
// running without the logger, notice or opener the caller meant it to have.
type Option func(*Config)

// OptionError is an [Option] given a value it cannot use.
type OptionError struct {
	Option  string
	Problem string
}

func (e *OptionError) Error() string {
	return fmt.Sprintf("secrets: option %s: %s", e.Option, e.Problem)
}

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

// DefaultTimeouts returns the budgets a Resolver reports when no option sets them.
func DefaultTimeouts() Timeouts {
	return Timeouts{Interactive: 60 * time.Second, Unattended: 10 * time.Second}
}

// longest is the budget a shared read runs under. The resolver cannot tell whether a
// provider will prompt, so it bounds a flight by the longer budget and leaves the
// tighter one to the provider, which knows.
func (t Timeouts) longest() time.Duration { return max(t.Interactive, t.Unattended) }

// WithTimeouts sets the provider-read budgets. A zero field keeps [DefaultTimeouts], so
// a partly specified configuration still works; a negative field is an *[OptionError]. A
// provider applies them, reading them through [Resolver.Timeouts]; the resolver bounds a
// read shared by concurrent callers by the longer of the two.
func WithTimeouts(t Timeouts) Option {
	return func(c *Config) {
		if t.Interactive < 0 || t.Unattended < 0 {
			c.fail("WithTimeouts", "a negative timeout")
			return
		}
		if t.Interactive > 0 {
			c.Timeouts.Interactive = t.Interactive
		}
		if t.Unattended > 0 {
			c.Timeouts.Unattended = t.Unattended
		}
	}
}

// Opener builds the provider a caller selected by name with [Resolver.WithProvider].
type Opener func(ctx context.Context, name string) (Provider, error)

// WithOpener sets the opener a named provider is built through. Without one, a read under
// a named provider fails; the fallback provider needs none. A nil opener is an
// *[OptionError].
func WithOpener(open Opener) Option {
	return func(c *Config) {
		if open == nil {
			c.fail("WithOpener", "nil opener")
			return
		}
		c.Open = open
	}
}

// WithShortSecret sets the function told about each resolved value shorter than
// [MinMaskLen]. Such a value is returned to the caller but never masked, because masking
// a two-character string would shred ordinary output; the caller has no other way to
// learn that the value is unprotected, so a caller that records output should say so.
// The function runs once per resolved reference, inside the read, and must not block. A
// nil function is an *[OptionError].
func WithShortSecret(notice func(ctx context.Context, ref string)) Option {
	return func(c *Config) {
		if notice == nil {
			c.fail("WithShortSecret", "nil notice")
			return
		}
		c.ShortSecret = notice
	}
}

// WithLogger sets where the endpoint forwarder reports what it cannot put on the socket:
// an upstream or provider failure, a server error. Without one, those reports are
// discarded. Records are logged with the request's run context, so a handler from
// [NewRedactingHandler] masks them. A nil logger is an *[OptionError].
func WithLogger(l *slog.Logger) Option {
	return func(c *Config) {
		if l == nil {
			c.fail("WithLogger", "nil logger")
			return
		}
		c.Log = l
	}
}
