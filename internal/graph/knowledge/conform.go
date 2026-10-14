package knowledge

import (
	"cmp"
	"maps"
	"math"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/types"
)

// Conform measures every conform metric over g, one row per metric, population and scope,
// and judges each row against the value o.Effective enforces: the committed ratchet value,
// else the configured anchor. A live percentile is reported and may be proposed, never
// enforced. A key with no configured value is measured only: no violations, no proposal.
// The mode-knobs row holds only the functions the coupling rule finds over-shared, so its n
// is how many there are, zero included.
//
// Rows come back sorted by metric, population and scope, and every list inside them in a
// fixed order, so two runs over one graph agree byte for byte. A graph with no symbols
// answers unknown rather than an empty pass. A language with no BodyShape lexer
// contributes no clone samples, and one with no package clause no main population. It
// reads g only and writes nothing.
func (g *Graph) Conform(o types.ConformOptions) types.ConformOutput {
	if !g.HasSymbols() {
		return types.ConformOutput{Answer: types.ClassifyAnswer(false, types.ReasonSymbolIndexMissing, nil)}
	}
	samples := conformSamples(g, o)
	loops, knobbed := sharedLoops(g, o)
	maps.Copy(samples, loops)
	samples[couplingKey(types.ConformModeKnobs)] = knobbed
	maps.Copy(samples, aliasSamples(g, o))
	groups := cloneGroups(g, o)
	known := conformKnownItems(o.Ratchet.Known)
	// past holds, per metric, every item some enforced row found past its value: a known
	// item outside it is stale.
	past := map[types.ConformMetric]map[string]bool{}
	mark := func(m types.ConformMetric, id string) {
		if past[m] == nil {
			past[m] = map[string]bool{}
		}
		past[m][id] = true
	}

	out := types.ConformOutput{Excluded: conformExcluded(g, o)}
	for _, key := range slices.SortedFunc(maps.Keys(samples), compareConformKeys) {
		items := samples[key]
		row := types.ConformRow{Key: key, N: len(items)}
		measures := make([]int, len(items))
		for i, it := range items {
			measures[i] = it.Measure
		}
		slices.Sort(measures)
		row.P50, row.P90, row.P95, row.P99 = nearestRank(measures, 50), nearestRank(measures, 90),
			nearestRank(measures, 95), nearestRank(measures, 99)
		limit, limited := conformLimit(o, key)
		if limited {
			row.Live, row.Anchor = nearestRank(measures, limit.Percentile), limit.Anchor
		}
		row.Value, row.Source = o.Effective(key)
		if row.Source == "" {
			out.Rows = append(out.Rows, row)
			continue
		}
		if key == cloneKey {
			for _, grp := range groups {
				if knownCloneGroup(grp, o.Ratchet.Known) {
					row.Known++
					continue
				}
				row.Violations = append(row.Violations, cloneItem(grp))
			}
		} else {
			for _, it := range items {
				if it.Measure <= row.Value {
					continue
				}
				mark(key.Metric, it.ID)
				if known[key.Metric][it.ID] {
					row.Known++
					continue
				}
				row.Violations = append(row.Violations, it)
			}
		}
		slices.SortFunc(row.Violations, func(a, b types.ConformItem) int {
			return cmp.Or(cmp.Compare(b.Measure, a.Measure), cmp.Compare(a.ID, b.ID))
		})
		if limited && row.N >= o.MinSample && float64(row.Live) < float64(row.Value)*(1-o.Step) {
			row.Propose = row.Live
		}
		out.Rows = append(out.Rows, row)
	}
	out.Stale = staleKnown(o.Ratchet.Known, past, groups)
	out.Answer = types.ClassifyAnswer(len(out.Rows) > 0, "", nil)
	return out
}

// cloneKey is the one row clone groups are judged in: source and main code together, since
// a body copied between a command and a library is one duplicate.
var cloneKey = types.ConformKey{Metric: types.ConformCloneTokens, Population: types.ConformPopSource}

