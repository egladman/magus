// Package annotate emits CI job-log structure: the fold markers and the
// warning/error notices a CI provider recognises.
//
// The shape mirrors [cache.RemoteBackend]: magus core names no provider.
// It holds an [Annotator], asks whether it is Active, and calls generic
// verbs. Provider syntax lives behind an implementation, so supporting a
// new one is a new implementation and no change to any call site.
//
// The vocabulary is deliberately the union of what real providers need,
// not the intersection, because the intersection is nearly empty:
//
//   - Groups carry an ID separate from their title. GitHub and Azure use
//     only a title, but GitLab sections and TeamCity blocks are keyed by
//     a machine name, and a shared abstraction that omitted it could not
//     express them at all.
//   - Groups state whether they want to be collapsed. GitHub always
//     collapses, Buildkite has three modes, GitLab takes a flag. Callers
//     say what they mean and a provider honours what it can.
//   - EndGroup may legitimately do nothing: Buildkite has no end marker,
//     since a new group implicitly closes the previous one.
//   - Annotations carry a source location and a diagnostic code, because
//     every provider that supports them at all supports those, and
//     dropping them would throw away magus's own MGSxxxx codes.
//
// A provider that cannot express something ignores it. Unsupported is
// the normal case, not an error: AWS CodeBuild and CircleCI have no log
// markers whatsoever.
package annotate

import (
	"io"
	"os"
	"strings"
	"sync"
	"unicode/utf8"
)

// Level is an annotation's severity. Providers spell these differently
// and some support only a subset; an implementation maps what it can and
// drops the rest.
type Level int

const (
	LevelNotice Level = iota
	LevelWarning
	LevelError
)

// String returns the lowercase level name, which is the token most
// provider syntaxes embed directly.
func (l Level) String() string {
	switch l {
	case LevelError:
		return "error"
	case LevelWarning:
		return "warning"
	default:
		return "notice"
	}
}

// Group describes a foldable section of the job log.
type Group struct {
	// ID is a stable, opaque identifier for the section. Providers that key
	// sections by name (GitLab, TeamCity) match a close against its open by
	// this; providers that address sections only by title ignore it.
	//
	// magus passes something meaningful and readable, such as a project
	// path. It does NOT normalize the character set, because what is legal
	// differs per provider and encoding one provider's rule here would put
	// that provider back into the generic layer. A provider with charset
	// restrictions normalizes in its own spell.
	ID string
	// Title is the human-readable heading.
	Title string
	// Collapsed asks for the section to start folded. It is a request, not
	// a guarantee: GitHub always folds regardless, and providers with no
	// notion of folding ignore it. Callers should set it false for output
	// the reader needs to see without clicking - a failure, above all.
	Collapsed bool
}

// Annotation is a message surfaced outside the job log, typically on a
// pull request. Every field beyond Message is optional; a provider uses
// what it supports.
type Annotation struct {
	Level   Level
	Message string
	// Title is a short headline shown above Message where supported.
	Title string
	// Code is a diagnostic identifier (magus emits MGSxxxx). Azure carries
	// it natively; others fold it into the title.
	Code string
	// File is workspace-relative. Line/EndLine/Col/EndCol are 1-based and
	// zero means unset, so a file-level annotation needs no sentinel.
	File                       string
	Line, EndLine, Col, EndCol int
}

// Annotator writes one provider's job-log structure.
//
// Implementations are constructed around their destination and are
// expected to be cheap: core may call Active per build step. Nothing
// here assumes the output is a stream - Buildkite raises annotations by
// invoking its agent binary, so an implementation may shell out.
type Annotator interface {
	// Active reports whether this provider is running the job. False makes
	// every other method a no-op, so callers do not branch.
	Active() bool
	// StartGroup opens a foldable section; EndGroup closes the one with
	// the given ID. Providers that auto-close on the next group treat
	// EndGroup as a no-op.
	StartGroup(g Group) error
	EndGroup(id string) error
	// Annotate raises a message outside the job log.
	Annotate(a Annotation) error
	// Quote returns text safe to replay into the job log, neutralising any
	// provider command syntax it contains.
	//
	// This is not cosmetic. magus captures a subprocess's output and
	// replays it, so a test that prints "::error::" or a GitLab section
	// marker would otherwise be interpreted by the runner: a dependency's
	// output could forge annotations or close a section magus opened.
	//
	// A provider supplies the line prefixes that introduce its commands
	// once, rather than being consulted per line - see QuoteWith. That is
	// what keeps this affordable on a path that runs over every replayed
	// line of a failing build's output.
	Quote(text string) string
}

