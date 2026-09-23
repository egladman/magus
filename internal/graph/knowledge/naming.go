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

// The naming lens reports a name a change adds that differs from what the rest of the workspace
// calls the same shape of thing. Like the conformance advisor's target half, the norm is derived
// from the tree and never declared, so every finding carries the counts that make it.
//
// A family is (language, scope, declaration shape, affix): an affix is a leading or trailing run
// of two name words that at least namingMinCohort same-shape declarations carry. Its dominance is
// measured over the shape-mates that share a word with the affix, not over the whole shape, which
// mixes accessors with everything else that takes a context: here 33 of the 35 functions shaped
// `func(ctx) value` that say From or Context end in FromContext, and that is the convention.
//
// Silence is the default. A name sharing no word with an affix is a different thing and says
// nothing (NewVM(ctx) beside the FromContext family). A split idiom yields no family: 9
// ContextWithX setters beside 10 WithX(ctx, ...) setters is a workspace that has not decided, so
// a new ContextWithEntryPoint and a new WithEntryPoint are both silent, which is correct rather
// than a miss.
const (
	namingMinCohort = 5
	namingMinShare  = 0.8
	// namingHeadline is the score a finding needs to lead; below it, down to namingEvidence, it
	// is ranked evidence that is shown and never asserted.
	namingHeadline = 0.5
	namingEvidence = 0.25
	// namingMaxHeadlines caps the findings one change leads with, best first.
	namingMaxHeadlines = 5
	// namingCommonWord is how many declarations may end in a type's name before the name is too
	// common to mean one thing (Path, File, Run).
	namingCommonWord = 12
	namingExamples   = 3
)

// namingDomainKinds are the workspace entities whose names a symbol can reuse. An exact match is
// asked about outright: the SDK mirrors CLI verbs on purpose, and only the author knows whether
// this one does.
var namingDomainKinds = []string{
	types.KindTarget, types.KindSpell, types.KindOp, types.KindCharm, types.KindModule, types.KindDiagnostic,
}

// NamingChange describes the change [Graph.Naming] observes.
type NamingChange struct {
	// Subjects are the symbol node IDs the change adds or re-signs.
	Subjects []string
	// Introduced reports whether a symbol is new in this change. A family half or more of whose
	// members are new is a convention being born, not one being broken, and says nothing. nil
	// treats every symbol as established.
	Introduced func(id string) bool
	// Generated reports whether a workspace-relative path is generated output. Generated and test
	// code never forms a family or carries a word. nil leaves only the path conventions (a gen/
	// directory, a _gen or .gen file).
	Generated func(path string) bool
}

// Naming observes each subject against the naming families, word bindings and interface sizes
// the rest of the graph's symbol index shows, keyed by subject ID. A subject with nothing to say
// is absent. Findings for one subject are best first; across the change, at most
// namingMaxHeadlines scoring namingHeadline or more are marked Headline.
//
// It reads only language-neutral facts (name, kind, scope, and the declaration shape a language's
// reader reports), never reads a file, and never fails: a symbol it cannot place is skipped.
func (g *Graph) Naming(c NamingChange) map[string][]types.DiffNaming {
	if len(c.Subjects) == 0 {
		return nil
	}
	x := newNamingIndex(g, c)
	out := map[string][]types.DiffNaming{}
	for _, id := range c.Subjects {
		d, ok := x.byID[id]
		if !ok || !d.shape.Visible {
			continue
		}
		found := slices.Concat(x.affixFindings(d), x.sizeFindings(d), x.bindingFindings(d))
		if len(found) == 0 {
			continue
		}
		slices.SortStableFunc(found, func(a, b types.DiffNaming) int { return cmp.Compare(b.Score, a.Score) })
		out[id] = found
	}
	markHeadlines(out)
	return out
}

// markHeadlines marks the strongest findings across the change, at most namingMaxHeadlines.
func markHeadlines(all map[string][]types.DiffNaming) {
	type ref struct {
		id string
		i  int
	}
	var ranked []ref
	for id, fs := range all {
		for i, f := range fs {
			if f.Score >= namingHeadline {
				ranked = append(ranked, ref{id, i})
			}
		}
	}
	slices.SortFunc(ranked, func(a, b ref) int {
		if c := cmp.Compare(all[b.id][b.i].Score, all[a.id][a.i].Score); c != 0 {
			return c
		}
		return cmp.Or(cmp.Compare(a.id, b.id), cmp.Compare(a.i, b.i))
	})
	for _, r := range ranked[:min(len(ranked), namingMaxHeadlines)] {
		all[r.id][r.i].Headline = true
	}
}

