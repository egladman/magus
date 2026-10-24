package knowledge

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"path"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// spreadUnrelated is the spread of callers that meet only through the function they share:
// past every finite ceiling.
const spreadUnrelated = math.MaxInt32

// minOptionCallers is how many caller packages a function needs before an option one of them
// alone sets is a knob: with two, every option one sets is trivially that caller's.
const minOptionCallers = 3

// The kinds of mode knob.
const (
	knobBranch = "branch"
	knobOption = "option"
)

// sharedUse is one function other packages call. ID is its conformance item ID; Body its lines;
// Callers the package directories outside its own that reference it, sorted; Spread the most
// import hops between two of them (zero for one caller); Knobs what bends it per caller,
// read only for two or more callers.
type sharedUse struct {
	ID, Source string
	Body       int
	Callers    []string
	Spread     int
	Knobs      []modeKnob
}

// modeKnob is one way a caller bends a shared function. Kind is knobBranch, a parameter the
// body switches on or tests bare, or knobOption, an options field or With function that only
// Setter, one caller package, uses.
type modeKnob struct {
	Name, Kind, Setter string
}

// sharedUses is every measured function or method, or each of only when it is non-nil, that
// a package other than its own references, in symbol ID order. Callers are read from the
// non-test files the population counts, so generated and excluded code calls nothing. It is
// empty with no symbol index.
func (g *Graph) sharedUses(o types.ConformanceOptions, only map[string]bool) []sharedUse {
	if !g.HasSymbols() {
		return nil
	}
	x := newConformanceIndex(g, o)
	ids := slices.Sorted(maps.Keys(x.local))
	if only != nil {
		ids = slices.Sorted(maps.Keys(only))
	}
	ig := g.ImportGraph()
	var out []sharedUse
	for _, id := range ids {
		local := x.local[id]
		if k := neutralKind(local); k != declFunction && k != declMethod {
			continue
		}
		n := g.nodes[id]
		file, _, ok := splitPathLine(n.Source)
		if !ok {
			continue
		}
		if pop, ok := x.population(file); !ok || pop == types.ConformancePopTest {
			continue
		}
		definer := path.Dir(file)
		callers := map[string]bool{}
		for _, f := range x.callerFiles(id) {
			if d := path.Dir(f); d != definer {
				callers[d] = true
			}
		}
		if len(callers) == 0 {
			continue
		}
		u := sharedUse{ID: symbolItemID(file, local), Source: n.Source, Callers: slices.Sorted(maps.Keys(callers))}
		if start, end := definitionSpan(n); start > 0 && end >= start {
			u.Body = end - start + 1
		}
		if len(callers) >= 2 {
			u.Spread = callerSpread(ig, definer, u.Callers)
			u.Knobs = modeKnobs(g, n, symbolShape(n, local), callers)
		}
		out = append(out, u)
	}
	return out
}

// callerFiles is every measured non-test file that references id, sorted.
func (x *conformanceIndex) callerFiles(id string) []string {
	var out []string
	for _, e := range x.g.in[id] {
		file, ok := strings.CutPrefix(e.Source, types.KindFile+":")
		if e.Relation != types.RelationReferences || !ok {
			continue
		}
		if pop, ok := x.population(file); ok && pop != types.ConformancePopTest && !slices.Contains(out, file) {
			out = append(out, file)
		}
	}
	slices.Sort(out)
	return out
}

// symbolShape is the declaration shape the symbol's language reader reports.
func symbolShape(n types.KnowledgeNode, local []descriptor) declShape {
	return pickShapeReader(n.Attrs[attrLanguage]).read(symbolFacts{
		Kind: n.Attrs[attrSymbolKind], Signature: n.Attrs[AttrSignature], Path: local, Neutral: neutralKind(local),
	})
}

