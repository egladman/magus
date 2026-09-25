// Package risk classifies how risky a change to a path is: generated output, prose, a
// comment-only edit, or code. It is magus's one classifier of change risk: the ci gate
// asks it whether a delta since a green run needs the gate again, and a merge asks it
// whether a conflicted path may be settled without a person.
//
// The classes rest on the workspace's declarations. A path a declared output glob or
// magus itself owns is generated; a path an effective gate_low_risk glob claims is prose
// (magus ships markdown defaults, see ProseScopes); a file whose delta touches only
// comments, by the comment syntax its language's spell declares, is comment-only. Every
// other path, and every path the classifier cannot read, is code.
package risk

import (
	"context"
	"path"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// Class is the risk class of one changed path.
type Class int

const (
	// ClassCode is everything the three low-risk classes do not cover.
	ClassCode Class = iota
	// ClassGenerated is a path a declared output glob or magus itself owns.
	ClassGenerated
	// ClassProse is a path an effective gate_low_risk glob claims; magus ships
	// markdown defaults (see DefaultProseGlobs).
	ClassProse
	// ClassCommentOnly is a file whose delta touches only comments.
	ClassCommentOnly
)

// String is the word the per-path verdict lines print.
func (c Class) String() string {
	switch c {
	case ClassGenerated:
		return "generated"
	case ClassProse:
		return "prose"
	case ClassCommentOnly:
		return "comment-only"
	}
	return "code"
}

// Classified is one changed path's class and the fact it rests on, so a reader can
// dispute the decision from the message alone.
type Classified struct {
	Path  string
	Class Class
	// Why names what classified it: the claiming role, the matching glob and its origin,
	// or the comment-comparison outcome.
	Why string
}

// LowRisk reports whether the path avoided ClassCode.
func (c Classified) LowRisk() bool { return c.Class != ClassCode }

// Line is the path's verdict line: `<path>: <class> (<why>)`.
func (c Classified) Line() string { return c.Path + ": " + c.Class.String() + " (" + c.Why + ")" }

// Delta is the per-path verdict over one delta.
type Delta struct {
	Paths []Classified
}

// LowRiskOnly reports whether every classified path avoided ClassCode. An empty delta is
// low-risk: nothing changed.
func (d Delta) LowRiskOnly() bool {
	return !slices.ContainsFunc(d.Paths, func(v Classified) bool { return v.Class == ClassCode })
}

// Lines renders one verdict line per path: every file, never a summary, because the
// reader of a refusal must be able to reconstruct the decision.
func (d Delta) Lines() []string {
	out := make([]string, len(d.Paths))
	for i, v := range d.Paths {
		out[i] = v.Line()
	}
	return out
}

// DefaultProseGlobs are the prose globs magus ships: markdown sources, which covers docs
// and changelog prose. They apply only while NO project declares gate_low_risk; the first
// declaration replaces them workspace-wide.
var DefaultProseGlobs = []string{"**/*.md", "**/*.markdown"}

// ProseOriginDefault names the built-in glob set in per-path attributions.
const ProseOriginDefault = "built-in default"

// Scope is one source of globs: the built-in prose set, or one project's gate_low_risk
// or merge_low_risk declaration. Globs match the way review_required's do (relative to
// the declaring project's directory), so an author names files the same way their
// sources and outputs already do.
type Scope struct {
	// Dir is the declaring project's workspace-relative path; "" or "." matches against
	// the whole workspace-relative path.
	Dir string
	// Globs are doublestar patterns relative to Dir.
	Globs []string
	// Origin is what the per-path attribution names: ProseOriginDefault, or
	// `<key> of project <path>`.
	Origin string
}

// WorkspaceGlobs are s's globs made relative to the workspace, the form a VCS attribute
// file takes.
func (s Scope) WorkspaceGlobs() []string {
	out := make([]string, len(s.Globs))
	for i, g := range s.Globs {
		if s.Dir == "" || s.Dir == "." {
			out[i] = g
		} else {
			out[i] = s.Dir + "/" + g
		}
	}
	return out
}

// ProseScopes resolves the workspace's effective prose globs. No declaration anywhere
// means the shipped defaults; ANY declaration replaces them with the union of declared
// scopes, so a workspace that declares `[]` and nothing else has turned the prose class
// off entirely.
func ProseScopes(projects []*types.Project) []Scope {
	var scopes []Scope
	declared := false
	for _, p := range projects {
		if !p.GateLowRiskDeclared {
			continue
		}
		declared = true
		if len(p.GateLowRisk) > 0 {
			scopes = append(scopes, Scope{Dir: p.Path, Globs: p.GateLowRisk, Origin: "gate_low_risk of project " + p.Path})
		}
	}
	if !declared {
		return []Scope{{Globs: DefaultProseGlobs, Origin: ProseOriginDefault}}
	}
	return scopes
}

// MergeScopes are the projects' merge_low_risk declarations: the code a merge may settle
// without a person. Nothing is declared by default.
func MergeScopes(projects []*types.Project) []Scope {
	var scopes []Scope
	for _, p := range projects {
		if len(p.MergeLowRisk) > 0 {
			scopes = append(scopes, Scope{Dir: p.Path, Globs: p.MergeLowRisk, Origin: "merge_low_risk of project " + p.Path})
		}
	}
	return scopes
}

// MatchScopes returns the first scope glob matching the workspace-relative path, with its
// origin, or ok=false when none claims it.
func MatchScopes(scopes []Scope, p string) (glob, origin string, ok bool) {
	for _, s := range scopes {
		rel := p
		if s.Dir != "" && s.Dir != "." {
			if !strings.HasPrefix(p, s.Dir+"/") {
				continue
			}
			rel = strings.TrimPrefix(p, s.Dir+"/")
		}
		for _, g := range s.Globs {
			if matched, err := doublestar.Match(g, rel); err == nil && matched {
				return g, s.Origin, true
			}
		}
	}
	return "", "", false
}

// Classifier classifies changed paths. Its dependencies are functions rather than the
// workspace types that provide them, so the class table is testable against literal
// content. The zero Classifier classifies every path as code.
type Classifier struct {
	// Role returns each path's describe-file role ("output", "maintained", "source", ...),
	// keyed by the path as passed. Missing entries classify by the remaining classes alone.
	Role func(ctx context.Context, paths []string) (map[string]string, error)
	// Prose is the effective glob set, from ProseScopes. Empty means the prose class is
	// off.
	Prose []Scope
	// Syntax routes a file extension (lowercase, with dot) to the comment syntax a spell
	// DECLARED for it (spells.CommentSyntaxIndex over the projects' resolved spells). An
	// unclaimed extension classifies as code.
	Syntax map[string]spells.CommentSyntax
	// At returns a file's content at the revision the delta is measured from. An error
	// means the path did not exist there or cannot be read; the path reads as code.
	At func(ctx context.Context, rev, path string) (string, error)
	// Working returns the file's changed content: the working tree's, or a merge's.
	Working func(path string) (string, error)
}

// Classify assigns each changed path its risk class against rev, the revision the change
// is measured from, and the reason a reader disputes it by. Every failure to classify (an
// unreadable revision, a file that does not lex) lands the path in ClassCode.
func (c Classifier) Classify(ctx context.Context, paths []string, rev string) Delta {
	out := Delta{Paths: make([]Classified, len(paths))}
	roles := map[string]string{}
	if c.Role != nil {
		if r, err := c.Role(ctx, paths); err == nil {
			roles = r
		}
	}
	for i, p := range paths {
		class, why := c.classify(ctx, p, roles[p], rev)
		out.Paths[i] = Classified{Path: p, Class: class, Why: why}
	}
	return out
}

func (c Classifier) classify(ctx context.Context, p, role, rev string) (Class, string) {
	if role == "output" {
		return ClassGenerated, "a declared output glob claims it"
	}
	if role == "maintained" {
		return ClassGenerated, "magus maintains it outside any target"
	}
	if glob, origin, ok := MatchScopes(c.Prose, p); ok {
		return ClassProse, "matches " + quoteGlob(glob) + " (" + origin + ")"
	}
	// Comment-only detection needs the language's comment and string syntax, and every
	// language gets it the same way: a syntax the language's SPELL declared
	// (mgs_getCommentSyntax), consumed by one string-aware stripper, Go and Buzz
	// included, so "comment-only" means one thing. A language whose spell declared
	// nothing classifies as code: guessing delimiters would trade one false comment-only
	// for trust in every refusal after it.
	ext := strings.ToLower(path.Ext(p))
	if syn, ok := c.Syntax[ext]; ok {
		return c.commentOnly(ctx, p, rev, func(old, cur string) bool {
			return CommentOnlyDeclared(old, cur, syn)
		})
	}
	return ClassCode, "no comment syntax is declared for this language; classified as code"
}

func (c Classifier) commentOnly(ctx context.Context, p, rev string, equal func(old, cur string) bool) (Class, string) {
	if c.At == nil {
		return ClassCode, "this VCS backend cannot read the file at the revision compared against"
	}
	if c.Working == nil {
		return ClassCode, "the changed copy is unreadable"
	}
	old, err := c.At(ctx, rev, p)
	if err != nil {
		return ClassCode, "absent at the revision compared against"
	}
	cur, err := c.Working(p)
	if err != nil {
		return ClassCode, "gone from the working tree"
	}
	if equal(old, cur) {
		return ClassCommentOnly, "only comments differ from the revision compared against"
	}
	return ClassCode, "differs beyond comments from the revision compared against"
}

// Merge classifies a conflicted path a three-way merge settled: the edit from the merge
// base's content to the settled content, by the same classes Classify assigns. It is
// allowed when the edit is low risk, or when it is code a scope in optIn (MergeScopes)
// claims; the returned Classified says which, and why.
func (c Classifier) Merge(ctx context.Context, path, base, merged string, optIn []Scope) (Classified, bool) {
	c.At = func(context.Context, string, string) (string, error) { return base, nil }
	c.Working = func(string) (string, error) { return merged, nil }
	v := c.Classify(ctx, []string{path}, "").Paths[0]
	if v.LowRisk() {
		return v, true
	}
	if glob, origin, ok := MatchScopes(optIn, path); ok {
		return Classified{Path: path, Class: ClassCode, Why: "matches " + quoteGlob(glob) + " (" + origin + ")"}, true
	}
	return v, false
}

// quoteGlob quotes a glob for the attribution line.
func quoteGlob(glob string) string { return `"` + glob + `"` }

// CommentOnlyDeclared reports whether two sources of a declared language differ only in
// comments: both strip to byte-identical text. No token-stream normalization happens:
// whitespace may be semantics (Python indentation), so the only thing removed is the
// comment spans themselves. Reformatting a code line therefore classifies as code even in
// languages where it is inert, which is the safe direction.
func CommentOnlyDeclared(old, cur string, syn spells.CommentSyntax) bool {
	return StripComments(old, syn) == StripComments(cur, syn)
}

// StripComments returns src with its comment spans removed, using the language's declared
// syntax. It is a string-aware state machine: a comment token inside a declared string
// form is content, a string quote inside a comment is comment, and block comments nest
// only where declared. A DIRECTIVE comment (declared prefix on the comment body) is code
// and stays.
//
// A comment span includes the horizontal whitespace immediately before it, and, when the
// comment is the only thing on its line, the line itself, newline included. Indentation
// of code lines is never touched: that is the no-whitespace-normalization rule, and
// Python is why it exists.
func StripComments(src string, syn spells.CommentSyntax) string {
	quotes := slices.Clone(syn.Quotes)
	// Longest opener first, so `"""` wins over `"` and `r#"` over `r"`.
	slices.SortStableFunc(quotes, func(a, b spells.Quote) int { return len(b.Open) - len(a.Open) })

	var out strings.Builder
	out.Grow(len(src))
	i := 0
	lineHasCode := false
	for i < len(src) {
		if q, ok := openingQuote(src, i, quotes); ok {
			end := stringEnd(src, i+len(q.Open), q)
			out.WriteString(src[i:end])
			lineHasCode = true
			i = end
			continue
		}
		if opener, ok := openingToken(src, i, syn.LineComments); ok {
			end := lineEnd(src, i)
			if isDirective(src[i+len(opener):end], syn.Directives) {
				out.WriteString(src[i:end])
				lineHasCode = true
			} else {
				dropTrailingIndent(&out)
				if !lineHasCode && end < len(src) {
					end++ // the whole line was the comment; its newline goes too
				}
			}
			i = end
			continue
		}
		if open, closer, ok := openingBlock(src, i, syn.BlockComments); ok {
			end := blockEnd(src, i+len(open), open, closer, syn.Nested)
			if isDirective(src[i+len(open):end], syn.Directives) {
				out.WriteString(src[i:end])
				lineHasCode = true
			} else {
				dropTrailingIndent(&out)
				wholeLine := !lineHasCode && (end >= len(src) || src[end] == '\n')
				if wholeLine && end < len(src) {
					end++
				}
			}
			i = end
			continue
		}
		ch := src[i]
		out.WriteByte(ch)
		if ch == '\n' {
			lineHasCode = false
		} else if ch != ' ' && ch != '\t' && ch != '\r' {
			lineHasCode = true
		}
		i++
	}
	return out.String()
}

func openingQuote(src string, i int, quotes []spells.Quote) (spells.Quote, bool) {
	for _, q := range quotes {
		if strings.HasPrefix(src[i:], q.Open) {
			return q, true
		}
	}
	return spells.Quote{}, false
}

func openingToken(src string, i int, tokens []string) (string, bool) {
	for _, t := range tokens {
		if strings.HasPrefix(src[i:], t) {
			return t, true
		}
	}
	return "", false
}

func openingBlock(src string, i int, blocks []spells.CommentBlock) (open, closer string, ok bool) {
	for _, b := range blocks {
		if strings.HasPrefix(src[i:], b.Open) {
			return b.Open, b.Close, true
		}
	}
	return "", "", false
}

// stringEnd scans from just past the opener to just past the closer; an unterminated
// string runs to EOF, which strips deterministically on both sides of a comparison.
func stringEnd(src string, i int, q spells.Quote) int {
	for i < len(src) {
		if !q.IgnoreEscape && src[i] == '\\' {
			i += 2
			continue
		}
		if strings.HasPrefix(src[i:], q.Close) {
			return i + len(q.Close)
		}
		i++
	}
	return len(src)
}

func lineEnd(src string, i int) int {
	if n := strings.IndexByte(src[i:], '\n'); n >= 0 {
		return i + n
	}
	return len(src)
}

// blockEnd scans from just past the opener to just past the matching closer, honoring
// nesting where declared.
func blockEnd(src string, i int, open, closer string, nested bool) int {
	depth := 1
	for i < len(src) {
		if nested && strings.HasPrefix(src[i:], open) {
			depth++
			i += len(open)
			continue
		}
		if strings.HasPrefix(src[i:], closer) {
			i += len(closer)
			depth--
			if depth == 0 {
				return i
			}
			continue
		}
		i++
	}
	return len(src)
}

// isDirective reports whether a comment body opens with a declared directive prefix,
// after leading whitespace.
func isDirective(body string, directives []string) bool {
	body = strings.TrimLeft(body, " \t")
	for _, d := range directives {
		if strings.HasPrefix(body, d) {
			return true
		}
	}
	return false
}

// dropTrailingIndent removes the horizontal whitespace already written just before a
// stripped comment, so `x = 1  # note` strips to `x = 1`.
func dropTrailingIndent(out *strings.Builder) {
	s := out.String()
	end := len(s)
	for end > 0 && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	if end == len(s) {
		return
	}
	trimmed := s[:end]
	out.Reset()
	out.WriteString(trimmed)
}