// namingDecl is one declaration the lens compares or counts.
type namingDecl struct {
	id, label, language, source string
	// scope is the namespace node ID; owner names the enclosing type of a member.
	scope, owner string
	shape        declShape
	words        []word
	// methods and fields count a type's members; a member counts toward its owner.
	methods, fields int
	// eligible marks a workspace-defined, non-test, non-generated declaration: a family member
	// or a subject. Anything else is at most a carrier of a word.
	eligible bool
}

// word is one token of a name: its folded form for comparison and where it sits in the name.
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

func (d *namingDecl) folded() []string {
	out := make([]string, len(d.words))
	for i, w := range d.words {
		out[i] = w.folded
	}
	return out
}

type namingIndex struct {
	change   NamingChange
	byID     map[string]*namingDecl
	groups   map[string][]*namingDecl
	byWord   map[string][]*namingDecl // head word (singular) -> every carrier, eligible or not
	headings map[string][]string      // word (singular) -> doc section headings carrying it
	domain   []types.KnowledgeNode
	projects []string
}

func newNamingIndex(g *Graph, c NamingChange) *namingIndex {
	x := &namingIndex{
		change:   c,
		byID:     map[string]*namingDecl{},
		groups:   map[string][]*namingDecl{},
		byWord:   map[string][]*namingDecl{},
		headings: map[string][]string{},
	}
	// Members meet their owner on (scope, owner name) rather than on a rebuilt ID, which would
	// have to reproduce every indexer's escaping and method disambiguators exactly.
	type ownerKey struct{ scope, name string }
	owners := map[ownerKey]*namingDecl{}
	var members []*namingDecl
	for id, n := range g.nodes {
		switch n.Kind {
		case types.KindSymbol:
		case types.KindDocSection:
			for _, w := range nameWords(n.Label) {
				s := singular(w.folded)
				if !slices.Contains(x.headings[s], n.Label) {
					x.headings[s] = append(x.headings[s], n.Label)
				}
			}
			continue
		case types.KindProject:
			x.projects = append(x.projects, n.Source)
			continue
		default:
			if slices.Contains(namingDomainKinds, n.Kind) {
				x.domain = append(x.domain, n)
			}
			continue
		}
		if n.Source == "" {
			continue
		}
		full, ok := parseDescriptors(id)
		if !ok {
			continue
		}
		local := afterNamespace(full)
		facts := symbolFacts{
			Label: n.Label, Kind: n.Attrs[attrSymbolKind], Signature: n.Attrs[AttrSignature],
			Path: local, Neutral: neutralKind(local),
		}
		if facts.Neutral == "" {
			continue
		}
		language := n.Attrs[attrLanguage]
		d := &namingDecl{
			id: id, label: n.Label, language: language, source: n.Source,
			scope: n.Attrs[attrNamespace], shape: readerFor(language).read(facts),
			words: nameWords(n.Label),
		}
		if d.shape.Kind == "" || len(d.words) == 0 {
			continue
		}
		file, _, _ := strings.Cut(n.Source, ":")
		d.eligible = !isTestSource(file) && !x.generated(file)
		x.byID[id] = d
		if len(local) == 2 {
			d.owner = local[0].Name
			members = append(members, d)
		} else {
			owners[ownerKey{d.scope, d.label}] = d
		}
		if d.eligible && d.shape.Visible && d.shape.Kind != declField {
			x.groups[d.group()] = append(x.groups[d.group()], d)
		}
		head := singular(d.words[len(d.words)-1].folded)
		x.byWord[head] = append(x.byWord[head], d)
	}
	for _, m := range members {
		if o, ok := owners[ownerKey{m.scope, m.owner}]; ok {
			if m.shape.Kind == declMethod {
				o.methods++
			} else {
				o.fields++
			}
		}
	}
	// Deterministic order: the graph is a map, and examples must not change between runs.
	for _, ds := range x.groups {
		slices.SortFunc(ds, func(a, b *namingDecl) int { return cmp.Compare(a.id, b.id) })
	}
	for _, ds := range x.byWord {
		slices.SortFunc(ds, func(a, b *namingDecl) int { return cmp.Compare(a.id, b.id) })
	}
	for _, hs := range x.headings {
		slices.Sort(hs)
	}
	return x
}

