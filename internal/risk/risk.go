// Package risk is magus's one classifier of change risk. Each changed path gets a class
// (generated output, prose, a comment-only edit, or code), and Assess lifts the classes,
// with what language provers can prove, into a tier: how much of the gate the change
// needs. The ci gate's redundancy check, CI verdict inheritance, job completion and
// `magus affected ci`'s gate sizing read the tier; a merge settles a conflicted path by
// its class, or, for two jobs that claimed disjoint declarations of it, by those claims
// (MergeByClaims).
//
// The classes rest on the workspace's declarations. A path a declared output glob or
// magus itself owns is generated; a path an effective gate_low_risk glob claims is prose
// (magus ships markdown defaults, see ProseScopes); a file whose delta touches only
// comments, by the comment syntax its language's spell declares, is comment-only. Every
// other path, and every path the classifier cannot read, is code.
package risk

import (
	"cmp"
	"context"
	"fmt"
	"io/fs"
	"path"
	"regexp"
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
func (c Classified) Line() string { return fmt.Sprintf("%s: %s (%s)", c.Path, c.Class.String(), c.Why) }

// Delta is the per-path verdict over one delta.
type Delta struct {
	Paths []Classified
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
			out[i] = fmt.Sprintf("%s/%s", s.Dir, g)
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
		return ClassProse, fmt.Sprintf("matches %s (%s)", quoteGlob(glob), origin)
	}
	// Comment-only detection needs the language's comment and string syntax, and every
	// language gets it the same way: a syntax the language's SPELL declared
	// (the comments field of its mgs_getLanguage record), consumed by one string-aware stripper, Go and Buzz
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
		return Classified{Path: path, Class: ClassCode, Why: fmt.Sprintf("matches %s (%s)", quoteGlob(glob), origin)}, true
	}
	return v, false
}

// Edit classifies one path's edit from old to cur by the classes Classify assigns. A nil
// side is the path absent there, so adding or deleting a file is never comment-only.
func (c Classifier) Edit(ctx context.Context, path string, old, cur []byte) Classified {
	read := func(content []byte) (string, error) {
		if content == nil {
			return "", fs.ErrNotExist
		}
		return string(content), nil
	}
	c.At = func(context.Context, string, string) (string, error) { return read(old) }
	c.Working = func(string) (string, error) { return read(cur) }
	return c.Classify(ctx, []string{path}, "").Paths[0]
}

// quoteGlob quotes a glob for the attribution line.
func quoteGlob(glob string) string { return fmt.Sprintf("%c%s%c", '"', glob, '"') }

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
	var out strings.Builder
	out.Grow(len(src))
	prev := 0
	for _, s := range commentSpans(src, syn) {
		out.WriteString(src[prev:s.start])
		dropTrailingIndent(&out)
		end := s.end
		wholeLine := s.ownLine && (s.closer == "" || end >= len(src) || src[end] == '\n')
		if wholeLine && end < len(src) {
			end++ // the whole line was the comment; its newline goes too
		}
		prev = end
	}
	out.WriteString(src[prev:])
	return out.String()
}

// commentSpan is one non-directive comment of a source: src[start:end] from its opener
// through its closer, or to the end of the line for a line comment (closer "").
type commentSpan struct {
	start, end     int
	opener, closer string
	// ownLine reports that nothing but whitespace precedes the opener on its line.
	ownLine bool
}

