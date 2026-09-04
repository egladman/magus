// Gate redundancy: whether a ci gate run would re-verify what a recorded
// green gate already verified, so the CLI can defer it when the machine is
// loaded instead of queuing a duplicate.
//
// The decision has three inputs, each computed by the caller: whether an
// identical-or-equivalent gate already passed for this branch (Redundant),
// whether the machine admission pool would queue this run (Saturated), and
// whether the caller asked to skip the check (Forced). A redundant gate under
// load is REFUSED with exit 75, the same fast-refusal shape MAGUS_NO_WAIT
// uses; a redundant gate on an idle machine runs anyway behind an advisory,
// because the daemon holding the load signal is an accelerant, never a
// capability gate. A gate that is not redundant always runs, silently.
//
// "Equivalent" means every path changed since the green gate's commit falls in
// one of exactly three low-risk classes: generated output (declared output
// globs and magus-maintained files, structural and not configurable), prose
// (globs; magus ships markdown defaults, and magus.project's gate_low_risk key
// replaces or empties them), and comment-only edits (a token- or
// declaration-driven comparison, not a glob). A merge commit in the range is
// never equivalent, however clean: a merge is the moment two verified
// histories combine into a tree neither gate ever saw, so it always re-gates.
//
// A deferral is never a success: the command either fully runs, or refuses
// with exit 75. There is no silent-skip-exit-0 state, and every refusal and
// advisory prints the full decision - the green gate matched, every changed
// path with its class and the declaration that classified it, and the pool
// state - so a reader can reconstruct and dispute it from the message alone.

package ci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"go/scanner"
	"go/token"
	"path"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	buzztoken "github.com/egladman/magus/libs/gopherbuzz/token"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// GateDecision is what the gate does about a redundancy finding.
type GateDecision int

const (
	// GateRun executes the gate with nothing printed.
	GateRun GateDecision = iota
	// GateAdvise prints the finding and executes the gate anyway.
	GateAdvise
	// GateRefuse does not execute; the refusal names the green gate and exits 75.
	GateRefuse
)

// GateFacts are what DecideGate combines. Zero value decides GateRun.
type GateFacts struct {
	// Redundant: a green gate is on record for this branch with an identical
	// input fingerprint, or with a delta that classifies entirely low-risk.
	Redundant bool
	// Saturated: the machine admission pool would queue this run.
	Saturated bool
	// Forced: the caller passed the override flag; the check is off.
	Forced bool
	// Nested: this magus runs under another one. It never refuses, because the
	// pool it reads counts its own ancestors' claims as load.
	Nested bool
}

// DecideGate is the decision matrix. It is a pure function so the matrix is
// testable without a store, a daemon, or a repository.
func DecideGate(f GateFacts) GateDecision {
	if f.Forced || !f.Redundant {
		return GateRun
	}
	if f.Saturated && !f.Nested {
		return GateRefuse
	}
	return GateAdvise
}

// PoolSaturated reports whether a new run would queue against the machine
// budget: something is already waiting, or an axis with a limit is fully
// held. A nil snapshot is an absent arbiter and reads as idle (fail open).
func PoolSaturated(m *types.MachineSnapshot) bool {
	if m == nil {
		return false
	}
	if len(m.Waiters) > 0 {
		return true
	}
	if m.BudgetSlots > 0 && m.HeldSlots >= m.BudgetSlots {
		return true
	}
	return m.BudgetMB > 0 && m.HeldMB >= m.BudgetMB
}

// GateStep is one (project, target) step's live cache key, as
// Magus.ComputeTargetKey mints it.
type GateStep struct {
	Project string
	Target  string
	Key     string
}

