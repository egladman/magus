package knowledge

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

// Conformance measures every conformance metric over g, one row per metric, population and scope,
// and judges each row against the value o.Effective enforces: the committed ratchet value,
// else the configured anchor. A live percentile is reported and may be proposed, never
// enforced. A key with no configured value is measured only: no violations, no proposal.
// file-split is a floor: an item at or under its value is past it, as is one whose name splits
// more than one concept off its root, and it proposes nothing.
// The mode-knobs row holds only the functions the coupling rule finds over-shared, so its n
// is how many there are, zero included. A known item renamed or moved is judged under its
// new ID when its recorded shape names it unambiguously (carryKnown).
//
// Rows come back sorted by metric, population and scope, and every list inside them in a
// fixed order, so two runs over one graph agree byte for byte. A graph with no symbols
// answers unknown rather than an empty pass. A language with no BodyShape lexer
// contributes no clone samples, and one with no package clause no main population. It
// reads g only and writes nothing.
func (g *Graph) Conformance(o types.ConformanceOptions) types.ConformanceOutput {
	if !g.HasSymbols() {
		return types.ConformanceOutput{Answer: types.ClassifyAnswer(false, types.ReasonSymbolIndexMissing, nil)}
	}
	samples := conformanceSamples(g, o)
	loops, knobbed := sharedLoops(g, o)
	maps.Copy(samples, loops)
	samples[couplingKey(types.ConformanceModeKnobs)] = knobbed
	maps.Copy(samples, aliasSamples(g, o))
	groups := cloneGroups(g, o)
	past := pastValue(o, samples, groups)
	shapes := newConformanceIndex(g, o).itemShapes()
	carry := carryKnown(o.Ratchet.Known, sampledIDs(samples), past, shapes)
	known := conformanceKnownItems(carry.known)

	out := types.ConformanceOutput{
		Excluded: conformanceExcluded(g, o), Carried: carry.carried, Ambiguous: carry.ambiguous,
		Shapes: listedShapes(carry.known, shapes),
	}
	for _, key := range slices.SortedFunc(maps.Keys(samples), compareConformanceKeys) {
		items := samples[key]
		row := types.ConformanceRow{Key: key, N: len(items)}
		measures := make([]int, len(items))
		for i, it := range items {
			measures[i] = it.Measure
		}
		slices.Sort(measures)
		row.P50, row.P90, row.P95, row.P99 = nearestRank(measures, 50), nearestRank(measures, 90),
			nearestRank(measures, 95), nearestRank(measures, 99)
		limit, limited := conformanceLimit(o, key)
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
				if knownCloneGroup(grp, carry.known) {
					row.Known++
					continue
				}
				row.Violations = append(row.Violations, cloneItem(grp))
			}
		} else {
			for _, it := range items {
				if !itemPast(key.Metric, it, row.Value) {
					continue
				}
				if known[key.Metric][it.ID] {
					row.Known++
					continue
				}
				row.Violations = append(row.Violations, it)
			}
		}
		slices.SortFunc(row.Violations, func(a, b types.ConformanceItem) int {
			return cmp.Or(cmp.Compare(b.Measure, a.Measure), cmp.Compare(a.ID, b.ID))
		})
		if limited && key.Metric != types.ConformanceFileSplit && row.N >= o.MinSample && float64(row.Live) < float64(row.Value)*(1-o.Step) {
			row.Propose = row.Live
		}
		out.Rows = append(out.Rows, row)
	}
	out.Stale = staleKnown(carry.known, past, groups)
	out.Answer = types.ClassifyAnswer(len(out.Rows) > 0, "", nil)
	return out
}

// cloneKey is the one row clone groups are judged in: source and main code together, since
// a body copied between a command and a library is one duplicate.
var cloneKey = types.ConformanceKey{Metric: types.ConformanceCloneTokens, Population: types.ConformancePopSource}

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

// conformanceLimit is the configured limit for k: its own scope's, else the unscoped key's.
func conformanceLimit(o types.ConformanceOptions, k types.ConformanceKey) (types.ConformanceLimit, bool) {
	if l, ok := o.Limits[k]; ok {
		return l, true
	}
	l, ok := o.Limits[types.ConformanceKey{Metric: k.Metric, Population: k.Population}]
	return l, ok
}