// generated applies the path conventions every language's generators share, then the caller's
// declared outputs.
func (x *namingIndex) generated(file string) bool {
	base := path.Base(file)
	if strings.Contains(base, "_gen.") || strings.Contains(base, ".gen.") || strings.Contains(base, "_pb.") || strings.Contains(base, ".pb.") {
		return true
	}
	for _, seg := range strings.Split(path.Dir(file), "/") {
		if seg == "gen" || seg == "generated" || seg == "testdata" || seg == "vendor" || seg == "node_modules" {
			return true
		}
	}
	return x.change.Generated != nil && x.change.Generated(file)
}

func (x *namingIndex) introduced(id string) bool {
	return x.change.Introduced != nil && x.change.Introduced(id)
}

// affix is a leading or trailing word run a family shares.
type affix struct {
	words    string // folded words joined by a space
	trailing bool
}

func (a affix) list() []string { return strings.Split(a.words, " ") }

// runs returns the leading and trailing two-word runs of a name.
//
// Two words, not one: measured over this tree with every exported symbol treated as new, a
// one-word affix is almost always a noun (Spell<X>, <X>Result) whose position says what the
// name is about rather than which idiom it follows, and those were most of the headlines. The
// idioms worth keeping (FromContext, ContextWith) are composed.
func runs(words []string) []affix {
	if len(words) < 2 {
		return nil
	}
	return []affix{
		{words: strings.Join(words[:2], " ")},
		{words: strings.Join(words[len(words)-2:], " "), trailing: true},
	}
}

// carries reports whether a name holds the affix in the affix's position.
func carries(words []string, a affix) bool {
	want := a.list()
	if len(words) < len(want) {
		return false
	}
	if a.trailing {
		return slices.Equal(words[len(words)-len(want):], want)
	}
	return slices.Equal(words[:len(want)], want)
}

func sharesWord(words, with []string) bool {
	for _, w := range words {
		if slices.Contains(with, w) {
			return true
		}
	}
	return false
}

// affixFindings scores the subject against every family of its shape that shares a word with it.
func (x *namingIndex) affixFindings(subj *namingDecl) []types.DiffNaming {
	if !subj.eligible {
		return nil
	}
	mine := subj.folded()
	group := x.groups[subj.group()]
	carriersOf := map[affix][]*namingDecl{}
	for _, m := range group {
		if m == subj {
			continue
		}
		seen := map[affix]bool{}
		for _, a := range runs(m.folded()) {
			if !seen[a] && sharesWord(a.list(), mine) {
				seen[a] = true
				carriersOf[a] = append(carriersOf[a], m)
			}
		}
	}
	var out []types.DiffNaming
	for a, carriers := range carriersOf {
		if len(carriers) < namingMinCohort || carries(mine, a) {
			continue
		}
		want := a.list()
		// The change's own names are what is being judged, so none of them counts against the
		// convention.
		var outliers []*namingDecl
		for _, m := range group {
			if m != subj && !x.introduced(m.id) && sharesWord(m.folded(), want) && !carries(m.folded(), a) {
				outliers = append(outliers, m)
			}
		}
		cohort := len(carriers) + len(outliers)
		dominance := float64(len(carriers)) / float64(cohort)
		if dominance < namingMinShare || x.born(carriers) {
			continue
		}
		score := dominance * affinity(mine, want, a.trailing)
		if score < namingEvidence {
			continue
		}
		pattern, said := affixPattern(carriers[0], len(want), a.trailing)
		out = append(out, types.DiffNaming{
			Check: types.DiffNamingAffix,
			Summary: fmt.Sprintf("`%s`: %d of %d %s of its shape that say %s are named `%s`",
				subj.label, len(carriers), cohort, plural(subj.shape.Kind), strings.Join(said, " or "), pattern),
			Score:    round2(score),
			Pattern:  pattern,
			Shape:    subj.shape.Key,
			Members:  len(carriers),
			Cohort:   cohort,
			Examples: labels(carriers),
			Outliers: labels(outliers),
		})
	}
	return out
}

// born reports whether half or more of a family is new in this change.
func (x *namingIndex) born(members []*namingDecl) bool {
	n := 0
	for _, m := range members {
		if x.introduced(m.id) {
			n++
		}
	}
	return n*2 >= len(members)
}

// affinity is how close a name comes to an affix: the share of the affix's words it holds, plus
// a quarter when its edge is a truncation of the affix (From of FromContext, or Context of it).
func affinity(words, affixWords []string, trailing bool) float64 {
	held := 0
	for _, w := range dedupe(affixWords) {
		if slices.Contains(words, w) {
			held++
		}
	}
	score := float64(held) / float64(len(dedupe(affixWords)))
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
		said[i] = w.text(d.label)
	}
	if trailing {
		start := ws[0].start
		for start > 0 && !isWordRune(d.label[start-1]) {
			start--
		}
		return "<X>" + d.label[start:], said
	}
	end := ws[n-1].end
	for end < len(d.label) && !isWordRune(d.label[end]) {
		end++
	}
	return d.label[:end] + "<X>", said
}

