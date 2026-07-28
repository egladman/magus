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
	"strings"
	"sync"
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
	// ID is the machine name providers key a section by (GitLab, TeamCity).
	// Providers that address sections only by title ignore it. Keep it to
	// letters, digits, underscore, dot, and dash: GitLab rejects the rest.
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
	// Concurrency is the provider's suggested parallelism for its runners,
	// or 0 for no opinion. Hosted runners are typically far smaller than
	// the machine count suggests, so a provider that knows its own sizing
	// saves every user from discovering it.
	Concurrency() int
	// Quote returns text safe to replay into the job log, neutralising any
	// provider command syntax it contains.
	//
	// This is not cosmetic. magus captures a subprocess's output and
	// replays it, so a test that prints "::error::" or "##vso[...]" would
	// otherwise have the runner execute it: a dependency's output could
	// forge annotations or mask values in someone else's CI.
	Quote(text string) string
}

// Nop is the Annotator used when no provider is detected. Every method
// succeeds and does nothing, so core needs no nil checks.
type Nop struct{}

func (Nop) Active() bool              { return false }
func (Nop) StartGroup(Group) error    { return nil }
func (Nop) EndGroup(string) error     { return nil }
func (Nop) Annotate(Annotation) error { return nil }
func (Nop) Concurrency() int          { return 0 }
func (Nop) Quote(text string) string  { return text }

// builtins are the providers compiled into magus, consulted in order.
// This slice is the only place the binary enumerates CI vendors.
//
// A built-in exists so the common case needs no configuration. Providers
// beyond these arrive as spells (see RegisterOpener), which is how a
// third party adds one without changing magus.
var builtins = []func(io.Writer) Annotator{
	newGitHubActions,
	newGitLabCI,
}

var (
	openerMu sync.RWMutex
	opener   func(io.Writer) Annotator
)

// RegisterOpener installs a hook that can supply a spell-backed
// Annotator. The bindings layer registers it at init, so core selects a
// spell provider without linking the Buzz VM - the same indirection
// [cache.RegisterRemoteBackendOpener] uses for remote cache backends.
//
// A registered opener wins over the built-ins: a workspace that names a
// provider explicitly means it.
func RegisterOpener(fn func(io.Writer) Annotator) {
	openerMu.Lock()
	defer openerMu.Unlock()
	opener = fn
}

// Detect returns the Annotator for the provider running this job, or
// [Nop] when none is recognised.
//
// A spell provider is preferred when one is registered and active;
// otherwise the built-ins are probed by environment, which is each
// provider's own documented signal and so needs no configuration.
func Detect(w io.Writer) Annotator {
	openerMu.RLock()
	fn := opener
	openerMu.RUnlock()
	if fn != nil {
		if a := fn(w); a != nil && a.Active() {
			return a
		}
	}
	for _, newBuiltin := range builtins {
		if a := newBuiltin(w); a.Active() {
			return a
		}
	}
	return Nop{}
}

// SanitizeID reduces s to the character set every provider accepts for a
// section name: letters, digits, underscore, dot, and dash. GitLab
// rejects anything else outright, so a project path (which contains
// slashes) has to be folded before it can key a section.
//
// Runs of rejected characters collapse to a single dash, so two distinct
// paths do not produce one long unreadable run of separators.
func SanitizeID(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '_', r == '.', r == '-':
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