func compareConformanceKeys(a, b types.ConformanceKey) int {
	return cmp.Or(cmp.Compare(a.Metric, b.Metric), cmp.Compare(a.Population, b.Population), cmp.Compare(a.Scope, b.Scope))
}

// conformanceSamples measures every item of every metric, keyed by metric, population and
// scope, each list in ID order. Generated and excluded files belong to no population. A
// dep-fanout package is keyed by its own directory when a value is configured for that
// directory, else by its layer. clone-tokens samples every callable with a shape, test
// code in its own row; its violations are cloneGroups, not these samples. call-sequence
// samples are callSeqSamples'.
func conformanceSamples(g *Graph, o types.ConformanceOptions) map[types.ConformanceKey][]types.ConformanceItem {
	x := newConformanceIndex(g, o)
	out := map[types.ConformanceKey][]types.ConformanceItem{}
	add := func(m types.ConformanceMetric, p types.ConformancePopulation, scope string, it types.ConformanceItem) {
		k := types.ConformanceKey{Metric: m, Population: p, Scope: scope}
		out[k] = append(out[k], it)
	}
	x.fileSamples(add)
	x.symbolSamples(add)
	x.mainLogicSamples(add)
	x.storePathSamples(add)
	x.fileSplitSamples(add)
	x.depSamples(add)
	for _, e := range x.exports() {
		add(types.ConformanceExportScope, e.pop, "", e.item)
	}
	maps.Copy(out, callSeqSamples(g, o))
	for _, items := range out {
		slices.SortFunc(items, func(a, b types.ConformanceItem) int { return cmp.Compare(a.ID, b.ID) })
	}
	return out
}