func isWordRune(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// sizeFindings compares an interface's method count with the interfaces declared beside it. The
// bound is the smallest count namingMinShare of them stay within, so one large aggregate among
// many small capabilities does not set it.
func (x *namingIndex) sizeFindings(subj *namingDecl) []types.DiffNaming {
	if subj.shape.Kind != declInterface || !subj.eligible {
		return nil
	}
	var peers []*namingDecl
	for _, m := range x.groups[subj.group()] {
		if m != subj {
			peers = append(peers, m)
		}
	}
	if len(peers) < namingMinCohort || x.born(peers) {
		return nil
	}
	counts := make([]int, len(peers))
	for i, p := range peers {
		counts[i] = p.methods
	}
	slices.Sort(counts)
	within := (len(counts)*int(namingMinShare*100) + 99) / 100
	bound := counts[within-1]
	if subj.methods <= bound {
		return nil
	}
	var small, large []*namingDecl
	for _, p := range peers {
		if p.methods <= bound {
			small = append(small, p)
		} else {
			large = append(large, p)
		}
	}
	// Full weight only at twice the bound past it (or six methods past a small bound), so one or
	// two methods over is evidence and not a headline.
	share := float64(len(small)) / float64(len(peers))
	score := share * min(1, float64(subj.methods-bound)/float64(2*max(bound, 3)))
	if score < namingEvidence {
		return nil
	}
	pattern := fmt.Sprintf("%d methods or fewer", bound)
	if bound == 1 {
		pattern = "1 method"
	}
	return []types.DiffNaming{{
		Check: types.DiffNamingSize,
		Summary: fmt.Sprintf("`%s` has %d methods; %d of %d interfaces declared beside it have %s",
			subj.label, subj.methods, len(small), len(peers), pattern),
		Score:    round2(score),
		Pattern:  pattern,
		Shape:    subj.shape.Key,
		Members:  len(small),
		Cohort:   len(peers),
		Examples: labels(small),
		Outliers: labels(large),
	}}
}

// bindingFindings looks the subject's name up among what the workspace already calls it. Only a
// project's root and types scopes are subjects: those are the names that travel (the SDK and the
// domain vocabulary), where one word meaning two things costs every reader.
func (x *namingIndex) bindingFindings(subj *namingDecl) []types.DiffNaming {
	if !subj.eligible || !x.travels(subj.source) {
		return nil
	}
	switch subj.shape.Kind {
	case declMethod, declField:
		return nil
	}
	mine := subj.folded()
	var hits []string
	for _, n := range x.domain {
		if slices.Equal(foldedWords(nameWords(n.Label)), mine) {
			hits = append(hits, n.Kind+" "+domainLabel(n))
		}
	}
	if len(hits) > 0 {
		slices.Sort(hits)
		return []types.DiffNaming{{
			Check: types.DiffNamingBinding,
			Summary: fmt.Sprintf("`%s` is already the name of %s in this workspace; say whether this mirrors it or means something else",
				subj.label, strings.Join(capList(hits, namingExamples), ", ")),
			Score:    1,
			Pattern:  subj.label,
			Members:  len(hits),
			Cohort:   len(hits),
			Examples: capList(hits, namingExamples),
		}}
	}
	return x.wordFindings(subj)
}

// wordFindings compares the meaning a new type gives its name with the meanings the
// declarations whose names end in the same words already give it. A heading carrying the words
// is counted and shown but never sets the dominant sense: it names a topic, not a declaration a
// reader could mistake this one for.
func (x *namingIndex) wordFindings(subj *namingDecl) []types.DiffNaming {
	switch subj.shape.Kind {
	case declType, declInterface, declStruct:
	default:
		return nil
	}
	mine := singularTail(subj.folded())
	var symbols []*namingDecl
	for _, d := range x.byWord[mine[len(mine)-1]] {
		if d == subj || !d.eligible || x.introduced(d.id) || !hasSuffix(singularTail(d.folded()), mine) {
			continue
		}
		switch d.shape.Kind {
		case declType, declInterface, declStruct, declField:
			symbols = append(symbols, d)
		}
	}
	if len(symbols) < 2 || len(symbols) > namingCommonWord {
		return nil
	}
	senses := map[string][]string{}
	for _, d := range symbols {
		senses[meaningOf(d)] = append(senses[meaningOf(d)], qualifiedLabel(d))
	}
	dominant, most := "", 0
	for _, s := range slices.Sorted(maps.Keys(senses)) {
		slices.Sort(senses[s])
		if len(senses[s]) > most {
			dominant, most = s, len(senses[s])
		}
	}
	if meaningOf(subj) == dominant {
		return nil
	}
	for _, h := range x.headings[mine[len(mine)-1]] {
		if containsRun(singularAll(foldedWords(nameWords(h))), mine) {
			senses["heading"] = append(senses["heading"], "\""+h+"\"")
		}
	}
	total := len(symbols) + len(senses["heading"])
	if total < 3 {
		return nil
	}
	order := slices.Sorted(maps.Keys(senses))
	score := float64(most) / float64(total)
	if score < namingEvidence {
		return nil
	}
	var examples []string
	for _, s := range order {
		examples = append(examples, s+": "+strings.Join(capList(senses[s], 2), ", "))
	}
	noun := "senses"
	if len(senses) == 1 {
		noun = "sense"
	}
	return []types.DiffNaming{{
		Check: types.DiffNamingBinding,
		Summary: fmt.Sprintf("`%s` reuses a name %d other things here already carry in %d %s; say which this is",
			subj.label, total, len(senses), noun),
		Score:    round2(score),
		Pattern:  subj.label,
		Members:  most,
		Cohort:   total,
		Examples: examples,
	}}
}

// travels reports whether a file sits in a project's root directory or its types directory.
func (x *namingIndex) travels(source string) bool {
	file, _, _ := strings.Cut(source, ":")
	dir := path.Dir(file)
	for _, p := range x.projects {
		if dir == p || dir == path.Join(p, "types") {
			return true
		}
	}
	return false
}

func meaningOf(d *namingDecl) string {
	switch {
	case d.shape.Kind == declField && d.shape.Meaning != "":
		return "field of " + d.shape.Meaning
	case d.shape.Kind == declField:
		return "field"
	case d.shape.Meaning != "":
		return d.shape.Meaning
	}
	return d.shape.Kind
}

// qualifiedLabel names a member through its owner, `hookAttribution.Transport`.
func qualifiedLabel(d *namingDecl) string {
	if d.owner == "" {
		return d.label
	}
	return d.owner + "." + d.label
}

func domainLabel(n types.KnowledgeNode) string {
	if n.Kind == types.KindTarget {
		return "`" + strings.TrimPrefix(n.ID, types.KindTarget+":") + "`"
	}
	return "`" + n.Label + "`"
}

func (w word) text(label string) string { return label[w.start:w.end] }

// nameWords splits an identifier into words: camelCase, PascalCase, snake_case, kebab-case and
// dotted, keeping an initialism whole (HTTPServer is HTTP, Server) and digits with the word
// before them. Words are folded to lower case for comparison.
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
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush(i)
		case start < 0:
			start = i
		case unicode.IsUpper(r):
			prev, _ := utf8.DecodeLastRuneInString(name[:i])
			next, _ := utf8.DecodeRuneInString(name[i+size:])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && unicode.IsLower(next)) {
				flush(i)
				start = i
			}
		}
		i += size
	}
	flush(len(name))
	return out
}