// nearestRank is the p-th percentile of sorted by nearest rank, the element at index
// ceil(p/100*n)-1, or zero when sorted is empty. p*n is formed before dividing so an
// integer p matches the integer form (p*n+99)/100-1 exactly.
func nearestRank(sorted []int, p float64) int {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(p*float64(len(sorted))/100)) - 1
	return sorted[min(max(i, 0), len(sorted)-1)]
}

// conformLimit is the configured limit for k: its own scope's, else the unscoped key's.
func conformLimit(o types.ConformOptions, k types.ConformKey) (types.ConformLimit, bool) {
	if l, ok := o.Limits[k]; ok {
		return l, true
	}
	l, ok := o.Limits[types.ConformKey{Metric: k.Metric, Population: k.Population}]
	return l, ok
}

func compareConformKeys(a, b types.ConformKey) int {
	return cmp.Or(cmp.Compare(a.Metric, b.Metric), cmp.Compare(a.Population, b.Population), cmp.Compare(a.Scope, b.Scope))
}

// conformSamples measures every item of every metric, keyed by metric, population and
// scope, each list in ID order. Generated and excluded files belong to no population. A
// dep-fanout package is keyed by its own directory when a value is configured for that
// directory, else by its layer. clone-tokens samples every callable with a shape, test
// code in its own row; its violations are cloneGroups, not these samples. call-sequence
// samples are callSeqSamples'.
func conformSamples(g *Graph, o types.ConformOptions) map[types.ConformKey][]types.ConformItem {
	x := newConformIndex(g, o)
	out := map[types.ConformKey][]types.ConformItem{}
	add := func(m types.ConformMetric, p types.ConformPopulation, scope string, it types.ConformItem) {
		k := types.ConformKey{Metric: m, Population: p, Scope: scope}
		out[k] = append(out[k], it)
	}
	x.fileSamples(add)
	x.symbolSamples(add)
	x.depSamples(add)
	for _, e := range x.exports() {
		add(types.ConformExportScope, e.pop, "", e.item)
	}
	maps.Copy(out, callSeqSamples(g, o))
	for _, items := range out {
		slices.SortFunc(items, func(a, b types.ConformItem) int { return cmp.Compare(a.ID, b.ID) })
	}
	return out
}

// cloneGroups is every set of two or more source or main callables that share one body
// shape and run over Effective(clone-tokens, source) tokens, each member's Measure its
// token count. Copies whose files differ only by a platform suffix are one body written
// per platform, so a shape held only by such twins forms no group. Members are in ID
// order and groups in first-member order.
func cloneGroups(g *Graph, o types.ConformOptions) [][]types.ConformItem {
	x := newConformIndex(g, o)
	limit, _ := o.Effective(cloneKey)
	byShape := map[string][]types.ConformItem{}
	for _, s := range x.callables() {
		if s.pop == types.ConformPopTest || s.shape == "" || s.tokens <= limit {
			continue
		}
		byShape[s.shape] = append(byShape[s.shape], types.ConformItem{ID: s.itemID, Source: s.node.Source, Measure: s.tokens})
	}
	var out [][]types.ConformItem
	for _, members := range byShape {
		if len(members) < 2 {
			continue
		}
		slices.SortFunc(members, func(a, b types.ConformItem) int { return cmp.Compare(a.ID, b.ID) })
		var bodies []string
		for _, m := range members {
			file := itemFile(m.ID)
			if !slices.ContainsFunc(bodies, func(b string) bool { return platformTwin(b, file) }) {
				bodies = append(bodies, file)
			}
		}
		if len(bodies) >= 2 {
			out = append(out, members)
		}
	}
	slices.SortFunc(out, func(a, b []types.ConformItem) int { return cmp.Compare(a[0].ID, b[0].ID) })
	return out
}

// exportScope is every Go exported top-level symbol, in a package under an internal
// directory or in a main package, that no non-test file outside its package directory
// references, in ID order. Methods are out of scope, since an interface may need them
// exported, and so is a module's root package, whose exports are its API.
func exportScope(g *Graph, o types.ConformOptions) []types.ConformItem {
	var out []types.ConformItem
	for _, e := range newConformIndex(g, o).exports() {
		if e.item.Measure > 0 {
			out = append(out, e.item)
		}
	}
	return out
}