// cloneGroups is every set of two or more source or main callables that share one body
// shape and run over Effective(clone-tokens, source) tokens, each member's Measure its
// token count. Copies whose files differ only by a platform suffix are one body written
// per platform, so a shape held only by such twins forms no group. Members are in ID
// order and groups in first-member order.
func cloneGroups(g *Graph, o types.ConformanceOptions) [][]types.ConformanceItem {
	x := newConformanceIndex(g, o)
	limit, _ := o.Effective(cloneKey)
	byShape := map[string][]types.ConformanceItem{}
	for _, s := range x.callables() {
		if s.pop == types.ConformancePopTest || s.shape == "" || s.tokens <= limit {
			continue
		}
		byShape[s.shape] = append(byShape[s.shape], types.ConformanceItem{ID: s.itemID, Source: s.node.Source, Measure: s.tokens})
	}
	var out [][]types.ConformanceItem
	for _, members := range byShape {
		if len(members) < 2 {
			continue
		}
		slices.SortFunc(members, func(a, b types.ConformanceItem) int { return cmp.Compare(a.ID, b.ID) })
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
	slices.SortFunc(out, func(a, b []types.ConformanceItem) int { return cmp.Compare(a[0].ID, b[0].ID) })
	return out
}

// exportScope is every Go exported top-level symbol, in a package under an internal
// directory or in a main package, that no non-test file outside its package directory
// references, in ID order. Methods are out of scope, since an interface may need them
// exported, and so is a module's root package, whose exports are its API.
func exportScope(g *Graph, o types.ConformanceOptions) []types.ConformanceItem {
	var out []types.ConformanceItem
	for _, e := range newConformanceIndex(g, o).exports() {
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

// conformanceIndex places the graph's files and symbols in their populations.
type conformanceIndex struct {
	g      *Graph
	o      types.ConformanceOptions
	fileNS map[string][]string
	// main holds the namespace IDs of Go main packages: those defining a top-level main.
	main map[string]bool
	// local is each symbol's descriptor path within its namespace.
	local map[string][]descriptor
}

func newConformanceIndex(g *Graph, o types.ConformanceOptions) *conformanceIndex {
	g.ensureAdj()
	x := &conformanceIndex{g: g, o: o, fileNS: g.fileNamespaces(), main: map[string]bool{}, local: map[string][]descriptor{}}
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
func (x *conformanceIndex) population(file string) (types.ConformancePopulation, bool) {
	if conformanceGenerated(x.g, x.o, file) || conformanceExcludes(x.o, file) {
		return "", false
	}
	if types.IsTestPath(file) {
		return types.ConformancePopTest, true
	}
	for _, ns := range x.fileNS[fileID(file)] {
		if x.main[ns] {
			return types.ConformancePopMain, true
		}
	}
	return types.ConformancePopSource, true
}

// conformanceGenerated reports whether file is generated output: declared by a target, opening
// with the generated-code header, under a gen directory, written by a protocol compiler,
// or a fixture or dependency tree.
func conformanceGenerated(g *Graph, o types.ConformanceOptions, file string) bool {
	return o.Generated[file] || g.nodes[fileID(file)].Attrs[attrGenerated] == "true" || generatedPath(file)
}

// generatedPath reports whether file's name or directory alone says it is generated output.
func generatedPath(file string) bool {
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

// conformanceExcludes reports whether one of o.Exclude's globs matches file. A malformed glob
// matches nothing here; refusing it is the loader's job.
func conformanceExcludes(o types.ConformanceOptions, file string) bool {
	for _, glob := range o.Exclude {
		if ok, err := doublestar.Match(glob, file); err == nil && ok {
			return true
		}
	}
	return false
}

// conformanceExcluded counts the source files o.Exclude hides that would otherwise be measured.
func conformanceExcluded(g *Graph, o types.ConformanceOptions) int {
	n := 0
	for _, f := range g.nodes {
		if f.Kind == types.KindFile && f.Attrs[attrLanguage] != "" && !conformanceGenerated(g, o, f.Source) && conformanceExcludes(o, f.Source) {
			n++
		}
	}
	return n
}

type conformanceAdd func(types.ConformanceMetric, types.ConformancePopulation, string, types.ConformanceItem)

// fileSamples measures each sized source file's lines and its top-level declarations. A
// file with no language is a document, not code.
func (x *conformanceIndex) fileSamples(add conformanceAdd) {
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
		add(types.ConformanceFileLines, pop, "", types.ConformanceItem{ID: f.Source, Source: source, Measure: lines})
		add(types.ConformanceFileSymbols, pop, "", types.ConformanceItem{ID: f.Source, Source: source, Measure: decls})
	}
}

// conformanceSymbol is one workspace-defined symbol placed in a population.
type conformanceSymbol struct {
	id, itemID string
	node       types.KnowledgeNode
	local      []descriptor
	pop        types.ConformancePopulation
	shape      string
	tokens     int
}

// symbols are the symbols defined in a measured file.
func (x *conformanceIndex) symbols() []conformanceSymbol {
	var out []conformanceSymbol
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
		s := conformanceSymbol{id: id, itemID: symbolItemID(file, local), node: n, local: local, pop: pop}
		if tokens, err := strconv.Atoi(n.Attrs[attrBodyTokens]); err == nil && n.Attrs[AttrBodyShape] != "" {
			s.shape, s.tokens = n.Attrs[AttrBodyShape], tokens
		}
		out = append(out, s)
	}
	return out
}

func (x *conformanceIndex) callables() []conformanceSymbol {
	var out []conformanceSymbol
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
	return fmt.Sprintf("%s#%s", file, strings.Join(names, "."))
}

// itemFile is the file a symbol item ID names.
func itemFile(id string) string {
	file, _, _ := strings.Cut(id, "#")
	return file
}

// symbolSamples measures each callable's lines and body tokens, and each type's methods.
func (x *conformanceIndex) symbolSamples(add conformanceAdd) {
	methods := map[string]int{}
	for _, s := range x.callables() {
		item := types.ConformanceItem{ID: s.itemID, Source: s.node.Source}
		if start, end := definitionSpan(s.node); start > 0 && end >= start {
			item.Measure = end - start + 1
			add(types.ConformanceFuncLines, s.pop, "", item)
		}
		if s.shape != "" {
			pop := types.ConformancePopSource
			if s.pop == types.ConformancePopTest {
				pop = types.ConformancePopTest
			}
			item.Measure = s.tokens
			add(types.ConformanceCloneTokens, pop, "", item)
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
		add(types.ConformanceTypeMethods, pop, "", types.ConformanceItem{ID: symbolItemID(file, x.local[typeID]), Source: t.Source, Measure: n})
	}
}

// depSamples measures how many other workspace package directories each package imports,
// read from the imports the index recorded. What a generated file imports is its
// generator's choice and is not counted, but importing a generated package is.
func (x *conformanceIndex) depSamples(add conformanceAdd) {
	deps := x.g.packageDepsExcept(func(file string) bool {
		_, ok := x.population(file)
		return !ok
	})
	dirs, targets := x.packageDirs(true), x.packageDirs(false)
	imports := map[string]map[string]bool{}
	pops := map[string]types.ConformancePopulation{}
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
		if !x.pinned(types.ConformanceKey{Metric: types.ConformanceDepFanout, Population: pop, Scope: dir}) {
			scope = x.layer(dir)
		}
		add(types.ConformanceDepFanout, pop, scope, types.ConformanceItem{ID: dir, Source: dir, Measure: len(tos)})
	}
}

// packageDirs places each namespace in the lowest directory holding a non-test file that
// defines it, counting only measured files when measured is set.
func (x *conformanceIndex) packageDirs(measured bool) map[string]string {
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

func (x *conformanceIndex) packagePopulation(ns string) types.ConformancePopulation {
	if x.main[ns] {
		return types.ConformancePopMain
	}
	return types.ConformancePopSource
}

// pinned reports whether k itself, not its unscoped fallback, has a configured or committed
// value.
func (x *conformanceIndex) pinned(k types.ConformanceKey) bool {
	if _, ok := x.o.Limits[k]; ok {
		return true
	}
	return slices.ContainsFunc(x.o.Ratchet.Thresholds, func(t types.ConformanceThreshold) bool { return t.ConformanceKey == k })
}

// layer is dir's declared layer, else its first path segment, as the precedent miner
// places a package.
func (x *conformanceIndex) layer(dir string) string {
	if declared := x.g.nodes[dirID(dir)].Attrs[types.AttrLayer]; declared != "" {
		return declared
	}
	first, _, _ := strings.Cut(dir, "/")
	return first
}

// conformanceExport is one in-scope exported symbol: Measure 1 when nothing outside its
// package references it, else 0.
type conformanceExport struct {
	item types.ConformanceItem
	pop  types.ConformancePopulation
}

func (x *conformanceIndex) exports() []conformanceExport {
	dirs := x.packageDirs(true)
	var out []conformanceExport
	for _, s := range x.symbols() {
		ns := s.node.Attrs[attrNamespace]
		dir, placed := dirs[ns]
		if s.pop == types.ConformancePopTest || !placed || s.node.Attrs[attrLanguage] != "go" || len(s.local) != 1 ||
			!exportedName(s.local[0].Name) || moduleRoot(ns) {
			continue
		}
		if !x.main[ns] && !slices.Contains(strings.Split(dir, "/"), "internal") {
			continue
		}
		item := types.ConformanceItem{ID: s.itemID, Source: s.node.Source, Measure: 1}
		if x.g.usedOutside(s.id, dir) > 0 {
			item.Measure = 0
		}
		out = append(out, conformanceExport{item: item, pop: s.pop})
	}
	slices.SortFunc(out, func(a, b conformanceExport) int { return cmp.Compare(a.item.ID, b.item.ID) })
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

// conformanceKnownItems indexes the known items by metric.
func conformanceKnownItems(known []types.ConformanceKnown) map[types.ConformanceMetric]map[string]bool {
	out := map[types.ConformanceMetric]map[string]bool{}
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
func knownCloneGroup(grp []types.ConformanceItem, known []types.ConformanceKnown) bool {
	return slices.ContainsFunc(known, func(k types.ConformanceKnown) bool {
		return k.Metric == types.ConformanceCloneTokens && !slices.ContainsFunc(grp, func(m types.ConformanceItem) bool {
			return !slices.Contains(k.Items, m.ID)
		})
	})
}

// cloneItem is a clone group as one violation, named by its members.
func cloneItem(grp []types.ConformanceItem) types.ConformanceItem {
	ids := make([]string, len(grp))
	for i, m := range grp {
		ids[i] = m.ID
	}
	return types.ConformanceItem{ID: strings.Join(ids, ", "), Source: grp[0].Source, Measure: grp[0].Measure}
}

// staleKnown is each known entry narrowed to the items no longer past their value, in
// record order, without shapes. A clone entry is stale whole once no group shares two of
// its members.
func staleKnown(known []types.ConformanceKnown, past map[types.ConformanceMetric]map[string]bool, groups [][]types.ConformanceItem) []types.ConformanceKnown {
	var out []types.ConformanceKnown
	for _, k := range known {
		k.Shapes = nil
		if k.Metric == types.ConformanceCloneTokens {
			if !slices.ContainsFunc(groups, func(grp []types.ConformanceItem) bool { return sharedMembers(grp, k.Items) >= 2 }) {
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
	slices.SortStableFunc(out, func(a, b types.ConformanceKnown) int { return cmp.Compare(a.Line, b.Line) })
	return out
}

// pastValue holds, per metric, every item a configured row finds past its value, and every
// member of a clone group when the clone row is configured: a known item outside it is
// stale.
func pastValue(o types.ConformanceOptions, samples map[types.ConformanceKey][]types.ConformanceItem, groups [][]types.ConformanceItem) map[types.ConformanceMetric]map[string]bool {
	past := map[types.ConformanceMetric]map[string]bool{}
	mark := func(m types.ConformanceMetric, id string) {
		if past[m] == nil {
			past[m] = map[string]bool{}
		}
		past[m][id] = true
	}
	for key, items := range samples {
		value, src := o.Effective(key)
		if src == "" || key == cloneKey {
			continue
		}
		for _, it := range items {
			if itemPast(key.Metric, it, value) {
				mark(key.Metric, it.ID)
			}
		}
	}
	if _, src := o.Effective(cloneKey); src != "" {
		for _, grp := range groups {
			for _, m := range grp {
				mark(types.ConformanceCloneTokens, m.ID)
			}
		}
	}
	return past
}

// sampledIDs is every item ID measured, per metric, whatever its measure.
func sampledIDs(samples map[types.ConformanceKey][]types.ConformanceItem) map[types.ConformanceMetric]map[string]bool {
	out := map[types.ConformanceMetric]map[string]bool{}
	for key, items := range samples {
		if out[key.Metric] == nil {
			out[key.Metric] = map[string]bool{}
		}
		for _, it := range items {
			out[key.Metric][it.ID] = true
		}
	}
	return out
}

// itemShapes is the fingerprint of every item a rename or a move can carry: a callable's
// or type's body shape under its item ID, and a file's fileShape under its path. An item
// ID two symbols share, such as a file's init functions, has none, and neither has a
// package.
func (x *conformanceIndex) itemShapes() map[string]string {
	out := map[string]string{}
	count := map[string]int{}
	for _, s := range x.symbols() {
		count[s.itemID]++
		if shape := s.node.Attrs[AttrBodyShape]; shape != "" {
			out[s.itemID] = shape
		}
	}
	for id, n := range count {
		if n > 1 {
			delete(out, id)
		}
	}
	for id, f := range x.g.nodes {
		if f.Kind != types.KindFile || f.Attrs[attrLanguage] == "" {
			continue
		}
		if _, ok := x.population(f.Source); !ok {
			continue
		}
		var digests []string
		for _, e := range x.g.out[id] {
			if e.Relation != types.RelationDefines || len(x.local[e.Target]) == 0 {
				continue
			}
			if d := x.g.nodes[e.Target].Attrs[AttrBodyDigest]; d != "" {
				digests = append(digests, d)
			}
		}
		if len(digests) > 0 {
			out[f.Source] = fileShape(digests)
		}
	}
	return out
}

// fileShape fingerprints the body digests of the symbols a file defines as a set: a file
// moved whole keeps it, and one that gained, lost or changed a symbol does not. The
// package clause is no symbol's body, so a move to another package keeps it too.
func fileShape(digests []string) string {
	slices.Sort(digests)
	sum := sha256.Sum256([]byte(strings.Join(digests, "\n")))
	return hex.EncodeToString(sum[:8])
}

// knownCarry is the known entries with each carried item re-keyed to its new ID, and the
// pairs carryKnown found.
type knownCarry struct {
	known              []types.ConformanceKnown
	carried, ambiguous []types.ConformanceCarry
}

// carryKnown follows known items through a rename or a move. A known item that matches no
// sample of its metric and has a recorded shape is carried to the one item past its value
// that no entry of the metric lists and whose current shape is equal. Two or more such
// items, or one that a second known item also claims, carry nothing: the pair is
// ambiguous, and the old ID stays stale beside a violation, as with no shape at all.
// known itself is not modified.
func carryKnown(known []types.ConformanceKnown, sampled, past map[types.ConformanceMetric]map[string]bool, shapes map[string]string) knownCarry {
	listed := conformanceKnownItems(known)
	var found []types.ConformanceCarry
	seen := map[metricItem]bool{}
	claims := map[metricItem]int{}
	for _, k := range known {
		for _, from := range k.Items {
			shape := k.Shapes[from]
			if shape == "" || sampled[k.Metric][from] || seen[metricItem{k.Metric, from}] {
				continue
			}
			seen[metricItem{k.Metric, from}] = true
			var to []string
			for id := range past[k.Metric] {
				if !listed[k.Metric][id] && shapes[id] == shape {
					to = append(to, id)
				}
			}
			if len(to) == 0 {
				continue
			}
			slices.Sort(to)
			for _, id := range to {
				claims[metricItem{k.Metric, id}]++
			}
			found = append(found, types.ConformanceCarry{Metric: k.Metric, From: from, To: to})
		}
	}

	out := knownCarry{known: known}
	renamed := map[metricItem]string{}
	for _, c := range found {
		if len(c.To) == 1 && claims[metricItem{c.Metric, c.To[0]}] == 1 {
			out.carried = append(out.carried, c)
			renamed[metricItem{c.Metric, c.From}] = c.To[0]
		} else {
			out.ambiguous = append(out.ambiguous, c)
		}
	}
	if len(renamed) > 0 {
		out.known = rekeyKnown(known, renamed)
	}
	return out
}

// metricItem is one item under one metric.
type metricItem struct {
	metric types.ConformanceMetric
	id     string
}

// rekeyKnown is a copy of known with each item renamed lists moved to its new ID, its
// recorded shape with it where the entry has one.
func rekeyKnown(known []types.ConformanceKnown, renamed map[metricItem]string) []types.ConformanceKnown {
	out := make([]types.ConformanceKnown, len(known))
	for i, k := range known {
		k.Items = slices.Clone(k.Items)
		k.Shapes = maps.Clone(k.Shapes)
		for j, item := range k.Items {
			to, ok := renamed[metricItem{k.Metric, item}]
			if !ok {
				continue
			}
			k.Items[j] = to
			if shape, ok := k.Shapes[item]; ok {
				k.Shapes[to] = shape
				delete(k.Shapes, item)
			}
		}
		out[i] = k
	}
	return out
}

// listedShapes is the current shape of each item known lists, where it has one, or nil
// when none has.
func listedShapes(known []types.ConformanceKnown, shapes map[string]string) map[string]string {
	var out map[string]string
	for _, k := range known {
		for _, item := range k.Items {
			if s := shapes[item]; s != "" {
				if out == nil {
					out = map[string]string{}
				}
				out[item] = s
			}
		}
	}
	return out
}

func sharedMembers(grp []types.ConformanceItem, items []string) int {
	n := 0
	for _, m := range grp {
		if slices.Contains(items, m.ID) {
			n++
		}
	}
	return n
}

// packageReach counts, per package directory other than own, the distinct nodes at the far
// end of id's edges of relation rel whose file keep accepts: the files referencing id when
// in is set, else the symbols id points at. It is how a metric asks whether code crosses
// out of its package, and to where.
func (x *conformanceIndex) packageReach(id, own string, rel types.RelationID, in bool, keep func(file string) bool) map[string]int {
	edges := x.g.out[id]
	if in {
		edges = x.g.in[id]
	}
	seen := map[string]bool{}
	out := map[string]int{}
	for _, e := range edges {
		far := e.Target
		if in {
			far = e.Source
		}
		if e.Relation != rel || seen[far] {
			continue
		}
		seen[far] = true
		file, ok := strings.CutPrefix(far, types.KindFile+":")
		if !ok {
			file, _, ok = splitPathLine(x.g.nodes[far].Source)
		}
		if !ok || path.Dir(file) == own || !keep(file) {
			continue
		}
		out[path.Dir(file)]++
	}
	return out
}

func nonTest(file string) bool { return !types.IsTestPath(file) }

// topReach is the directory reach counts most, ties to the lowest name, or "" when empty.
func topReach(reach map[string]int) string {
	top := ""
	for _, dir := range slices.Sorted(maps.Keys(reach)) {
		if top == "" || reach[dir] > reach[top] {
			top = dir
		}
	}
	return top
}

// mainLogicSamples measures a Go main-package callable that computes rather than wires by
// its body tokens, zero for the rest; Home is the package its file references most.
// Statements would read closer, but only a local coverage profile records them.
func (x *conformanceIndex) mainLogicSamples(add conformanceAdd) {
	homes := map[string]string{}
	for _, s := range x.callables() {
		if s.pop != types.ConformancePopMain || s.node.Attrs[attrLanguage] != "go" || s.shape == "" {
			continue
		}
		file, _, _ := splitPathLine(s.node.Source)
		item := types.ConformanceItem{ID: s.itemID, Source: s.node.Source}
		if x.computes(s, file) {
			item.Measure = s.tokens
			home, ok := homes[file]
			if !ok {
				home = topReach(x.packageReach(fileID(file), path.Dir(file), types.RelationReferences, false, x.sourceFile))
				homes[file] = home
			}
			item.Home = home
		}
		add(types.ConformanceMainLogic, s.pop, "", item)
	}
}

// computes reports whether s, defined in file, is logic rather than wiring.
func (x *conformanceIndex) computes(s conformanceSymbol, file string) bool {
	if len(s.local) == 1 && (s.local[0].Name == "main" || s.local[0].Name == "init") {
		return false
	}
	ifaces := x.interfaceNames(file)
	params, _, _ := goFuncParts(s.node.Attrs[AttrSignature])
	for _, p := range params {
		typ := strings.TrimLeft(p.Type, "*[].")
		if p.Name == "args" && p.Type == "[]string" || typ == "io.Writer" {
			return false
		}
		if _, name, ok := strings.Cut(typ, "."); ok && ifaces[name] || ifaces[typ] {
			return false
		}
	}
	return len(x.packageReach(s.id, path.Dir(file), types.RelationCalls, false, nonTest)) == 0
}

// interfaceNames are the names of the workspace interfaces file defines or references.
func (x *conformanceIndex) interfaceNames(file string) map[string]bool {
	out := map[string]bool{}
	for _, e := range x.g.out[fileID(file)] {
		if n := x.g.nodes[e.Target]; n.Kind == types.KindSymbol && n.Attrs[attrSymbolKind] == "Interface" {
			out[n.Label] = true
		}
	}
	return out
}

func (x *conformanceIndex) sourceFile(file string) bool {
	pop, ok := x.population(file)
	return ok && pop == types.ConformancePopSource
}

// storePathSuffixes end the name of a symbol that says where something lives on disk.
var storePathSuffixes = []string{"Path", "Dir", "File", "FileName", "Ext"}

// stateWriters are the internal/file writers a store goes through; a package whose code
// references one keeps state. Writes made with os directly are invisible: the index holds
// no standard library symbols.
var stateWriters = map[string]bool{
	"WriteFileAtomic": true, "WriteFrom": true, "ReplaceFile": true, "AppendLines": true, "RewriteLines": true, "JSONL": true,
}

// storePathSamples measures each exported path of a package that keeps state by how many
// measured non-test files outside its package reference it: only a store's owner may know
// where it lives. Home is that owner.
func (x *conformanceIndex) storePathSamples(add conformanceAdd) {
	stores := x.stateDirs()
	for _, s := range x.symbols() {
		if s.pop == types.ConformancePopTest || s.node.Attrs[attrLanguage] != "go" || len(s.local) != 1 ||
			!exportedName(s.local[0].Name) || !storePath(s.node, s.local[0].Name) {
			continue
		}
		file, _, _ := splitPathLine(s.node.Source)
		own := path.Dir(file)
		if !stores[own] {
			continue
		}
		n := 0
		for _, c := range x.packageReach(s.id, own, types.RelationReferences, true, x.measuredCode) {
			n += c
		}
		add(types.ConformanceStorePathExport, s.pop, "", types.ConformanceItem{ID: s.itemID, Source: s.node.Source, Measure: n, Home: own})
	}
}

// storePath reports whether n, named name, says where something lives: a string constant
// or variable, or a function returning a string, named with a storePathSuffix.
func storePath(n types.KnowledgeNode, name string) bool {
	if !slices.ContainsFunc(storePathSuffixes, func(suffix string) bool { return strings.HasSuffix(name, suffix) }) {
		return false
	}
	sig := n.Attrs[AttrSignature]
	switch n.Attrs[attrSymbolKind] {
	case "Constant", "Variable":
		_, typ, _ := strings.Cut(sig, fmt.Sprintf(" %s ", name))
		return strings.HasPrefix(strings.TrimPrefix(typ, "untyped "), "string")
	case "Function":
		_, results, ok := goFuncParts(sig)
		return ok && (results == "string" || results == "(string, error)")
	}
	return false
}

func (x *conformanceIndex) measuredCode(file string) bool {
	pop, ok := x.population(file)
	return ok && pop != types.ConformancePopTest
}

// stateDirs are the package directories whose non-test files reference a stateWriter of a
// package ending in internal/file.
func (x *conformanceIndex) stateDirs() map[string]bool {
	out := map[string]bool{}
	for id, f := range x.g.nodes {
		if f.Kind != types.KindFile || types.IsTestPath(f.Source) {
			continue
		}
		for _, e := range x.g.out[id] {
			if e.Relation != types.RelationReferences {
				continue
			}
			ns := x.g.nodes[e.Target].Attrs[attrNamespace]
			local := x.local[e.Target]
			if len(local) == 1 && stateWriters[local[0].Name] && strings.HasSuffix(ns, "/internal/file`/") {
				out[path.Dir(f.Source)] = true
				break
			}
		}
	}
	return out
}

// fileSplitSamples measures each concept sibling by the lines it and its root would make as
// one file. Home is the root; Unshared marks a sibling sharing no symbol with it, and Rename
// the conceptName of one whose suffix is more than one segment.
func (x *conformanceIndex) fileSplitSamples(add conformanceAdd) {
	for id, f := range x.g.nodes {
		if f.Kind != types.KindFile {
			continue
		}
		pop, ok := x.population(f.Source)
		if !ok || pop == types.ConformancePopTest {
			continue
		}
		for _, e := range x.g.out[id] {
			if e.Relation != types.RelationSplits || e.Attrs[attrSplitClass] != splitConcept {
				continue
			}
			root := x.g.nodes[e.Target]
			lines, err := strconv.Atoi(f.Attrs[AttrLines])
			rootLines, rootErr := strconv.Atoi(root.Attrs[AttrLines])
			if _, measured := x.population(root.Source); err != nil || rootErr != nil || !measured {
				continue
			}
			add(types.ConformanceFileSplit, pop, "", types.ConformanceItem{ID: f.Source, Source: f.Source + ":1",
				Measure: lines + rootLines, Home: root.Source, Unshared: e.Attrs[attrSplitCoupled] != "true",
				Rename: conceptName(root.Source, e.Attrs[attrSplitSuffix])})
		}
	}
}

// conceptName is the file a sibling of root with suffix should be, or "" when suffix is one
// segment. splitRoot takes the longest existing root, so a suffix of two segments means the
// file its first segment names does not exist.
func conceptName(root, suffix string) string {
	first, _, many := strings.Cut(suffix, "_")
	if !many {
		return ""
	}
	ext := path.Ext(root)
	return strings.TrimSuffix(root, ext) + "_" + first + ext
}

// itemPast reports whether it is past value in a row of m: by measure, or for file-split by
// a name that splits more than one concept.
func itemPast(m types.ConformanceMetric, it types.ConformanceItem, value int) bool {
	return m.Past(it.Measure, value) || it.Rename != ""
}
