package session

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Config is what [Open] and [LoadEvents] open a writer from. Dir, Retention and
// PruneErrors are required; Invocation is required by Open and ignored by LoadEvents.
type Config struct {
	// Dir is the store, created if absent.
	Dir string
	// Invocation is the id the writer files its records under; see [ValidFileID].
	Invocation string
	// Start is the start record the writer emits ahead of its first fact.
	Start InvocationStart
	// Retention is how long an invocation file outlives its newest fact when opening
	// prunes the store. It must be positive; [DefaultRetention] is one choice.
	Retention time.Duration
	// PruneErrors hears each error the prune an open runs meets: the prune's own, the
	// prune lock's, and the stamp write's. None of them fails the open. It runs on the
	// opening goroutine and must not block.
	PruneErrors func(context.Context, error)

	clock func() time.Time
	tie   Tie
	errs  []error
}

// Option adjusts a [Config] before [Open], [LoadEvents] or [Prune] reads it. An option
// given a nil or empty value records an *[OptionError] on the Config rather than being
// ignored, so the call is refused.
type Option func(*Config)

// OptionError is an [Option] given a value it cannot use.
type OptionError struct {
	Option  string
	Problem string
}

func (e *OptionError) Error() string {
	return fmt.Sprintf("session: option %s: %s", e.Option, e.Problem)
}

// ConfigError is a [Config] field a writer cannot be opened from.
type ConfigError struct {
	Field   string
	Problem string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("session: config %s: %s", e.Field, e.Problem)
}

func (c *Config) fail(option, problem string) {
	c.errs = append(c.errs, &OptionError{Option: option, Problem: problem})
}

// WithClock stamps records and ages files by now instead of the real clock. A nil now is
// an *[OptionError].
func WithClock(now func() time.Time) Option {
	return func(c *Config) {
		if now == nil {
			c.fail("WithClock", "nil function")
			return
		}
		c.clock = now
	}
}

// WithTie has pruning keep the records t binds together; see [Prune]. A Tie with
// neither Key nor Held binds nothing and is an *[OptionError].
func WithTie(t Tie) Option {
	return func(c *Config) {
		if t.Key == nil && t.Held == nil {
			c.fail("WithTie", "a Tie with neither Key nor Held")
			return
		}
		c.tie = t
	}
}

// now is the configured clock's reading, the real clock's without [WithClock].
func (c Config) now() time.Time {
	if c.clock == nil {
		return time.Now()
	}
	return c.clock()
}

// applied is c with opts applied and every problem they recorded, joined.
func (c Config) applied(opts []Option) (Config, error) {
	for _, opt := range opts {
		opt(&c)
	}
	return c, errors.Join(c.errs...)
}

// checked is c with opts applied, and every option problem and every invalid field but
// Invocation.
func (c Config) checked(opts []Option) (Config, []error) {
	for _, opt := range opts {
		opt(&c)
	}
	errs := c.errs
	if c.Dir == "" {
		errs = append(errs, &ConfigError{Field: "Dir", Problem: "empty"})
	}
	if c.Retention <= 0 {
		errs = append(errs, &ConfigError{Field: "Retention", Problem: fmt.Sprintf("%s is not a positive duration", c.Retention)})
	}
	if c.PruneErrors == nil {
		errs = append(errs, &ConfigError{Field: "PruneErrors", Problem: "nil function"})
	}
	return c, errs
}