// callerSpread is the most import hops between two of callers over ig, read in either
// direction, with definer and its edges removed: how far apart the packages are that the
// function joins. A pair with no path left meets only through definer, which is
// spreadUnrelated. One caller has a spread of zero.
func callerSpread(ig types.ImportGraph, definer string, callers []string) int {
	adj := map[string][]string{}
	for from, tos := range ig.Packages {
		for _, to := range tos {
			if from != definer && to != definer {
				adj[from] = append(adj[from], to)
				adj[to] = append(adj[to], from)
			}
		}
	}
	spread := 0
	for i, a := range callers {
		dist := map[string]int{a: 0}
		for queue := []string{a}; len(queue) > 0; queue = queue[1:] {
			for _, next := range adj[queue[0]] {
				if _, seen := dist[next]; !seen {
					dist[next] = dist[queue[0]] + 1
					queue = append(queue, next)
				}
			}
		}
		for _, b := range callers[i+1:] {
			d, ok := dist[b]
			if !ok {
				return spreadUnrelated
			}
			spread = max(spread, d)
		}
	}
	return spread
}

// modeKnobs is what bends sym per caller, sorted by kind and name: each parameter of shape
// its body branches on, and, on a function minOptionCallers or more of callers call, each
// field of a workspace struct parameter or With function returning its variadic option type
// that exactly one caller package references. A func parameter is the caller's own code, not
// a knob, and data the body never branches on is not one either.
func modeKnobs(g *Graph, sym types.KnowledgeNode, shape declShape, callers map[string]bool) []modeKnob {
	branched := strings.Split(sym.Attrs[attrBranchIdents], ",")
	var out []modeKnob
	for _, p := range shape.Params {
		if p.Name != "" && slices.Contains(branched, p.Name) {
			out = append(out, modeKnob{Name: p.Name, Kind: knobBranch})
		}
		if len(callers) >= minOptionCallers {
			out = append(out, paramOptions(g, sym, p.Type, callers)...)
		}
	}
	slices.SortFunc(out, func(a, b modeKnob) int { return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Name, b.Name)) })
	return slices.CompactFunc(out, func(a, b modeKnob) bool { return a == b })
}

// paramOptions is the one-caller options a parameter of type typ offers: the fields of the
// workspace struct it names, or for a variadic ...T, the With functions of T's package
// returning T, each referenced from exactly one of callers.
func paramOptions(g *Graph, sym types.KnowledgeNode, typ string, callers map[string]bool) []modeKnob {
	typ, variadic := strings.CutPrefix(typ, "...")
	typ = strings.TrimLeft(typ, "*")
	if i := strings.IndexByte(typ, '['); i >= 0 {
		typ = typ[:i]
	}
	ns, name := typeNamespace(g, sym, typ)
	typeID := fmt.Sprintf("%s%s#", ns, name)
	if t, ok := g.nodes[typeID]; ns == "" || !ok || t.Kind != types.KindSymbol {
		return nil
	}
	var out []modeKnob
	for _, m := range namespaceMembers(g, ns) {
		p, _ := parseDescriptors(m)
		local := afterNamespace(p)
		if len(local) == 0 {
			continue
		}
		switch {
		case variadic && len(local) == 1 && local[0].Suffix == '(' && strings.HasPrefix(local[0].Name, "With") &&
			strings.HasSuffix(g.nodes[m].Attrs[AttrSignature], " "+name):
		case !variadic && strings.HasPrefix(m, typeID) && neutralKind(local) == declField:
		default:
			continue
		}
		setters := map[string]bool{}
		for _, e := range g.in[m] {
			file, ok := strings.CutPrefix(e.Source, types.KindFile+":")
			if e.Relation == types.RelationReferences && ok && !types.IsTestPath(file) && callers[path.Dir(file)] {
				setters[path.Dir(file)] = true
			}
		}
		if len(setters) == 1 {
			out = append(out, modeKnob{Name: local[len(local)-1].Name, Kind: knobOption, Setter: slices.Collect(maps.Keys(setters))[0]})
		}
	}
	return out
}

