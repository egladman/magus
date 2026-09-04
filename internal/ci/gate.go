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
	"path"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
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
	// Syntax routes a file extension (lowercase, with dot) to the comment
	// syntax a spell DECLARED for it (spells.CommentSyntaxIndex over the
	// projects' resolved spells). An unclaimed extension classifies as code.
	Syntax map[string]spells.CommentSyntax
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
	// Comment-only detection needs the language's comment and string syntax,
	// and every language gets it the same way: a syntax the language's SPELL
	// declared (mgs_getCommentSyntax), consumed by one string-aware stripper -
	// Go and Buzz included, so "comment-only" means one thing. A language
	// whose spell declared nothing classifies as code - guessing delimiters
	// would trade one false comment-only for trust in every refusal after it.
	ext := strings.ToLower(path.Ext(p))
	if syn, ok := c.Syntax[ext]; ok {
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

// CommentOnlyDeclared reports whether two sources of a declared language
// differ only in comments: both strip to byte-identical text. No token-stream
// normalization happens - whitespace may be semantics (Python indentation),
// so the only thing removed is the comment spans themselves. Reformatting a
// code line therefore re-gates even in languages where it is inert, which is
// the safe direction.
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

func openingBlock(src string, i int, blocks []spells.CommentBlock) (open, closer string, ok bool) {
	for _, b := range blocks {
		if strings.HasPrefix(src[i:], b.Open) {
			return b.Open, b.Close, true
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

// InheritOff reports whether any project declared gate_inherit false, the
// workspace's off-switch for CI verdict inheritance. One declaration turns it
// off workspace-wide, the same reach a gate_low_risk declaration has.
func InheritOff(projects []*types.Project) bool {
	return slices.ContainsFunc(projects, func(p *types.Project) bool { return p.GateInheritOff })
}

// PRMergeFreeRange is MergeFreeRange with the head commit exempt from the
// merge rule. A CI provider tests a pull request as a synthetic merge of the
// branch into its base (GitHub's refs/pull/N/merge), so under a PR checkout
// the head is ALWAYS a merge, and it is the harness's rather than the
// delta's: the classification diffs the tree it produced against green, so
// everything it folded in is accounted for. A merge anyone pushed sits below
// the synthetic head and still refuses.
func PRMergeFreeRange(history []types.Commit, green string) bool {
	if len(history) == 0 {
		return false
	}
	if history[0].ID == green {
		return true
	}
	return MergeFreeRange(history[1:], green)
}

// InheritFinding is a fired verdict-inheritance decision: the green run being
// inherited, its head commit, and the classified delta a reader disputes it by.
type InheritFinding struct {
	Run    string
	Commit string
	Delta  GateDelta
}

// InheritProbe gathers the CI verdict-inheritance inputs. Dependencies are
// functions for ChangeClassifier's reason: the decision is testable with a
// stubbed provider and no repository.
type InheritProbe struct {
	// Disabled: the workspace declared gate_inherit false; nothing is probed.
	Disabled bool
	// LastGreenRun asks the CI provider for the branch/PR's newest green run
	// of this same pipeline. ok=false is the ordinary no-answer case.
	LastGreenRun func(ctx context.Context) (run, commit string, ok bool)
	// History lists commits from HEAD, newest first, deep enough to reach a
	// green run worth inheriting.
	History func(ctx context.Context) ([]types.Commit, error)
	// Changed lists the paths whose content differs between the working tree
	// and the green commit.
	Changed    func(ctx context.Context, green string) ([]string, error)
	Classifier ChangeClassifier
}

// Evaluate decides. ok=false means the plan proceeds exactly as it would
// have before this feature existed, with no output at all; ok=true carries
// the full finding, because an inherited verdict is never a silent skip.
func (p InheritProbe) Evaluate(ctx context.Context) (InheritFinding, bool) {
	if p.Disabled || p.LastGreenRun == nil || p.History == nil || p.Changed == nil {
		return InheritFinding{}, false
	}
	run, commit, ok := p.LastGreenRun(ctx)
	if !ok || commit == "" {
		return InheritFinding{}, false
	}
	history, err := p.History(ctx)
	if err != nil || !PRMergeFreeRange(history, commit) {
		return InheritFinding{}, false
	}
	changed, err := p.Changed(ctx, commit)
	if err != nil {
		return InheritFinding{}, false
	}
	delta := p.Classifier.Classify(ctx, changed, commit)
	if !delta.LowRiskOnly() {
		return InheritFinding{}, false
	}
	return InheritFinding{Run: run, Commit: commit, Delta: delta}, true
}

// AnnotationText renders the finding for a CI annotation: the inherited run,
// its commit, and every changed path with its class, so the annotation alone
// lets a reader reconstruct and dispute the decision.
func (f InheritFinding) AnnotationText() string {
	var b strings.Builder
	b.WriteString("verdict inherited from run " + f.Run + " (commit " + shortRev(f.Commit) +
		"): every path changed since that green run classifies low-risk")
	if len(f.Delta.Paths) == 0 {
		b.WriteString("\nno paths changed since that run")
		return b.String()
	}
	for _, line := range f.Delta.Lines() {
		b.WriteString("\n" + line)
	}
	return b.String()
}

// SummaryMarkdown renders the finding for the workflow's job summary, under
// the same explicitness contract: every file, its class, and what classified
// it - never a count.
func (f InheritFinding) SummaryMarkdown() string {
	var b strings.Builder
	b.WriteString("### Inherited verdict\n\n")
	b.WriteString("The shard fan-out was short-circuited: every path changed since this " +
		"branch's last green CI run classifies low-risk, so that run's verdict stands.\n\n")
	b.WriteString("Inherited run: " + f.Run + " at commit `" + shortRev(f.Commit) + "`.\n\n")
	if len(f.Delta.Paths) == 0 {
		b.WriteString("No paths changed since that run.\n")
		return b.String()
	}
	b.WriteString("| Changed path | Class | Classified by |\n| --- | --- | --- |\n")
	for _, p := range f.Delta.Paths {
		b.WriteString("| `" + p.Path + "` | " + p.Class.String() + " | " + p.Why + " |\n")
	}
	b.WriteString("\nTo dispute a row, its last column names the declaration or mechanism " +
		"that classified it; to turn inheritance off, declare `gate_inherit = false` in magus.project.\n")
	return b.String()
}

// shortRev abbreviates a commit id for the inheritance report.
func shortRev(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
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
