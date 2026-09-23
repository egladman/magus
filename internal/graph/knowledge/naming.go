package knowledge

import (
	"cmp"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egladman/magus/types"
)

// The conformance lens reports how a symbol a change adds, renames or re-signs sits against what
// the rest of the workspace already does with the same kind of declaration. Like the conformance
// advisor's target half, the norm is derived from the tree and never declared, so every finding
// states its counts.
//
// Silence is the default, and it is the hard part. A norm needs a cohort of at least MinCohort
// declarations agreeing at MinShare or more; a workspace that has not decided yields nothing. The
// change's own new declarations never count toward a norm, so a change cannot manufacture the
// convention it is then measured against.
const (
	conformanceMinCohort = 5
	conformanceMinShare  = 0.8
	// conformanceBar is the score a finding needs to be reported at all. Below it a finding is a
	// guess, and a guess shown beside facts teaches readers to skim both.
	conformanceBar = 0.5
	// conformanceMaxFindings caps what one change reports, strongest first.
	conformanceMaxFindings = 5
	conformanceExamples    = 3
)

// ConformanceChange describes the change [Graph.Conformance] observes.
type ConformanceChange struct {
	// Subjects are the symbol node IDs the change adds, renames or re-signs.
	Subjects []string
	// Introduced holds the symbol IDs new in this change. They never count toward a norm.
	Introduced map[string]bool
	// Generated holds the workspace-relative paths that are generated output. Generated and
	// test code never forms a norm and is never a subject.
	Generated map[string]bool
	// Renamed maps a subject to the name it replaced.
	Renamed map[string]string
	// Read returns a workspace file's text as the change leaves it. nil skips the one check that
	// searches text, for a renamed symbol's former name.
	Read func(path string) (string, bool)
	// MinCohort and MinShare are the silence gates: how many declarations a norm needs, and the
	// share of them that must agree. Zero takes the defaults, 5 and 0.8.
	MinCohort int
	MinShare  float64
}

// Conformance runs every conformance check over the change and returns what clears the bar,
// keyed by the symbol ID each finding is about: a subject, or the interface a subject method
// grows. At most conformanceMaxFindings are returned across the change, strongest first, ties
// broken by check name, message and subject so two runs over one graph agree byte for byte.
//
// Each finding is a [types.Check] with Status advice and Evidence inferred: a fact about the
// index, never a rule. It reads only language-neutral facts (name, kind, scope, calls,
// references, and the declaration shape a language's reader reports), reads a file only through
// ConformanceChange.Read, and never fails: a symbol it cannot place is skipped.
func (g *Graph) Conformance(c ConformanceChange) map[string][]types.Check {
	if len(c.Subjects) == 0 {
		return nil
	}
	x := newNamingIndex(g, c)
	type named struct {
		check string
		finding
	}
	var all []named
	for _, chk := range conformanceChecks {
		for _, f := range chk.run(x) {
			if f.score >= conformanceBar {
				all = append(all, named{chk.name(), f})
			}
		}
	}
	slices.SortFunc(all, func(a, b named) int {
		return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(a.check, b.check),
			cmp.Compare(a.message, b.message), cmp.Compare(a.subject, b.subject))
	})
	out := map[string][]types.Check{}
	for _, f := range all[:min(len(all), conformanceMaxFindings)] {
		out[f.subject] = append(out[f.subject], types.Check{
			Name: f.check, Status: types.CheckAdvice, Message: f.message, Details: f.details,
			Evidence: types.EvidenceInferred,
		})
	}
	return out
}

// conformanceCheck is one check [Graph.Conformance] runs. run reads the change's subjects off the
// index and returns every finding it can support with a score in [0, 1]; the caller applies the
// bar and the cap, so a check never decides how much of a change is worth reading.
type conformanceCheck interface {
	name() string
	run(x *namingIndex) []finding
}

var conformanceChecks = []conformanceCheck{
	affixCheck{}, collisionCheck{}, sizeCheck{},
	renameCheck{}, orderCheck{},
}

// finding is one scored observation about the declaration subject names.
type finding struct {
	subject string
	score   float64
	message string
	details []string
}