// typeNamespace places a parameter type: an unqualified name in sym's own namespace, and
// pkg.Name in the namespace sym's file imports whose package is named pkg. ns is empty when
// the type is not the workspace's.
func typeNamespace(g *Graph, sym types.KnowledgeNode, typ string) (ns, name string) {
	qual, name, qualified := strings.Cut(typ, ".")
	if !qualified {
		return sym.Attrs[attrNamespace], typ
	}
	file, _, _ := splitPathLine(sym.Source)
	for _, e := range g.out[fileID(file)] {
		if e.Relation != types.RelationReferences || !g.isNamespace(e.Target) {
			continue
		}
		if p, ok := parseDescriptors(e.Target); ok && len(p) == 1 && path.Base(p[0].Name) == qual {
			return e.Target, name
		}
	}
	return "", name
}

// namespaceMembers is every symbol a file defining ns defines in ns, sorted.
func namespaceMembers(g *Graph, ns string) []string {
	var out []string
	for _, def := range g.in[ns] {
		if def.Relation != types.RelationDefines {
			continue
		}
		for _, e := range g.out[def.Source] {
			if e.Relation == types.RelationDefines && e.Target != ns && g.nodes[e.Target].Attrs[attrNamespace] == ns {
				out = append(out, e.Target)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// overShared reports the coupling rule: a knob, a body past Effective(shared-body), and
// callers either past Effective(caller-spread) apart or more than Effective(shared-reach).
// Each conjunct alone fires on what is kept on purpose: far callers alone are a utility, a
// knob alone an edge flag, size alone a parser. With any of the three unconfigured it holds
// no verdict and reports false.
func (u sharedUse) overShared(o types.ConformanceOptions) bool {
	body, bodySrc := o.Effective(couplingKey(types.ConformanceSharedBody))
	spread, spreadSrc := o.Effective(couplingKey(types.ConformanceCallerSpread))
	reach, reachSrc := o.Effective(couplingKey(types.ConformanceSharedReach))
	if bodySrc == "" || spreadSrc == "" || reachSrc == "" {
		return false
	}
	return len(u.Knobs) > 0 && u.Body > body && (u.Spread > spread || len(u.Callers) > reach)
}

// couplingKey is m's key: the coupling metrics pool source and main code as source.
func couplingKey(m types.ConformanceMetric) types.ConformanceKey {
	return types.ConformanceKey{Metric: m, Population: types.ConformancePopSource}
}

// sharedLoops measures every shared function: its reach into shared-reach, and for two or
// more callers its body into shared-body and its spread into caller-spread, each list in ID
// order. violations are the functions overShared holds, each measured by its knob count, in
// ID order: the mode-knobs list.
func sharedLoops(g *Graph, o types.ConformanceOptions) (samples map[types.ConformanceKey][]types.ConformanceItem, violations []types.ConformanceItem) {
	samples = map[types.ConformanceKey][]types.ConformanceItem{}
	add := func(m types.ConformanceMetric, u sharedUse, measure int) {
		k := couplingKey(m)
		samples[k] = append(samples[k], types.ConformanceItem{ID: u.ID, Source: u.Source, Measure: measure})
	}
	for _, u := range g.sharedUses(o, nil) {
		add(types.ConformanceSharedReach, u, len(u.Callers))
		if len(u.Callers) < 2 {
			continue
		}
		add(types.ConformanceSharedBody, u, u.Body)
		add(types.ConformanceCallerSpread, u, u.Spread)
		if u.overShared(o) {
			violations = append(violations, types.ConformanceItem{ID: u.ID, Source: u.Source, Measure: len(u.Knobs)})
		}
	}
	byID := func(a, b types.ConformanceItem) int { return cmp.Compare(a.ID, b.ID) }
	for _, items := range samples {
		slices.SortFunc(items, byID)
	}
	slices.SortFunc(violations, byID)
	return samples, violations
}