func foldedWords(ws []word) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.folded
	}
	return out
}

// singular folds a plural so a heading's "transports" meets a symbol's Transport.
func singular(w string) string {
	if len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") {
		return w[:len(w)-1]
	}
	return w
}

// singularTail folds the head word of a name, the one a plural lands on.
func singularTail(ws []string) []string {
	out := slices.Clone(ws)
	if len(out) > 0 {
		out[len(out)-1] = singular(out[len(out)-1])
	}
	return out
}

func singularAll(ws []string) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = singular(w)
	}
	return out
}

func hasSuffix(ws, suffix []string) bool {
	return len(ws) >= len(suffix) && slices.Equal(ws[len(ws)-len(suffix):], suffix)
}

func containsRun(ws, run []string) bool {
	for i := 0; i+len(run) <= len(ws); i++ {
		if slices.Equal(ws[i:i+len(run)], run) {
			return true
		}
	}
	return false
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

// labels names up to namingExamples declarations, distinct and sorted.
func labels(ds []*namingDecl) []string {
	var out []string
	for _, d := range ds {
		if !slices.Contains(out, d.label) {
			out = append(out, d.label)
		}
	}
	slices.Sort(out)
	return capList(out, namingExamples)
}

func capList(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return xs[:n:n]
}

func dedupe(xs []string) []string {
	var out []string
	for _, x := range xs {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