// namingDecl is one declaration the lens compares or counts: workspace-defined, and in neither
// test nor generated code.
type namingDecl struct {
	id, label, language, source string
	// scope is the namespace node ID; owner names the enclosing type of a member.
	scope, owner string
	ownerDecl    *namingDecl
	shape        declShape
	words        []word
	folded       []string
	// affixes caches the name's two-word runs: every subject of a shape scans the same members.
	affixes []affix
	// methods counts a type's method members.
	methods int
}

// word is one token of a name: its folded form for comparison and its byte span in the name.
type word struct {
	folded     string
	start, end int
}

// group is the key of the declarations a family forms within. Functions and methods compare
// across the language; a type or value idiom is local to its scope.
func (d *namingDecl) group() string {
	scope := ""
	switch d.shape.Kind {
	case declType, declInterface, declStruct, declValue:
		scope = d.scope
	}
	key := d.shape.Key
	if key == "" {
		key = d.shape.Kind
	}
	return d.language + "\x00" + scope + "\x00" + d.shape.Kind + "\x00" + key
}

func (d *namingDecl) callable() bool {
	return d.shape.Kind == declFunction || d.shape.Kind == declMethod
}

func (d *namingDecl) file() string {
	file, _, _ := strings.Cut(d.source, ":")
	return file
}

type namingIndex struct {
	g        *Graph
	change   ConformanceChange
	cohort   int
	share    float64
	byID     map[string]*namingDecl
	subjects []*namingDecl
	groups   map[string][]*namingDecl
	domain   []types.KnowledgeNode
	projects []string
	// callables are the functions and methods, in ID order.
	callables []*namingDecl
}

func newNamingIndex(g *Graph, c ConformanceChange) *namingIndex {
	x := &namingIndex{
		g:      g,
		change: c,
		cohort: cmp.Or(c.MinCohort, conformanceMinCohort),
		share:  cmp.Or(c.MinShare, conformanceMinShare),
		byID:   map[string]*namingDecl{},
		groups: map[string][]*namingDecl{},
	}
	// Members meet their owner on (scope, owner name) rather than on a rebuilt ID, which would
	// have to reproduce every indexer's escaping and method disambiguators exactly.
	type ownerKey struct{ scope, name string }
	owners := map[ownerKey]*namingDecl{}
	var members []*namingDecl
	for id, n := range g.nodes {
		switch n.Kind {
		case types.KindSymbol:
		case types.KindProject:
			x.projects = append(x.projects, n.Source)
			continue
		default:
			if slices.Contains(namingDomainKinds, n.Kind) {
				x.domain = append(x.domain, n)
			}
			continue
		}
		// Test and generated code is never a subject and never a norm, so it is not read at all.
		file, _, _ := strings.Cut(n.Source, ":")
		if file == "" || isTestSource(file) || x.generated(file) {
			continue
		}
		full, ok := parseDescriptors(id)
		if !ok {
			continue
		}
		local := afterNamespace(full)
		facts := symbolFacts{
			Kind: n.Attrs[attrSymbolKind], Signature: n.Attrs[AttrSignature],
			Path: local, Neutral: neutralKind(local),
		}
		if facts.Neutral == "" {
			continue
		}
		language := n.Attrs[attrLanguage]
		shape := readerFor(language).read(facts)
		// No check reads a field. A reader may still turn a `.` member into a method (an
		// indexer spelling an interface method that way), so this asks the shape.
		if shape.Kind == "" || shape.Kind == declField {
			continue
		}
		d := &namingDecl{
			id: id, label: n.Label, language: language, source: n.Source,
			scope: n.Attrs[attrNamespace], shape: shape, words: nameWords(n.Label),
		}
		if len(d.words) == 0 {
			continue
		}
		d.folded = foldedWords(d.words)
		x.byID[id] = d
		if len(local) == 2 {
			d.owner = local[0].Name
			members = append(members, d)
		} else {
			owners[ownerKey{d.scope, d.label}] = d
		}
		if d.shape.Visible {
			x.groups[d.group()] = append(x.groups[d.group()], d)
		}
		if d.callable() {
			x.callables = append(x.callables, d)
		}
	}
	for _, m := range members {
		if o, ok := owners[ownerKey{m.scope, m.owner}]; ok {
			m.ownerDecl = o
			if m.shape.Kind == declMethod {
				o.methods++
			}
		}
	}
	// Deterministic order: the graph is a map, and examples must not change between runs.
	for _, ds := range x.groups {
		slices.SortFunc(ds, func(a, b *namingDecl) int { return cmp.Compare(a.id, b.id) })
	}
	slices.SortFunc(x.callables, func(a, b *namingDecl) int { return cmp.Compare(a.id, b.id) })
	for _, id := range c.Subjects {
		if d, ok := x.byID[id]; ok && !slices.Contains(x.subjects, d) {
			x.subjects = append(x.subjects, d)
		}
	}
	return x
}