// Nop is the Annotator used when no provider is detected. Every method
// succeeds and does nothing, so core needs no nil checks.
type Nop struct{}

func (Nop) Active() bool              { return false }
func (Nop) StartGroup(Group) error    { return nil }
func (Nop) EndGroup(string) error     { return nil }
func (Nop) Annotate(Annotation) error { return nil }
func (Nop) Quote(text string) string  { return text }

var (
	openerMu sync.RWMutex
	opener   func(io.Writer) Annotator
)

// RegisterOpener installs the hook that supplies a spell-backed
// Annotator. The bindings layer registers it at init, so core selects a
// provider without linking the Buzz VM - the same indirection
// [cache.RegisterRemoteBackendOpener] uses for remote cache backends.
func RegisterOpener(fn func(io.Writer) Annotator) {
	openerMu.Lock()
	defer openerMu.Unlock()
	opener = fn
}

// Detect returns the Annotator for the provider running this job, or
// [Nop] when none is active.
//
// Every provider is a spell: magus ships no CI syntax of its own, so a
// workspace opts in by naming one (magus.ci.provider), and adding support
// for a new system is a spell someone writes rather than a change to
// magus. A spell that reports itself inactive - the github spell outside
// Actions - yields Nop, so an unconditional wiring costs nothing
// elsewhere.
func Detect(w io.Writer) Annotator {
	openerMu.RLock()
	fn := opener
	openerMu.RUnlock()
	if fn != nil {
		if a := fn(w); a != nil && a.Active() {
			return a
		}
	}
	return Nop{}
}

// QuoteWith neutralises any line in text that begins with one of the
// given command prefixes, by dropping the prefix's first character so
// the provider no longer recognises the line as a command.
//
// Providers hand over their prefixes once, at resolution, rather than
// being asked per line: this runs over every replayed line of a failing
// build's output, which is the one path where crossing into a spell's VM
// per call would cost more than the whole feature is worth. It is the
// declarative half of an otherwise fully spell-implemented contract.
//
// Dropping the first character rather than inserting one keeps the result
// plain ASCII and still legible: "::error::x" becomes ":error::x", and a
// GitLab marker loses the escape byte that introduced it, leaving text a
// reader can still see. Leading whitespace is preserved.
func QuoteWith(text string, prefixes []string) string {
	if len(prefixes) == 0 {
		return text
	}
	var hit bool
	for _, p := range prefixes {
		if p != "" && strings.Contains(text, p) {
			hit = true
			break
		}
	}
	if !hit {
		return text
	}
	lines := strings.Split(text, "\n")
	for i, ln := range lines {
		trimmed := strings.TrimLeft(ln, " \t")
		for _, p := range prefixes {
			if p != "" && strings.HasPrefix(trimmed, p) {
				_, size := utf8.DecodeRuneInString(trimmed)
				lines[i] = ln[:len(ln)-len(trimmed)] + trimmed[size:]
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

// OnCI reports whether magus is running in an automated environment,
// independent of which one.
//
// It reads CI, the de-facto convention every major provider sets
// (GitHub, GitLab, CircleCI, Travis, Buildkite, and others). That is a
// cross-vendor signal rather than a vendor name, so consulting it here
// does not put provider knowledge back into the binary.
//
// This answers a different question from [Detect]: a workspace that
// wires no provider spell still runs on CI, and callers that only need
// to know "is a human at this terminal" - to suppress a hint whose
// command addresses local state, say - should not require one.
func OnCI() bool { return os.Getenv("CI") != "" }