// platformSuffixes are the filename suffixes that build a file for one platform: Go's
// GOOS and GOARCH values, plus the _unix and _other spellings a build tag pairs with them.
var platformSuffixes = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true, "hurd": true,
	"illumos": true, "ios": true, "js": true, "linux": true, "nacl": true, "netbsd": true,
	"openbsd": true, "plan9": true, "solaris": true, "wasip1": true, "windows": true, "zos": true,
	"386": true, "amd64": true, "amd64p32": true, "arm": true, "armbe": true, "arm64": true,
	"arm64be": true, "loong64": true, "mips": true, "mipsle": true, "mips64": true,
	"mips64le": true, "mips64p32": true, "mips64p32le": true, "ppc": true, "ppc64": true,
	"ppc64le": true, "riscv": true, "riscv64": true, "s390": true, "s390x": true, "sparc": true,
	"sparc64": true, "wasm": true,
	"unix": true, "other": true,
}

// platformTwin reports whether files a and b are one file written per platform: in one
// directory, with one extension, both carrying a platform suffix, and equal once it is
// dropped. A file is not its own twin, and fs.go is not a twin of fs_linux.go: both build
// on linux, so a copy between them is a real one.
func platformTwin(a, b string) bool {
	if a == b || path.Dir(a) != path.Dir(b) || path.Ext(a) != path.Ext(b) {
		return false
	}
	stemA, okA := platformStem(a)
	stemB, okB := platformStem(b)
	return okA && okB && stemA == stemB
}

// platformStem is file's base name less its extension and up to two platform suffixes
// (name_GOOS_GOARCH), with any _test suffix kept; ok reports whether one was dropped.
func platformStem(file string) (stem string, ok bool) {
	base := strings.TrimSuffix(path.Base(file), path.Ext(file))
	base, test := strings.CutSuffix(base, "_test")
	for range 2 {
		i := strings.LastIndexByte(base, '_')
		if i < 1 || !platformSuffixes[base[i+1:]] {
			break
		}
		base, ok = base[:i], true
	}
	if test {
		base += "_test"
	}
	return base, ok
}

// conformIndex places the graph's files and symbols in their populations.
type conformIndex struct {
	g      *Graph
	o      types.ConformOptions
	fileNS map[string][]string
	// main holds the namespace IDs of Go main packages: those defining a top-level main.
	main map[string]bool
	// local is each symbol's descriptor path within its namespace.
	local map[string][]descriptor
}

func newConformIndex(g *Graph, o types.ConformOptions) *conformIndex {
	g.ensureAdj()
	x := &conformIndex{g: g, o: o, fileNS: g.fileNamespaces(), main: map[string]bool{}, local: map[string][]descriptor{}}
	for id, n := range g.nodes {
		if n.Kind != types.KindSymbol {
			continue
		}
		p, ok := parseDescriptors(id)
		if !ok {
			continue
		}
		local := afterNamespace(p)
		x.local[id] = local
		if n.Attrs[attrLanguage] == "go" && len(local) == 1 && local[0] == (descriptor{Name: "main", Suffix: '('}) &&
			!types.IsTestPath(n.Source) {
			x.main[n.Attrs[attrNamespace]] = true
		}
	}
	return x
}

// population places a workspace file, or reports false for generated or excluded output.
func (x *conformIndex) population(file string) (types.ConformPopulation, bool) {
	if conformGenerated(x.g, x.o, file) || conformExcludes(x.o, file) {
		return "", false
	}
	if types.IsTestPath(file) {
		return types.ConformPopTest, true
	}
	for _, ns := range x.fileNS[fileID(file)] {
		if x.main[ns] {
			return types.ConformPopMain, true
		}
	}
	return types.ConformPopSource, true
}