// commentSpans is the string-aware state machine StripComments documents, reporting the
// spans it would strip instead of stripping them.
func commentSpans(src string, syn spells.CommentSyntax) []commentSpan {
	quotes := slices.Clone(syn.Quotes)
	// Longest opener first, so `"""` wins over `"` and `r#"` over `r"`.
	slices.SortStableFunc(quotes, func(a, b spells.Quote) int { return len(b.Open) - len(a.Open) })

	var spans []commentSpan
	i := 0
	lineHasCode := false
	for i < len(src) {
		if q, ok := openingQuote(src, i, quotes); ok {
			i = stringEnd(src, i+len(q.Open), q)
			lineHasCode = true
			continue
		}
		if opener, ok := openingToken(src, i, syn.LineComments); ok {
			end := lineEnd(src, i)
			if isDirective(src[i+len(opener):end], syn.Directives) {
				lineHasCode = true
			} else {
				spans = append(spans, commentSpan{start: i, end: end, opener: opener, ownLine: !lineHasCode})
			}
			i = end
			continue
		}
		if open, closer, ok := openingBlock(src, i, syn.BlockComments); ok {
			end := blockEnd(src, i+len(open), open, closer, syn.Nested)
			if isDirective(src[i+len(open):end], syn.Directives) {
				lineHasCode = true
			} else {
				spans = append(spans, commentSpan{start: i, end: end, opener: open, closer: closer, ownLine: !lineHasCode})
			}
			i = end
			continue
		}
		switch src[i] {
		case '\n':
			lineHasCode = false
		case ' ', '\t', '\r':
		default:
			lineHasCode = true
		}
		i++
	}
	return spans
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

// OverlappingClaims returns the first pair of claimed declarations, one from each list,
// that can name one declaration: equal, or one naming the other by types.NamesDeclaration.
// ok is false when the two lists are disjoint.
func OverlappingClaims(ours, theirs []string) (a, b string, ok bool) {
	for _, x := range ours {
		for _, y := range theirs {
			if x == y || types.NamesDeclaration(x, y) || types.NamesDeclaration(y, x) {
				return x, y, true
			}
		}
	}
	return "", "", false
}

// ClaimSide is one side of a merge: its file content, and the declaration halves of the
// `path#declaration` write paths of the jobs whose work it carries.
type ClaimSide struct {
	Content []byte
	Claims  []string
}

// MergeByClaims merges ours and theirs, both descended from base, for sides whose jobs
// claimed disjoint declarations of file.
//
// An edit only one side made merges as that side has it, wherever it is. Where the two
// sides' edits touch or overlap, which is where a line merge gives up:
//   - each side's edit must sit in declarations its own claims name;
//   - import lines either side changed in the file's preamble merge as a set;
//   - two pure insertions at one base line keep ours, then theirs (types.MergeBothAdded);
//   - any other overlap does not settle.
//
// The result's Regions name each place the sides' edits touched and settled, as
// types.MergeByClaim, types.MergeBothAdded or types.MergeImportSet. It returns an error
// naming the region for every case it does not settle, and for a path no diff driver
// places or input types.Hunks cannot diff.
func MergeByClaims(file string, base []byte, ours, theirs ClaimSide) (types.MergeResolution, error) {
	driver, ok := types.MatchDiffDriver(file)
	if !ok {
		return types.MergeResolution{}, fmt.Errorf("%s: no diff driver places its declarations", file)
	}
	ea, okA := sideEdits(file, base, ours.Content, sideOurs)
	eb, okB := sideEdits(file, base, theirs.Content, sideTheirs)
	if !okA || !okB {
		return types.MergeResolution{}, fmt.Errorf("%s: binary, or too far from the merge base to merge by line", file)
	}
	m := claimMerger{
		path:   file,
		driver: driver,
		o:      types.SplitLines(base),
		sides:  [2][]string{types.SplitLines(ours.Content), types.SplitLines(theirs.Content)},
		claims: [2][]string{ours.Claims, theirs.Claims},
		edits:  slices.Concat(ea, eb),
	}
	m.declsO = driver.Declarations(m.o)
	m.decls = [2][]string{driver.Declarations(m.sides[0]), driver.Declarations(m.sides[1])}
	slices.SortStableFunc(m.edits, func(x, y claimEdit) int {
		return cmp.Or(cmp.Compare(x.bs, y.bs), cmp.Compare(x.be, y.be), cmp.Compare(x.side, y.side))
	})

	var out []string
	pos := 0
	for _, c := range m.clusters() {
		start, end := c[0].bs, clusterEnd(c)
		out = append(out, m.o[pos:start]...)
		lines, err := m.settle(c, start, end)
		if err != nil {
			return types.MergeResolution{}, err
		}
		out = append(out, lines...)
		pos = end
	}
	out = append(out, m.o[pos:]...)
	return types.MergeResolution{Content: []byte(strings.Join(out, "")), Regions: m.regions}, nil
}

// mergeSide is which of the two merged revisions an edit came from; it indexes the
// per-side arrays of claimMerger.
type mergeSide uint8

const (
	sideOurs mergeSide = iota
	sideTheirs
)

func (s mergeSide) String() string {
	if s == sideOurs {
		return "ours"
	}
	return "theirs"
}

// claimEdit is one run a side changed: base lines [bs, be) became the side's [ss, se).
type claimEdit struct {
	side   mergeSide
	bs, be int
	ss, se int
}

// sideEdits reads side's edits to base off the edit script types.Hunks computes, so the
// runs are the ones types.MergeThreeWay aligns. With no driver, Hunks reports each changed
// run whole: the deleted run, then the run inserted in its place.
func sideEdits(file string, base, side []byte, which mergeSide) ([]claimEdit, bool) {
	regions, ok := types.Hunks(file, base, side, types.DiffDriver{})
	if !ok {
		return nil, false
	}
	var out []claimEdit
	i, j := 0, 0 // the base and side line just past the last edit
	for _, r := range regions {
		lo, hi := r.Lines[0]-1, r.Lines[1]
		if r.Side == types.RegionOld {
			j += lo - i
			out = append(out, claimEdit{side: which, bs: lo, be: hi, ss: j, se: j})
			i = hi
			continue
		}
		// Inserted at the side line a deletion left off at, it replaces that deletion; any
		// later line has a kept line between the two.
		if n := len(out); n > 0 && out[n-1].be == i && out[n-1].ss == j && out[n-1].se == j && lo == j {
			out[n-1].se = hi
		} else {
			i += lo - j
			out = append(out, claimEdit{side: which, bs: i, be: i, ss: lo, se: hi})
		}
		j = hi
	}
	return out, true
}

type claimMerger struct {
	path    string
	driver  types.DiffDriver
	o       []string
	declsO  []string
	sides   [2][]string
	decls   [2][]string
	claims  [2][]string
	edits   []claimEdit
	regions []types.MergeRegion
}

// clusters groups the sorted edits into runs that overlap in base: a shared base line, or
// insertions at one line. Edits that only meet end to end stay apart, since each applies
// at its own place.
func (m *claimMerger) clusters() [][]claimEdit {
	var out [][]claimEdit
	for _, e := range m.edits {
		if n := len(out); n > 0 {
			c := out[n-1]
			end := clusterEnd(c)
			if e.bs < end || (e.bs == e.be && c[0].bs == end && e.bs == end) {
				out[n-1] = append(c, e)
				continue
			}
		}
		out = append(out, []claimEdit{e})
	}
	return out
}

func clusterEnd(c []claimEdit) int {
	end := c[0].be
	for _, e := range c[1:] {
		end = max(end, e.be)
	}
	return end
}

// contested reports whether the other side has an edit touching or overlapping e: the
// edits a line merge stops at.
func (m *claimMerger) contested(e claimEdit) bool {
	return slices.ContainsFunc(m.edits, func(f claimEdit) bool {
		return f.side != e.side && e.bs <= f.be && f.bs <= e.be
	})
}

// settle is the merged lines for one cluster spanning base [start, end).
func (m *claimMerger) settle(c []claimEdit, start, end int) ([]string, error) {
	if len(c) == 1 {
		e := c[0]
		if m.contested(e) && !m.imports(c) {
			if err := m.claimed(e); err != nil {
				return nil, err
			}
			m.note(m.location(e), types.MergeByClaim)
		}
		return m.sides[e.side][e.ss:e.se], nil
	}
	if m.imports(c) {
		m.note(types.Location{Path: m.path, Declaration: types.Preamble}, types.MergeImportSet)
		return importSet(m.o[start:end], m.render(c, sideOurs, start, end), m.render(c, sideTheirs, start, end)), nil
	}
	if start == end {
		for _, e := range c {
			if err := m.claimed(e); err != nil {
				return nil, err
			}
		}
		m.note(m.location(c[0]), types.MergeBothAdded)
		return slices.Concat(m.render(c, sideOurs, start, end), m.render(c, sideTheirs, start, end)), nil
	}
	return nil, fmt.Errorf("%s: both sides changed the same lines", m.location(c[0]))
}

// render is base [start, end) with one side's edits in c applied.
func (m *claimMerger) render(c []claimEdit, side mergeSide, start, end int) []string {
	var out []string
	pos := start
	for _, e := range c {
		if e.side != side {
			continue
		}
		out = append(out, m.o[pos:e.bs]...)
		out = append(out, m.sides[side][e.ss:e.se]...)
		pos = e.be
	}
	return append(out, m.o[pos:end]...)
}

// claimed reports the first declaration e reaches that its side's claims do not name.
func (m *claimMerger) claimed(e claimEdit) error {
	for _, d := range m.reached(e) {
		if d == "" || !slices.ContainsFunc(m.claims[e.side], func(c string) bool { return types.NamesDeclaration(c, d) }) {
			return fmt.Errorf("%s: %s changed it, and none of that side's claims names it", types.Location{Path: m.path, Declaration: orPreamble(d)}, e.side)
		}
	}
	return nil
}

// reached is every declaration enclosing a line e removed or added, in order.
func (m *claimMerger) reached(e claimEdit) []string {
	return slices.Compact(slices.Concat(m.declsO[e.bs:e.be], m.decls[e.side][e.ss:e.se]))
}

// imports reports whether every line the edits in c removed or added is an import line
// in the preamble, by the import syntax of the path's driver.
func (m *claimMerger) imports(c []claimEdit) bool {
	re := importLines[m.driver.Name]
	if re == nil {
		return false
	}
	for _, e := range c {
		if slices.ContainsFunc(m.reached(e), func(d string) bool { return d != "" }) {
			return false
		}
		for _, l := range slices.Concat(m.o[e.bs:e.be], m.sides[e.side][e.ss:e.se]) {
			if !re.MatchString(l) {
				return false
			}
		}
	}
	return true
}

func (m *claimMerger) note(loc types.Location, kind types.MergeKind) {
	m.regions = append(m.regions, types.MergeRegion{Locations: []types.Location{loc}, Kind: kind})
}

func (m *claimMerger) location(e claimEdit) types.Location {
	d := m.reached(e)
	if len(d) == 0 {
		return types.Location{Path: m.path, Declaration: types.Preamble}
	}
	return types.Location{Path: m.path, Declaration: orPreamble(d[0])}
}

func orPreamble(decl string) string {
	if decl == "" {
		return types.Preamble
	}
	return decl
}

// importLines matches one import line of each driver's language. Only one-line forms are
// here, so the opening and closing lines of a block (Go's `import (`, Python's
// parenthesized from) never match, and an edit to one stays a conflict.
var importLines = map[string]*regexp.Regexp{
	"golang":     regexp.MustCompile(`^\s*(import\s+)?([\w.]+\s+)?"[^"]*"\s*$`),
	"python":     regexp.MustCompile(`^(import\s+[\w.]+(\s+as\s+\w+)?(\s*,\s*[\w.]+(\s+as\s+\w+)?)*|from\s+[\w.]+\s+import\s+[\w*]+(\s+as\s+\w+)?(\s*,\s*\w+(\s+as\s+\w+)?)*)\s*$`),
	"rust":       regexp.MustCompile(`^(pub(\([^)]*\))?\s+)?use\s+[^;]+;\s*$`),
	"typescript": regexp.MustCompile(`^import\s+(type\s+)?([^;'"]*\s+from\s+)?['"][^'"]+['"];?\s*$`),
	"buzz":       regexp.MustCompile(`^import\s+"[^"]+"(\s+as\s+\w+)?;\s*$`),
}

// importSet merges import lines as a set: ours's lines without the ones theirs removed
// from base, then the ones theirs added. When both sides kept their lines sorted, so does
// the result, the order gofmt and most import sorters keep.
func importSet(o, a, b []string) []string {
	out := slices.DeleteFunc(slices.Clone(a), func(l string) bool {
		return slices.Contains(o, l) && !slices.Contains(b, l)
	})
	for _, l := range b {
		if !slices.Contains(o, l) && !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	if slices.IsSorted(a) && slices.IsSorted(b) {
		slices.Sort(out)
	}
	return out
}