// GateFingerprint condenses a gate's per-step cache keys into one identity.
// Derived from the step-key machinery rather than a fresh tree hash, so it
// inherits everything a real run keys on: sources, spell claims, tool
// versions, the env allowlist, and the charm set. Order-insensitive; an empty
// selection fingerprints to "".
func GateFingerprint(steps []GateStep) string {
	if len(steps) == 0 {
		return ""
	}
	lines := make([]string, len(steps))
	for i, s := range steps {
		lines[i] = s.Project + "\x00" + s.Target + "\x00" + s.Key
	}
	slices.Sort(lines)
	h := sha256.New()
	for _, l := range lines {
		_, _ = h.Write([]byte(l))
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// MergeFreeRange reports whether walking history (newest first) from its head
// down to green crosses no merge commit. green at the head is a trivially
// merge-free range. A green commit the walk never reaches reports false: a
// range that cannot be bounded cannot be vouched for.
func MergeFreeRange(history []types.Commit, green string) bool {
	for _, c := range history {
		if c.ID == green {
			return true
		}
		if len(c.Parents) > 1 {
			return false
		}
	}
	return false
}

// ChangeClass is the risk class of one changed path.
type ChangeClass int

const (
	// ClassCode is everything the three low-risk classes do not cover.
	ClassCode ChangeClass = iota
	// ClassGenerated is a path a declared output glob or magus itself owns.
	ClassGenerated
	// ClassProse is a path an effective gate_low_risk glob claims; magus ships
	// markdown defaults (see DefaultProseGlobs).
	ClassProse
	// ClassCommentOnly is a file whose delta touches only comments.
	ClassCommentOnly
)

// String is the word the per-path verdict lines print.
func (c ChangeClass) String() string {
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

// ClassifiedPath is one changed path's class and the fact it rests on, so a
// reader can dispute the decision from the message alone.
type ClassifiedPath struct {
	Path  string
	Class ChangeClass
	// Why names what classified it: the claiming role, the matching glob and
	// its origin, or the comment-comparison outcome.
	Why string
}

// GateDelta is the per-path verdict over one delta.
type GateDelta struct {
	Paths []ClassifiedPath
}

// LowRiskOnly reports whether every classified path avoided ClassCode. An
// empty delta is low-risk: nothing changed since the green gate.
func (d GateDelta) LowRiskOnly() bool {
	return !slices.ContainsFunc(d.Paths, func(v ClassifiedPath) bool { return v.Class == ClassCode })
}

// Lines renders one verdict line per path - every file, never a summary,
// because the refusal's reader must be able to reconstruct the decision.
func (d GateDelta) Lines() []string {
	out := make([]string, len(d.Paths))
	for i, v := range d.Paths {
		out[i] = v.Path + ": " + v.Class.String() + " (" + v.Why + ")"
	}
	return out
}

// DefaultProseGlobs are the prose globs magus ships: markdown sources, which
// covers docs and changelog prose. They apply only while NO project declares
// gate_low_risk; the first declaration replaces them workspace-wide.
var DefaultProseGlobs = []string{"**/*.md", "**/*.markdown"}

// ProseOriginDefault names the built-in glob set in per-path attributions.
const ProseOriginDefault = "built-in default"

// ProseScope is one source of prose globs: the built-in default set, or one
// project's gate_low_risk declaration. Globs match the way review_required's
// do - relative to the declaring project's directory - so an author names
// files the same way their sources and outputs already do.
type ProseScope struct {
	// Dir is the declaring project's workspace-relative path; "" or "." matches
	// against the whole workspace-relative path.
	Dir string
	// Globs are doublestar patterns relative to Dir.
	Globs []string
	// Origin is what the per-path attribution names: ProseOriginDefault, or
	// `gate_low_risk of project <path>`.
	Origin string
}

// ProseScopes resolves the workspace's effective prose globs. No declaration
// anywhere means the shipped defaults; ANY declaration replaces them with the
// union of declared scopes, so a workspace that declares `[]` and nothing else
// has turned the prose class off entirely.
func ProseScopes(projects []*types.Project) []ProseScope {
	var scopes []ProseScope
	declared := false
	for _, p := range projects {
		if !p.GateLowRiskDeclared {
			continue
		}
		declared = true
		if len(p.GateLowRisk) > 0 {
			scopes = append(scopes, ProseScope{Dir: p.Path, Globs: p.GateLowRisk, Origin: "gate_low_risk of project " + p.Path})
		}
	}
	if !declared {
		return []ProseScope{{Globs: DefaultProseGlobs, Origin: ProseOriginDefault}}
	}
	return scopes
}

// matchProse returns the first scope glob matching the workspace-relative
// path, with its origin, or ok=false when no prose glob claims it.
func matchProse(scopes []ProseScope, p string) (glob, origin string, ok bool) {
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

// ChangeClassifier classifies the paths changed since a green gate. Its
// dependencies are functions rather than the workspace types that provide
// them, so the class table is testable against literal content.
type ChangeClassifier struct {
	// Role returns each path's describe-file role ("output", "maintained",
	// "source", ...), keyed by the path as passed. Missing entries classify by
	// the remaining classes alone.
	Role func(ctx context.Context, paths []string) (map[string]string, error)
	// Prose is the effective glob set, from ProseScopes. Empty means the prose
	// class is off.
	Prose []ProseScope
	// At returns a file's content at the green gate's revision. An error means
	// the path did not exist there or cannot be read; the path reads as code.
	At func(ctx context.Context, rev, path string) (string, error)
	// Working returns a file's current working-tree content.
	Working func(path string) (string, error)
}

// Classify assigns each changed path its risk class against green, the commit
// the recorded gate passed at, and the reason a reader disputes it by. Every
// failure to classify (an unreadable revision, a file that does not lex) lands
// the path in ClassCode: the gate runs.
func (c ChangeClassifier) Classify(ctx context.Context, paths []string, green string) GateDelta {
	out := GateDelta{Paths: make([]ClassifiedPath, len(paths))}
	roles := map[string]string{}
	if c.Role != nil {
		if r, err := c.Role(ctx, paths); err == nil {
			roles = r
		}
	}
	for i, p := range paths {
		class, why := c.classify(ctx, p, roles[p], green)
		out.Paths[i] = ClassifiedPath{Path: p, Class: class, Why: why}
	}
	return out
}

func (c ChangeClassifier) classify(ctx context.Context, p, role, green string) (ChangeClass, string) {
	if role == "output" {
		return ClassGenerated, "a declared output glob claims it"
	}
	if role == "maintained" {
		return ClassGenerated, "magus maintains it outside any target"
	}
	if glob, origin, ok := matchProse(c.Prose, p); ok {
		return ClassProse, "matches " + quoteGlob(glob) + " (" + origin + ")"
	}
	// Comment-only detection needs the language's comment and string syntax.
	// Go and Buzz use the real lexers magus owns; every other language is
	// covered only by a DECLARED syntax (spells.CommentSyntaxForExtension),
	// consumed by one string-aware stripper. A language that declared nothing
	// classifies as code - guessing delimiters would trade one false
	// comment-only for trust in every refusal after it.
	ext := strings.ToLower(path.Ext(p))
	switch ext {
	case ".go":
		return c.commentOnly(ctx, p, green, CommentOnlyGo)
	case ".buzz":
		return c.commentOnly(ctx, p, green, CommentOnlyBuzz)
	}
	if syn, ok := spells.CommentSyntaxForExtension(ext); ok {
		return c.commentOnly(ctx, p, green, func(old, cur string) bool {
			return CommentOnlyDeclared(old, cur, syn)
		})
	}
	return ClassCode, "no comment syntax is declared for this language; classified as code"
}

func (c ChangeClassifier) commentOnly(ctx context.Context, p, green string, equal func(old, cur string) bool) (ChangeClass, string) {
	if c.At == nil {
		return ClassCode, "this VCS backend cannot read the file at the green gate's revision"
	}
	if c.Working == nil {
		return ClassCode, "the working tree copy is unreadable"
	}
	old, err := c.At(ctx, green, p)
	if err != nil {
		return ClassCode, "absent at the green gate's revision"
	}
	cur, err := c.Working(p)
	if err != nil {
		return ClassCode, "gone from the working tree"
	}
	if equal(old, cur) {
		return ClassCommentOnly, "only comments differ from the green gate's revision"
	}
	return ClassCode, "differs beyond comments from the green gate's revision"
}

// quoteGlob quotes a glob for the attribution line.
func quoteGlob(glob string) string { return `"` + glob + `"` }

// CommentOnlyGo reports whether two Go sources differ only in comments (and
// whitespace, which carries no meaning once tokenized). Both sides are lexed
// with the standard scanner; equal token streams mean equal programs. A
// DIRECTIVE comment (//go:build, //go:generate, //go:embed, //nolint, and the
// rest of the no-space-then-colon convention) is code, not comment: it stays
// in the stream, so editing one is never comment-only. A source that does not
// lex cleanly reports false, so a broken file always re-gates.
func CommentOnlyGo(old, cur string) bool {
	a, ok := goTokens(old)
	if !ok {
		return false
	}
	b, ok := goTokens(cur)
	if !ok {
		return false
	}
	return slices.Equal(a, b)
}

func goTokens(src string) ([]string, bool) {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	broken := false
	s.Init(file, []byte(src), func(token.Position, string) { broken = true }, scanner.ScanComments)
	var out []string
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.COMMENT && !goDirective(lit) {
			continue
		}
		out = append(out, tok.String()+"\x00"+lit)
	}
	return out, !broken
}

// goDirective reports whether a Go comment is a compiler or tool directive.
// The convention (gofmt preserves these verbatim) is a line comment whose text
// starts immediately after the slashes - no space - with a word carrying a
// colon; //nolint and cgo's //export lack the colon and are named explicitly.
func goDirective(lit string) bool {
	rest, ok := strings.CutPrefix(lit, "//")
	if !ok {
		return false
	}
	if rest == "" || rest[0] == ' ' || rest[0] == '\t' {
		return false
	}
	word, _, _ := strings.Cut(rest, " ")
	return strings.Contains(word, ":") || word == "nolint" || word == "export"
}

// CommentOnlyBuzz is CommentOnlyGo for Buzz. The gopherbuzz lexer never emits
// a comment token; a leading block only annotates the next token's Doc field,
// which the comparison ignores along with positions. A comment inside a string
// interpolation is part of the interpolated expression's source and compares
// as content, which errs toward re-gating.
func CommentOnlyBuzz(old, cur string) bool {
	a, err := buzztoken.Tokenize(old)
	if err != nil {
		return false
	}
	b, err := buzztoken.Tokenize(cur)
	if err != nil {
		return false
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !buzzTokenEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

func buzzTokenEqual(a, b buzztoken.Token) bool {
	if a.Kind != b.Kind || a.Val != b.Val || a.Raw != b.Raw || len(a.Parts) != len(b.Parts) {
		return false
	}
	for i := range a.Parts {
		if a.Parts[i] != b.Parts[i] {
			return false
		}
	}
	return true
}

// CommentOnlyDeclared reports whether two sources of a declared language
// differ only in comments: both strip to byte-identical text. No token-stream
// normalization happens here, unlike the Go and Buzz comparators - a declared
// language's whitespace may be semantics (Python indentation), so the only
// thing removed is the comment spans themselves.
func CommentOnlyDeclared(old, cur string, syn spells.CommentSyntax) bool {
	return StripComments(old, syn) == StripComments(cur, syn)
}

// StripComments returns src with its comment spans removed, using the
// language's declared syntax. It is a string-aware state machine: a comment
// token inside a declared string form is content, a string quote inside a
// comment is comment, and block comments nest only where declared. A
// DIRECTIVE comment (declared prefix on the comment body) is code and stays.
//
// A comment span includes the horizontal whitespace immediately before it,
// and, when the comment is the only thing on its line, the line itself -
// newline included. Indentation of code lines is never touched: that is the
// no-whitespace-normalization rule, and Python is why it exists.
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

func openingBlock(src string, i int, blocks [][2]string) (open, closer string, ok bool) {
	for _, b := range blocks {
		if strings.HasPrefix(src[i:], b[0]) {
			return b[0], b[1], true
		}
	}
	return "", "", false
}

// stringEnd scans from just past the opener to just past the closer; an
// unterminated string runs to EOF, which strips deterministically on both
// sides of a comparison.
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

// blockEnd scans from just past the opener to just past the matching closer,
// honoring nesting where declared.
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

// isDirective reports whether a comment body opens with a declared directive
// prefix, after leading whitespace.
func isDirective(body string, directives []string) bool {
	body = strings.TrimLeft(body, " \t")
	for _, d := range directives {
		if strings.HasPrefix(body, d) {
			return true
		}
	}
	return false
}

// dropTrailingIndent removes the horizontal whitespace already written just
// before a stripped comment, so `x = 1  # note` strips to `x = 1`.
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