// conformGenerated reports whether file is generated output: declared by a target, opening
// with the generated-code header, under a gen directory, written by a protocol compiler,
// or a fixture or dependency tree.
func conformGenerated(g *Graph, o types.ConformOptions, file string) bool {
	if o.Generated[file] || g.nodes[fileID(file)].Attrs[attrGenerated] == "true" {
		return true
	}
	base := path.Base(file)
	for _, s := range generatedSuffixes {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	for _, seg := range strings.Split(path.Dir(file), "/") {
		switch seg {
		case "gen", "testdata", "vendor", "node_modules":
			return true
		}
	}
	return false
}

// conformExcludes reports whether one of o.Exclude's globs matches file. A malformed glob
// matches nothing here; refusing it is the loader's job.
func conformExcludes(o types.ConformOptions, file string) bool {
	for _, glob := range o.Exclude {
		if ok, err := doublestar.Match(glob, file); err == nil && ok {
			return true
		}
	}
	return false
}

// conformExcluded counts the source files o.Exclude hides that would otherwise be measured.
func conformExcluded(g *Graph, o types.ConformOptions) int {
	n := 0
	for _, f := range g.nodes {
		if f.Kind == types.KindFile && f.Attrs[attrLanguage] != "" && !conformGenerated(g, o, f.Source) && conformExcludes(o, f.Source) {
			n++
		}
	}
	return n
}

type conformAdd func(types.ConformMetric, types.ConformPopulation, string, types.ConformItem)

// fileSamples measures each sized source file's lines and its top-level declarations. A
// file with no language is a document, not code.
func (x *conformIndex) fileSamples(add conformAdd) {
	for id, f := range x.g.nodes {
		if f.Kind != types.KindFile || f.Attrs[attrLanguage] == "" {
			continue
		}
		lines, err := strconv.Atoi(f.Attrs[AttrLines])
		if err != nil {
			continue
		}
		pop, ok := x.population(f.Source)
		if !ok {
			continue
		}
		decls := 0
		for _, e := range x.g.out[id] {
			if e.Relation == types.RelationDefines && len(x.local[e.Target]) == 1 {
				decls++
			}
		}
		source := f.Source + ":1"
		add(types.ConformFileLines, pop, "", types.ConformItem{ID: f.Source, Source: source, Measure: lines})
		add(types.ConformFileSymbols, pop, "", types.ConformItem{ID: f.Source, Source: source, Measure: decls})
	}
}

// conformSymbol is one workspace-defined symbol placed in a population.
type conformSymbol struct {
	id, itemID string
	node       types.KnowledgeNode
	local      []descriptor
	pop        types.ConformPopulation
	shape      string
	tokens     int
}

// symbols are the symbols defined in a measured file.
func (x *conformIndex) symbols() []conformSymbol {
	var out []conformSymbol
	for id, local := range x.local {
		n := x.g.nodes[id]
		file, _, ok := splitPathLine(n.Source)
		if !ok || len(local) == 0 {
			continue
		}
		pop, ok := x.population(file)
		if !ok {
			continue
		}
		s := conformSymbol{id: id, itemID: symbolItemID(file, local), node: n, local: local, pop: pop}
		if tokens, err := strconv.Atoi(n.Attrs[attrBodyTokens]); err == nil && n.Attrs[AttrBodyShape] != "" {
			s.shape, s.tokens = n.Attrs[AttrBodyShape], tokens
		}
		out = append(out, s)
	}
	return out
}

func (x *conformIndex) callables() []conformSymbol {
	var out []conformSymbol
	for _, s := range x.symbols() {
		if k := neutralKind(s.local); k == declFunction || k == declMethod {
			out = append(out, s)
		}
	}
	return out
}

// symbolItemID names a symbol as a ratchet lists it: its file, then its path within the
// namespace, "vcs/hg.go#hgVCS.Bisect".
func symbolItemID(file string, local []descriptor) string {
	names := make([]string, len(local))
	for i, d := range local {
		names[i] = d.Name
	}
	return file + "#" + strings.Join(names, ".")
}

// itemFile is the file a symbol item ID names.
func itemFile(id string) string {
	file, _, _ := strings.Cut(id, "#")
	return file
}

// symbolSamples measures each callable's lines and body tokens, and each type's methods.
func (x *conformIndex) symbolSamples(add conformAdd) {
	methods := map[string]int{}
	for _, s := range x.callables() {
		item := types.ConformItem{ID: s.itemID, Source: s.node.Source}
		if start, end := definitionSpan(s.node); start > 0 && end >= start {
			item.Measure = end - start + 1
			add(types.ConformFuncLines, s.pop, "", item)
		}
		if s.shape != "" {
			pop := types.ConformPopSource
			if s.pop == types.ConformPopTest {
				pop = types.ConformPopTest
			}
			item.Measure = s.tokens
			add(types.ConformCloneTokens, pop, "", item)
		}
		if neutralKind(s.local) == declMethod {
			methods[s.id[:strings.LastIndexByte(s.id, '#')+1]]++
		}
	}
	for typeID, n := range methods {
		t, ok := x.g.nodes[typeID]
		file, _, placed := splitPathLine(t.Source)
		if !ok || !placed || len(x.local[typeID]) != 1 {
			continue
		}
		pop, ok := x.population(file)
		if !ok {
			continue
		}
		add(types.ConformTypeMethods, pop, "", types.ConformItem{ID: symbolItemID(file, x.local[typeID]), Source: t.Source, Measure: n})
	}
}

// depSamples measures how many other workspace package directories each package imports,
// read from the imports the index recorded. What a generated file imports is its
// generator's choice and is not counted, but importing a generated package is.
func (x *conformIndex) depSamples(add conformAdd) {
	deps := x.g.packageDepsExcept(func(file string) bool {
		_, ok := x.population(file)
		return !ok
	})
	dirs, targets := x.packageDirs(true), x.packageDirs(false)
	imports := map[string]map[string]bool{}
	pops := map[string]types.ConformPopulation{}
	for ns, dir := range dirs {
		if imports[dir] == nil {
			imports[dir] = map[string]bool{}
		}
		if x.main[ns] || pops[dir] == "" {
			pops[dir] = x.packagePopulation(ns)
		}
		for to := range deps[ns] {
			if td, ok := targets[to]; ok && td != dir {
				imports[dir][td] = true
			}
		}
	}
	for dir, tos := range imports {
		pop := pops[dir]
		scope := dir
		if !x.pinned(types.ConformKey{Metric: types.ConformDepFanout, Population: pop, Scope: dir}) {
			scope = x.layer(dir)
		}
		add(types.ConformDepFanout, pop, scope, types.ConformItem{ID: dir, Source: dir, Measure: len(tos)})
	}
}

// packageDirs places each namespace in the lowest directory holding a non-test file that
// defines it, counting only measured files when measured is set.
func (x *conformIndex) packageDirs(measured bool) map[string]string {
	dirs := map[string]string{}
	for fid, nss := range x.fileNS {
		f := x.g.nodes[fid]
		if f.Source == "" || types.IsTestPath(f.Source) {
			continue
		}
		if _, ok := x.population(f.Source); measured && !ok {
			continue
		}
		dir := path.Dir(f.Source)
		for _, ns := range nss {
			if cur, ok := dirs[ns]; !ok || dir < cur {
				dirs[ns] = dir
			}
		}
	}
	return dirs
}

func (x *conformIndex) packagePopulation(ns string) types.ConformPopulation {
	if x.main[ns] {
		return types.ConformPopMain
	}
	return types.ConformPopSource
}

// pinned reports whether k itself, not its unscoped fallback, has a configured or committed
// value.
func (x *conformIndex) pinned(k types.ConformKey) bool {
	if _, ok := x.o.Limits[k]; ok {
		return true
	}
	return slices.ContainsFunc(x.o.Ratchet.Thresholds, func(t types.ConformThreshold) bool { return t.ConformKey == k })
}

// layer is dir's declared layer, else its first path segment, as the precedent miner
// places a package.
func (x *conformIndex) layer(dir string) string {
	if declared := x.g.nodes[dirID(dir)].Attrs[types.AttrLayer]; declared != "" {
		return declared
	}
	first, _, _ := strings.Cut(dir, "/")
	return first
}

// conformExport is one in-scope exported symbol: Measure 1 when nothing outside its
// package references it, else 0.
type conformExport struct {
	item types.ConformItem
	pop  types.ConformPopulation
}

func (x *conformIndex) exports() []conformExport {
	dirs := x.packageDirs(true)
	var out []conformExport
	for _, s := range x.symbols() {
		ns := s.node.Attrs[attrNamespace]
		dir, placed := dirs[ns]
		if s.pop == types.ConformPopTest || !placed || s.node.Attrs[attrLanguage] != "go" || len(s.local) != 1 ||
			!exportedName(s.local[0].Name) || moduleRoot(ns) {
			continue
		}
		if !x.main[ns] && !slices.Contains(strings.Split(dir, "/"), "internal") {
			continue
		}
		item := types.ConformItem{ID: s.itemID, Source: s.node.Source, Measure: 1}
		for _, e := range x.g.in[s.id] {
			ref := strings.TrimPrefix(e.Source, types.KindFile+":")
			if e.Relation == types.RelationReferences && !types.IsTestPath(ref) && path.Dir(ref) != dir {
				item.Measure = 0
				break
			}
		}
		out = append(out, conformExport{item: item, pop: s.pop})
	}
	slices.SortFunc(out, func(a, b conformExport) int { return cmp.Compare(a.item.ID, b.item.ID) })
	return out
}

func exportedName(name string) bool {
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}

// moduleRoot reports whether ns is a module's root package: its import path, the namespace
// descriptor, is the module the key names, "gomod example.com/m `example.com/m`/".
func moduleRoot(ns string) bool {
	parts := strings.SplitN(strings.TrimPrefix(ns, types.KindSymbol+":"), " ", 3)
	if len(parts) != 3 {
		return false
	}
	p, ok := parseDescriptors(ns)
	return ok && len(p) == 1 && p[0].Suffix == '/' && p[0].Name == parts[1]
}

// conformKnownItems indexes the known items by metric.
func conformKnownItems(known []types.ConformKnown) map[types.ConformMetric]map[string]bool {
	out := map[types.ConformMetric]map[string]bool{}
	for _, k := range known {
		if out[k.Metric] == nil {
			out[k.Metric] = map[string]bool{}
		}
		for _, it := range k.Items {
			out[k.Metric][it] = true
		}
	}
	return out
}

// knownCloneGroup reports whether one known clone entry lists every member of grp: a copy
// added to an accepted group makes the group a violation again.
func knownCloneGroup(grp []types.ConformItem, known []types.ConformKnown) bool {
	return slices.ContainsFunc(known, func(k types.ConformKnown) bool {
		return k.Metric == types.ConformCloneTokens && !slices.ContainsFunc(grp, func(m types.ConformItem) bool {
			return !slices.Contains(k.Items, m.ID)
		})
	})
}

// cloneItem is a clone group as one violation, named by its members.
func cloneItem(grp []types.ConformItem) types.ConformItem {
	ids := make([]string, len(grp))
	for i, m := range grp {
		ids[i] = m.ID
	}
	return types.ConformItem{ID: strings.Join(ids, ", "), Source: grp[0].Source, Measure: grp[0].Measure}
}

// staleKnown is each known entry narrowed to the items no longer past their value, in
// record order. A clone entry is stale whole once no group shares two of its members.
func staleKnown(known []types.ConformKnown, past map[types.ConformMetric]map[string]bool, groups [][]types.ConformItem) []types.ConformKnown {
	var out []types.ConformKnown
	for _, k := range known {
		if k.Metric == types.ConformCloneTokens {
			if !slices.ContainsFunc(groups, func(grp []types.ConformItem) bool { return sharedMembers(grp, k.Items) >= 2 }) {
				out = append(out, k)
			}
			continue
		}
		stale := k
		stale.Items = nil
		for _, it := range k.Items {
			if !past[k.Metric][it] {
				stale.Items = append(stale.Items, it)
			}
		}
		if len(stale.Items) > 0 {
			out = append(out, stale)
		}
	}
	slices.SortStableFunc(out, func(a, b types.ConformKnown) int { return cmp.Compare(a.Line, b.Line) })
	return out
}

func sharedMembers(grp []types.ConformItem, items []string) int {
	n := 0
	for _, m := range grp {
		if slices.Contains(items, m.ID) {
			n++
		}
	}
	return n
}
