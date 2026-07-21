// Package diag is the shared diagnostic-code framework: the reusable machinery a codebase uses to declare
// its OWN stable, documented diagnostic codes and render them as errors that point at a lookup page. It is
// deliberately code-namespace-AGNOSTIC. It owns the MECHANISM - the Code/Error types, the "[code] msg /
// see: url" rendering, the errors.Is matching, and the run-time sink plumbing - but never a catalog of
// codes. Each consumer instantiates a Domain with its own docs-URL layout and declares its own Code
// constants; the namespaces are entirely separate, so no code is ever shared across consumers. magus
// instantiates it for its MGS#### codes; gopherbuzz instantiates it separately for its BZZ#### codes.
//
// It is its own module (github.com/egladman/diag, at libs/diag) precisely so both magus and gopherbuzz -
// which are separate Go modules - can each depend on it without depending on each other.
package diag

import (
	"context"
	"errors"
	"fmt"
)

// Code is a stable diagnostic identifier, e.g. "MGS1001" or "BZZ0003". Its prefix and numbering are a
// convention of the consumer that declares it; this package never interprets the string.
type Code string

// Error lets a Code serve as an errors.Is sentinel, so `errors.Is(err, MGS2007)` matches an *Error
// carrying that code - the idiomatic Go form (cf. syscall.Errno). The rendered text is the bare code; a
// Code is an IDENTIFIER, not a message, so build an error that carries context with Domain.Errorf rather
// than returning a bare Code.
func (c Code) Error() string { return string(c) }

// Error is a coded diagnostic error: a Code, a human message, and - when built through a Domain - the docs
// URL to render. A bare literal &Error{Code: X} carries no URL and is meant only as an errors.Is target.
type Error struct {
	Code Code
	Msg  string
	url  string // docs URL, captured at construction by a Domain; empty for a bare errors.Is-target literal
}

// ErrSentinel matches any *Error via errors.Is, so a caller can test "is this a diagnostic error at all"
// without naming a specific code.
var ErrSentinel = errors.New("diag")

// Error renders "[CODE] message" plus a "see: <url>" line when a docs URL was captured.
func (e *Error) Error() string {
	if e.url == "" {
		return fmt.Sprintf("[%s] %s", e.Code, e.Msg)
	}
	return fmt.Sprintf("[%s] %s\n  see: %s", e.Code, e.Msg, e.url)
}

// Is matches ErrSentinel (any diagnostic error), a bare Code sentinel with the same code, or another
// *Error carrying the same code. The Code case is the idiomatic target: errors.Is(err, MGS2007).
func (e *Error) Is(target error) bool {
	if target == ErrSentinel {
		return true
	}
	switch t := target.(type) {
	case Code:
		return e.Code == t
	case *Error:
		return e.Code == t.Code
	}
	return false
}

// Domain is one consumer's diagnostic namespace: how to build the docs URL for a Code in its family, and
// the factory for that consumer's coded errors. "Domain" here is the NSError sense - a namespace of related
// codes - not a network domain. A consumer creates one (magus for MGS, gopherbuzz for BZZ) and declares
// its own Code constants alongside it.
type Domain struct {
	urlFn func(Code) string
}

// New returns a Domain whose docs URL for a Code is built by urlFn.
func New(urlFn func(Code) string) *Domain {
	return &Domain{urlFn: urlFn}
}

// URL returns the docs page for c under this domain.
func (d *Domain) URL(c Code) string { return d.urlFn(c) }

// Errorf builds an *Error with c, a formatted message, and c's docs URL captured for rendering.
func (d *Domain) Errorf(c Code, format string, args ...any) *Error {
	return &Error{Code: c, Msg: fmt.Sprintf(format, args...), url: d.urlFn(c)}
}

// Format renders a code+message as a single slog-friendly line: "[CODE] msg (see URL)".
func (d *Domain) Format(c Code, msg string) string {
	return fmt.Sprintf("[%s] %s (see %s)", c, msg, d.urlFn(c))
}

// Event is one diagnostic fired during a run: the code, a message, and the unit that emitted it
// ("<project>:<target>", a project path, or empty).
type Event struct {
	Code    Code   `json:"code"              yaml:"code"`
	Message string `json:"message,omitempty" yaml:"message,omitempty"`
	Unit    string `json:"unit,omitempty"    yaml:"unit,omitempty"`
}

// Sink records diagnostics fired during a run; it must be safe for concurrent use. A run installs one in
// its context so a deep emission site reaches it via Emit; consumers drain it.
type Sink interface {
	Record(Event)
}

type sinkKey struct{}

// WithSink returns ctx carrying s, so a deep emission site can reach the sink without threading it through
// every signature.
func WithSink(ctx context.Context, s Sink) context.Context {
	return context.WithValue(ctx, sinkKey{}, s)
}

// Emit records ev to the sink in ctx, or is a no-op when none is installed (the common CLI path).
func Emit(ctx context.Context, ev Event) {
	if s, ok := ctx.Value(sinkKey{}).(Sink); ok && s != nil {
		s.Record(ev)
	}
}