// generatedSuffixes name the files protocol and schema compilers write, whatever the workspace
// declares. Anything else generated is known only through ConformanceChange.Generated: a name
// like image_gen.go or a directory called gen is as often hand-written.
var generatedSuffixes = []string{".pb.go", "_pb.ts", "_pb.js", ".pb.ts", "_pb2.py", "_pb2_grpc.py", "_grpc.pb.go"}

// generated reports whether file is generated output, or in a directory that holds fixtures or
// dependencies rather than the workspace's own code.
func (x *namingIndex) generated(file string) bool {
	if x.change.Generated[file] {
		return true
	}
	base := path.Base(file)
	for _, s := range generatedSuffixes {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	for _, seg := range strings.Split(path.Dir(file), "/") {
		if seg == "testdata" || seg == "vendor" || seg == "node_modules" {
			return true
		}
	}
	return false
}

// peers returns the declarations beside subj that a norm is measured over: not subj, and not
// new in this change.
func (x *namingIndex) peers(ds []*namingDecl, subj *namingDecl) []*namingDecl {
	var out []*namingDecl
	for _, d := range ds {
		if d != subj && !x.change.Introduced[d.id] {
			out = append(out, d)
		}
	}
	return out
}

// affix is a leading or trailing two-word run a family shares.
type affix struct {
	words    string // the two folded words joined by a space
	trailing bool
}

func (a affix) pair() (string, string) {
	first, second, _ := strings.Cut(a.words, " ")
	return first, second
}

// runs returns the leading and trailing two-word runs of a name.
//
// Two words, not one: measured over this tree with every exported symbol treated as new, a
// one-word affix is almost always a noun (Spell<X>, <X>Result) whose position says what the
// name is about rather than which idiom it follows. The idioms worth keeping (FromContext,
// ContextWith) are composed.
func (d *namingDecl) runs() []affix {
	if d.affixes == nil && len(d.folded) >= 2 {
		n := len(d.folded)
		d.affixes = []affix{
			{words: d.folded[0] + " " + d.folded[1]},
			{words: d.folded[n-2] + " " + d.folded[n-1], trailing: true},
		}
	}
	return d.affixes
}

// carries reports whether a name holds the affix in the affix's position.
func carries(words []string, a affix) bool {
	if len(words) < 2 {
		return false
	}
	first, second := a.pair()
	at := 0
	if a.trailing {
		at = len(words) - 2
	}
	return words[at] == first && words[at+1] == second
}

// sharesAffixWord reports whether words holds either word of a.
func sharesAffixWord(words []string, a affix) bool {
	first, second := a.pair()
	return slices.Contains(words, first) || slices.Contains(words, second)
}

// affixCheck reports a visible name missing the word run (a leading or trailing pair of words)
// that most same-shape declarations sharing a word with it carry.
//
// Dominance is measured over the shape-mates that share a word with the affix, not over the
// whole shape, which mixes accessors with everything else that takes a context. A name sharing
// no word with an affix is a different thing and says nothing (NewVM(ctx) beside the
// FromContext family). A split idiom yields no family: ContextWithX setters beside as many
// WithX(ctx, ...) setters is a workspace that has not decided, and both spellings stay silent.
//
// Constants and variables are not compared. Their shape is their type, and `const string`
// groups a hint's id with the hint keys declared beside it: measured on this tree, that was the
// one false finding among its first three.
type affixCheck struct{}

func (affixCheck) name() string { return types.CheckNamingAffix }

func (affixCheck) run(x *namingIndex) []finding {
	var out []finding
	for _, subj := range x.subjects {
		if !subj.shape.Visible || subj.shape.Kind == declValue {
			continue
		}
		mine := subj.folded
		group := x.peers(x.groups[subj.group()], subj)
		carriersOf := map[affix][]*namingDecl{}
		for _, m := range group {
			for _, a := range m.runs() {
				if sharesAffixWord(mine, a) {
					carriersOf[a] = append(carriersOf[a], m)
				}
			}
		}
		for _, a := range slices.SortedFunc(maps.Keys(carriersOf), func(p, q affix) int {
			if c := cmp.Compare(p.words, q.words); c != 0 || p.trailing == q.trailing {
				return c
			}
			if p.trailing {
				return 1
			}
			return -1
		}) {
			carriers := carriersOf[a]
			if len(carriers) < x.cohort || carries(mine, a) {
				continue
			}
			want := strings.Split(a.words, " ")
			var outliers []*namingDecl
			for _, m := range group {
				if sharesAffixWord(m.folded, a) && !carries(m.folded, a) {
					outliers = append(outliers, m)
				}
			}
			cohort := len(carriers) + len(outliers)
			dominance := float64(len(carriers)) / float64(cohort)
			if dominance < x.share {
				continue
			}
			pattern, said := affixPattern(carriers[0], len(want), a.trailing)
			details := []string{"named " + pattern + ": " + strings.Join(labels(carriers), ", ")}
			if len(outliers) > 0 {
				details = append(details, "not: "+strings.Join(labels(outliers), ", "))
			}
			out = append(out, finding{
				subject: subj.id,
				score:   dominance * affinity(mine, want, a.trailing),
				message: fmt.Sprintf("`%s`: %d of %d %s shaped `%s` that say %s are named `%s`",
					subj.label, len(carriers), cohort, plural(subj.shape.Kind), shapeOrKind(subj),
					strings.Join(said, " or "), pattern),
				details: details,
			})
		}
	}
	return out
}

func shapeOrKind(d *namingDecl) string { return cmp.Or(d.shape.Key, d.shape.Kind) }

// affinity is how close a name comes to an affix: the share of the affix's words it holds, plus
// a quarter when its edge is a truncation of the affix (From of FromContext, or Context of it).
func affinity(words, affixWords []string, trailing bool) float64 {
	distinct := slices.Compact(slices.Sorted(slices.Values(affixWords)))
	held := 0
	for _, w := range distinct {
		if slices.Contains(words, w) {
			held++
		}
	}
	score := float64(held) / float64(len(distinct))
	for k := 1; k < len(affixWords) && k <= len(words); k++ {
		edge := words[:k]
		if trailing {
			edge = words[len(words)-k:]
		}
		if slices.Equal(edge, affixWords[:k]) || slices.Equal(edge, affixWords[len(affixWords)-k:]) {
			score += 0.25
			break
		}
	}
	return min(score, 1)
}

// affixPattern renders an affix as a carrier spells it (`<X>FromContext`, `New<X>`, `<X>_file`)
// and returns its words as spelled there.
func affixPattern(d *namingDecl, n int, trailing bool) (string, []string) {
	ws := d.words[:n]
	if trailing {
		ws = d.words[len(d.words)-n:]
	}
	said := make([]string, len(ws))
	for i, w := range ws {
		said[i] = d.label[w.start:w.end]
	}
	if trailing {
		start := ws[0].start
		for start > 0 {
			r, size := utf8.DecodeLastRuneInString(d.label[:start])
			if isWordRune(r) {
				break
			}
			start -= size
		}
		return "<X>" + d.label[start:], said
	}
	end := ws[n-1].end
	for end < len(d.label) {
		r, size := utf8.DecodeRuneInString(d.label[end:])
		if isWordRune(r) {
			break
		}
		end += size
	}
	return d.label[:end] + "<X>", said
}

// namingDomainKinds are the workspace entities whose names a symbol can reuse. An SDK mirrors
// CLI verbs on purpose, and only the author knows whether this name does, so a match is stated
// and never judged. A script host module is not one of them: it is named for the library area
// it wraps (json, http), which a type anywhere is expected to share, and on this tree both of
// its matches were that.
var namingDomainKinds = []string{
	types.KindTarget, types.KindSpell, types.KindOp, types.KindCharm, types.KindDiagnostic,
}

// collisionCheck reports a top-level name a workspace entity (a target, spell, op, charm or
// diagnostic) already carries. Only a symbol declared at the top level of a project is a
// subject: those are the names that travel, where one word meaning two things costs every
// reader. It states the match and asks nothing of the name itself.
type collisionCheck struct{}

func (collisionCheck) name() string { return types.CheckNameCollision }

func (collisionCheck) run(x *namingIndex) []finding {
	var out []finding
	for _, subj := range x.subjects {
		if !subj.shape.Visible || subj.owner != "" || !slices.Contains(x.projects, path.Dir(subj.file())) {
			continue
		}
		mine := subj.folded
		var hits []string
		for _, n := range x.domain {
			if !slices.Equal(foldedWords(nameWords(n.Label)), mine) {
				continue
			}
			hit := n.Kind + " `" + n.Label + "`"
			if n.Kind == types.KindTarget {
				hit = n.Kind + " `" + strings.TrimPrefix(n.ID, types.KindTarget+":") + "`"
			}
			if !slices.Contains(hits, hit) {
				hits = append(hits, hit)
			}
		}
		if len(hits) == 0 {
			continue
		}
		slices.Sort(hits)
		out = append(out, finding{
			subject: subj.id,
			score:   1,
			message: fmt.Sprintf("`%s` is also the name of %s in this workspace",
				subj.label, strings.Join(capList(hits, conformanceExamples), ", ")),
			details: hits,
		})
	}
	return out
}

// sizeCheck compares an interface's method count with the interfaces declared beside it, for an
// interface the change adds or grows. The bound is the smallest count MinShare of them stay
// within, so one large aggregate among many small capabilities does not set it. Only a language
// whose index reports interface members has counts to compare.
type sizeCheck struct{}

func (sizeCheck) name() string { return types.CheckInterfaceSize }

func (sizeCheck) run(x *namingIndex) []finding {
	var ifaces []*namingDecl
	for _, subj := range x.subjects {
		d := subj
		if d.shape.Kind == declMethod && d.ownerDecl != nil {
			d = d.ownerDecl
		}
		if d.shape.Kind == declInterface && d.shape.Visible && !slices.Contains(ifaces, d) {
			ifaces = append(ifaces, d)
		}
	}
	var out []finding
	for _, subj := range ifaces {
		peers := x.peers(x.groups[subj.group()], subj)
		if len(peers) < x.cohort {
			continue
		}
		counts := make([]int, len(peers))
		for i, p := range peers {
			counts[i] = p.methods
		}
		slices.Sort(counts)
		within := (len(counts)*int(x.share*100) + 99) / 100
		bound := counts[within-1]
		if subj.methods <= bound {
			continue
		}
		var small, large []*namingDecl
		for _, p := range peers {
			if p.methods <= bound {
				small = append(small, p)
			} else {
				large = append(large, p)
			}
		}
		pattern := fmt.Sprintf("%d methods or fewer", bound)
		if bound == 1 {
			pattern = "1 method"
		}
		details := []string{pattern + ": " + strings.Join(labels(small), ", ")}
		if len(large) > 0 {
			details = append(details, "larger: "+strings.Join(labels(large), ", "))
		}
		// Full weight only at twice the bound past it (or six methods past a small bound), so one
		// or two methods over stays below the bar.
		out = append(out, finding{
			subject: subj.id,
			score:   float64(len(small)) / float64(len(peers)) * min(1, float64(subj.methods-bound)/float64(2*max(bound, 3))),
			message: fmt.Sprintf("`%s` has %d methods; %d of %d interfaces declared beside it have %s",
				subj.label, subj.methods, len(small), len(peers), pattern),
			details: details,
		})
	}
	return out
}

// nameWords splits an identifier into words: camelCase, PascalCase, snake_case, kebab-case and
// dotted, with digits kept on the word before them. An initialism stays whole (HTTPServer is
// HTTP, Server), including its plural (SessionIDs is Session, IDs) and a single capital that
// leads a mixed-case word (OAuth, IPv6). Words are folded to lower case for comparison.
func nameWords(name string) []word {
	var out []word
	start := -1
	flush := func(end int) {
		if start >= 0 && end > start {
			out = append(out, word{folded: strings.ToLower(name[start:end]), start: start, end: end})
		}
		start = -1
	}
	for i := 0; i < len(name); {
		r, size := utf8.DecodeRuneInString(name[i:])
		switch {
		case !isWordRune(r):
			flush(i)
		case start < 0:
			start = i
		case unicode.IsUpper(r):
			prev, _ := utf8.DecodeLastRuneInString(name[:i])
			next, nextSize := utf8.DecodeRuneInString(name[i+size:])
			switch {
			case unicode.IsLower(prev) || unicode.IsDigit(prev):
				flush(i)
				start = i
			case unicode.IsUpper(prev) && unicode.IsLower(next):
				after, _ := utf8.DecodeRuneInString(name[i+size+nextSize:])
				plural := next == 's' && !unicode.IsLower(after)
				single := utf8.RuneCountInString(name[start:i]) == 1
				if !plural && !single {
					flush(i)
					start = i
				}
			}
		}
		i += size
	}
	flush(len(name))
	return out
}

// isWordRune reports whether r can be part of a name word; anything else separates words.
func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// isIdentRune reports whether r can be part of an identifier in the languages the index covers:
// a word rune, or the underscore and dollar sign that join words without separating the name.
func isIdentRune(r rune) bool { return isWordRune(r) || r == '_' || r == '$' }

// IndexIdentifier returns the byte index of the first place text holds name as a whole
// identifier, not inside a longer one (EntryPoint is not in EntryPointFrom or myEntryPoint), or
// -1 when it holds none. An empty name is never found.
func IndexIdentifier(text, name string) int {
	if name == "" {
		return -1
	}
	for from := 0; ; {
		i := strings.Index(text[from:], name)
		if i < 0 {
			return -1
		}
		start, end := from+i, from+i+len(name)
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		after, _ := utf8.DecodeRuneInString(text[end:])
		if (start == 0 || !isIdentRune(before)) && (end == len(text) || !isIdentRune(after)) {
			return start
		}
		from = start + 1
	}
}

// RenamedFrom returns the name one of removed spells where the added line spells name, with the
// rest of the line unchanged: the declaration on that line was renamed in place. It returns ""
// when no removed line fits, or when two fit with different names.
func RenamedFrom(added, name string, removed []string) string {
	at := IndexIdentifier(added, name)
	if at < 0 {
		return ""
	}
	prefix, suffix := added[:at], added[at+len(name):]
	old := ""
	for _, r := range removed {
		if len(r) <= len(prefix)+len(suffix) || !strings.HasPrefix(r, prefix) || !strings.HasSuffix(r, suffix) {
			continue
		}
		cand := r[len(prefix) : len(r)-len(suffix)]
		if cand == name || strings.IndexFunc(cand, func(r rune) bool { return !isIdentRune(r) }) >= 0 ||
			IndexIdentifier(r, cand) != len(prefix) {
			continue
		}
		if old != "" && old != cand {
			return ""
		}
		old = cand
	}
	return old
}

func foldedWords(ws []word) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.folded
	}
	return out
}

func plural(kind string) string {
	switch kind {
	case declFunction:
		return "functions"
	case declMethod:
		return "methods"
	case declInterface:
		return "interfaces"
	case declStruct:
		return "structs"
	case declValue:
		return "values"
	case declField:
		return "fields"
	}
	return "types"
}

// labels names up to conformanceExamples declarations, distinct and sorted.
func labels(ds []*namingDecl) []string {
	var out []string
	for _, d := range ds {
		if !slices.Contains(out, d.label) {
			out = append(out, d.label)
		}
	}
	slices.Sort(out)
	return capList(out, conformanceExamples)
}

func capList(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return xs[:n:n]
}
