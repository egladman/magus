// Package annotate emits CI job-log structure: the fold markers and the
// warning/error notices a CI provider recognizes.
//
// The shape mirrors [cache.RemoteBackend]: magus core names no provider.
// It holds an [Annotator], asks whether it is Active, and calls generic
// verbs. Provider syntax lives behind an implementation, so supporting a
// new one is a new implementation and no change to any call site.
//
// The vocabulary is the UNION of what real providers need, not the
// intersection, which is nearly empty:
//
//   - Groups carry an ID separate from their title, because GitLab
//     sections and TeamCity blocks are keyed by a machine name.
//   - Groups state whether they want to be collapsed; providers honor
//     what they can.
//   - EndGroup may legitimately do nothing: Buildkite has no end marker.
//   - Annotations carry a source location and a diagnostic code, so
//     magus's own MGSxxxx codes survive.
//
// A provider that cannot express something ignores it. Unsupported is the
// normal case, not an error: CodeBuild and CircleCI have no markers at all.
package annotate

import (
	"io"
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
	case LevelNotice:
		return "notice"
	default:
		// Notice is spelled out above so an unknown level cannot silently become it.
		// This function decides how loudly a finding is reported, and defaulting to the
		// QUIETEST token means a level added upstream, or an int a spell handed in, gets
		// downgraded rather than noticed.
		return "unknown"
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
//
// SECURITY: Message and Title are UNTRUSTED - they carry a failing
// process's output, so their content is whatever some test, compiler or
// transitive dependency chose to print. A provider must never interpolate
// them where their content becomes syntax (a shell command, a URL), or a
// dependency that printed a payload would be executing it on every CI
// machine. [Sanitize] is a floor, not a license to be careless.
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
	// Quote returns text safe to replay into the job log, neutralizing any
	// provider command syntax it contains.
	//
	// Not cosmetic: magus replays captured subprocess output, so a test
	// printing "::error::" or a GitLab section marker would be interpreted
	// by the runner, forging annotations or closing a section magus opened.
	//
	// Providers supply their command prefixes once rather than being
	// consulted per line (see QuoteWith), which is what keeps this
	// affordable over every replayed line of a failing build.
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

// QuoteWith neutralizes any line in text that begins with one of the
// given command prefixes, by dropping the prefix's first character so
// the provider no longer recognizes the line as a command.
//
// Providers hand over their prefixes once rather than being asked per line:
// this runs over every replayed line of a failing build, the one path where
// crossing into a spell's VM per call would cost more than the feature is
// worth.
//
// Dropping the first character rather than inserting one keeps the result
// plain ASCII and legible - "::error::x" becomes ":error::x". Leading
// whitespace is preserved.
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

// Limits on what crosses into a provider. A provider is third-party code
// and the values it receives are attacker-influenced (see [Annotation]),
// so the boundary is bounded rather than trusting either side to cope.
const (
	// maxMessageLen is generous enough for a real failure cause and far
	// below what would make a spell's string handling a denial-of-service
	// vector. Callers already excerpt; this is the backstop.
	maxMessageLen = 4096
	// maxFieldLen bounds the short fields, which are headings and paths.
	maxFieldLen = 512
	// maxQuotePrefixes bounds what a provider can make magus match against
	// every replayed line. QuoteWith is O(lines x prefixes), so an
	// unbounded list turns a failing build into a hang.
	maxQuotePrefixes = 16
)

// Sanitize returns a copy of a bounded to the limits above and stripped
// of control characters.
//
// Control characters are the sharp edge: the message comes from a failing
// process, so it can carry escape sequences that would re-take the terminal
// or the job log when a provider echoes it. Tab is kept (ordinary in
// compiler output); newline is kept because providers encode it themselves.
//
// Truncation is marked, so a reader can tell a bounded message from a
// process that printed that much.
func Sanitize(a Annotation) Annotation {
	a.Message = clampText(a.Message, maxMessageLen)
	a.Title = clampText(a.Title, maxFieldLen)
	a.Code = clampText(a.Code, maxFieldLen)
	a.File = clampText(a.File, maxFieldLen)
	return a
}

// clampText strips control characters and truncates to n bytes on a rune
// boundary, appending a marker when it cut.
func clampText(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' {
			return r
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + " ... (truncated)"
}

// ClampPrefixes bounds a provider's declared quote prefixes, dropping
// empty entries (which would match every line) and over-long ones.
func ClampPrefixes(in []string) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		if p == "" || len(p) > maxFieldLen {
			continue
		}
		out = append(out, p)
		if len(out) == maxQuotePrefixes {
			break
		}
	}
	return out
}

// SanitizeGroup bounds a group's fields the way [Sanitize] bounds an
// annotation's. A group's title embeds a project and target name, which
// come from a magusfile rather than from process output, so this is a
// weaker threat than an annotation - but the same boundary applies, and
// a title is echoed into the job log all the same.
func SanitizeGroup(g Group) Group {
	g.ID = clampText(g.ID, maxFieldLen)
	g.Title = clampText(g.Title, maxFieldLen)
	return g
}
