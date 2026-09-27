package knowledge

import (
	"cmp"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// The norm miner counts, over the whole index, the shapes a later rule would measure a change
// against. It reads the same facts the conformance lens does (namespaces, defines and
// references edges, calls, the declaration shape a language's reader reports) and applies the
// same gates, so a row the table calls a norm is one the lens would too.

// normExamples caps the agreeing members a row names.
const normExamples = 3

// NormOptions scopes [Graph.Norms].
type NormOptions struct {
	// Generated holds the workspace-relative paths that are generated output. Generated and
	// test code is never counted.
	Generated map[string]bool
}

// Norms mines the norm table from g, which must carry its symbols. Rows come back ordered by
// family, scope and key, so two runs over one graph agree byte for byte. A graph with no
// symbols yields no rows, never an error.
func (g *Graph) Norms(o NormOptions) []types.Norm {
	x := newNamingIndex(g, ConformanceChange{Generated: o.Generated})
	m := normMiner{g: g, x: x, fileNS: g.fileNamespaces()}
	pk := m.packages()
	rows := slices.Concat(m.depDirection(pk), m.depFanout(pk), m.errSentinelName(), m.testPackageName())
	for i := range rows {
		r := &rows[i]
		if r.Cohort > 0 {
			r.Share = float64(r.Agree) / float64(r.Cohort)
		}
		r.Silent = r.Cohort < conformanceMinCohort || r.Share < conformanceMinShare
	}
	slices.SortFunc(rows, func(a, b types.Norm) int {
		return cmp.Or(cmp.Compare(a.Family, b.Family), cmp.Compare(a.Scope, b.Scope), cmp.Compare(a.Key, b.Key))
	})
	return rows
}

type normMiner struct {
	g      *Graph
	x      *namingIndex
	fileNS map[string][]string
}

// normPackages places each workspace package: its directory and the top-level directory
// (its layer) that holds it.
type normPackages struct {
	deps  packageGraph
	dir   map[string]string
	layer map[string]string
	// authored holds the packages with a hand-written source. A wholly generated package is
	// imported like any other, but what it imports is its generator's decision.
	authored map[string]bool
}

// packages reads layers off the directory tree the graph already holds: a top-level dir or
// project node is a layer, and the workspace root is ".". No directory name is special.
func (m normMiner) packages() normPackages {
	tops := map[string]bool{}
	for _, n := range m.g.nodes {
		if (n.Kind == types.KindDir || n.Kind == types.KindProject) && n.Source != "." && !strings.Contains(n.Source, "/") {
			tops[n.Source] = true
		}
	}
	pk := normPackages{
		deps: m.g.packageDeps(), dir: map[string]string{}, layer: map[string]string{}, authored: map[string]bool{},
	}
	for id, nss := range m.fileNS {
		n := m.g.nodes[id]
		if n.Kind != types.KindFile || isTestSource(n.Source) {
			continue
		}
		dir := path.Dir(n.Source)
		for _, ns := range nss {
			if cur, ok := pk.dir[ns]; !ok || dir < cur {
				pk.dir[ns] = dir
			}
			if !m.x.generated(n.Source) {
				pk.authored[ns] = true
			}
		}
	}
	for ns, dir := range pk.dir {
		top, _, _ := strings.Cut(dir, "/")
		switch {
		case dir == ".":
			pk.layer[ns] = "."
		case tops[top]:
			pk.layer[ns] = top
		}
	}
	return pk
}

type normEdge struct{ from, to string }

func (e normEdge) String() string { return e.from + " -> " + e.to }

// depDirection counts, for each pair of layers, the package imports running each way. The
// majority direction is the key and the minority edges are the deviations. Imports within
// one layer say nothing about direction and are not counted.
func (m normMiner) depDirection(pk normPackages) []types.Norm {
	edges := map[[2]string]map[normEdge]bool{}
	for from, tos := range pk.deps {
		lf := pk.layer[from]
		if lf == "" || !pk.authored[from] {
			continue
		}
		for to := range tos {
			lt := pk.layer[to]
			if lt == "" || lt == lf {
				continue
			}
			k := [2]string{lf, lt}
			if edges[k] == nil {
				edges[k] = map[normEdge]bool{}
			}
			edges[k][normEdge{pk.dir[from], pk.dir[to]}] = true
		}
	}
	pairs := map[[2]string]bool{}
	for k := range edges {
		pairs[[2]string{min(k[0], k[1]), max(k[0], k[1])}] = true
	}
	var out []types.Norm
	for p := range pairs {
		fwd, back := p, [2]string{p[1], p[0]}
		if len(edges[back]) > len(edges[fwd]) {
			fwd, back = back, fwd
		}
		agree := slices.SortedFunc(maps.Keys(edges[fwd]), compareEdges)
		dev := slices.SortedFunc(maps.Keys(edges[back]), compareEdges)
		row := types.Norm{
			Family: types.NormDepDirection,
			Scope:  p[0] + ", " + p[1],
			Key:    fwd[0] + " -> " + fwd[1],
			Agree:  len(agree),
			Cohort: len(agree) + len(dev),
		}
		for _, e := range agree[:min(len(agree), normExamples)] {
			row.Examples = append(row.Examples, e.String())
		}
		for _, e := range dev {
			row.Deviations = append(row.Deviations, types.NormSite{Subject: e.String(), Source: e.from})
		}
		out = append(out, row)
	}
	return out
}

func compareEdges(a, b normEdge) int {
	return cmp.Or(cmp.Compare(a.from, b.from), cmp.Compare(a.to, b.to))
}

// depFanout is, per layer, the distribution of how many distinct packages in it each
// importing package imports. The cohort is the packages that import the layer at all: a
// package that never does says nothing about how many is usual.
func (m normMiner) depFanout(pk normPackages) []types.Norm {
	// Keyed by directory, not namespace: a language that makes each file a module has many
	// namespaces per directory, and counting each would add one import per importing file.
	imports := map[string]map[normEdge]bool{}
	for from, tos := range pk.deps {
		if pk.layer[from] == "" || !pk.authored[from] {
			continue
		}
		for to := range tos {
			lt := pk.layer[to]
			if lt == "" || pk.dir[to] == pk.dir[from] {
				continue
			}
			if imports[lt] == nil {
				imports[lt] = map[normEdge]bool{}
			}
			imports[lt][normEdge{pk.dir[from], pk.dir[to]}] = true
		}
	}
	var out []types.Norm
	for layer, edges := range imports {
		byPkg := map[string]int{}
		for e := range edges {
			byPkg[e.from]++
		}
		pkgs := slices.SortedFunc(maps.Keys(byPkg), func(a, b string) int {
			return cmp.Or(cmp.Compare(byPkg[b], byPkg[a]), cmp.Compare(a, b))
		})
		vals := make([]int, len(pkgs))
		for i, p := range pkgs {
			vals[i] = byPkg[p]
		}
		slices.Sort(vals)
		q := func(p float64) int { return vals[min(len(vals)-1, int(p*float64(len(vals))))] }
		qs := &types.NormQuantiles{P95: q(0.95), Max: vals[len(vals)-1]}
		row := types.Norm{
			Family:    types.NormDepFanout,
			Scope:     layer,
			Key:       fmt.Sprintf("at most %d", qs.P95),
			Cohort:    len(pkgs),
			Quantiles: qs,
		}
		for _, p := range pkgs {
			if byPkg[p] > qs.P95 {
				row.Deviations = append(row.Deviations, types.NormSite{Subject: fmt.Sprintf("%s imports %d", p, byPkg[p]), Source: p})
				continue
			}
			row.Agree++
			if len(row.Examples) < normExamples {
				row.Examples = append(row.Examples, fmt.Sprintf("%s imports %d", p, byPkg[p]))
			}
		}
		out = append(out, row)
	}
	return out
}

// errSentinelName counts the values a language's reader types as error, by whether the
// name's first word starts with err.
func (m normMiner) errSentinelName() []types.Norm {
	byLang := map[string]*types.Norm{}
	for _, id := range slices.Sorted(maps.Keys(m.x.byID)) {
		d := m.x.byID[id]
		if d.shape.Kind != declValue || d.shape.Meaning != "error" {
			continue
		}
		row := byLang[d.language]
		if row == nil {
			row = &types.Norm{Family: types.NormErrSentinelName, Scope: d.language, Key: "err<X>"}
			byLang[d.language] = row
		}
		row.Cohort++
		name := path.Dir(d.file()) + "." + d.label
		if !strings.HasPrefix(d.folded[0], "err") {
			row.Deviations = append(row.Deviations, types.NormSite{Subject: name, Source: d.source})
			continue
		}
		row.Agree++
		if len(row.Examples) < normExamples {
			row.Examples = append(row.Examples, name)
		}
	}
	return derefRows(byLang)
}

// testPackageName counts test files by whether they declare the package their directory's
// other sources declare. Only a language that packages by directory is judged: one whose
// directories of several sources almost always share one namespace. A language that gives
// every file its own module has no package for a test to agree with.
func (m normMiner) testPackageName() []types.Norm {
	type dirLang struct{ dir, lang string }
	sources := map[dirLang]map[string]bool{}
	files := map[dirLang]int{}
	var tests []string
	for id, nss := range m.fileNS {
		n := m.g.nodes[id]
		if n.Kind != types.KindFile || m.x.generated(n.Source) {
			continue
		}
		if isTestSource(n.Source) {
			tests = append(tests, id)
			continue
		}
		for _, ns := range nss {
			k := dirLang{path.Dir(n.Source), m.g.nodes[ns].Attrs[attrLanguage]}
			if sources[k] == nil {
				sources[k] = map[string]bool{}
			}
			sources[k][ns] = true
		}
		if len(nss) > 0 {
			files[dirLang{path.Dir(n.Source), m.g.nodes[nss[0]].Attrs[attrLanguage]}]++
		}
	}
	shared, split := map[string]int{}, map[string]int{}
	for k, nss := range sources {
		switch {
		case files[k] < 2:
		case len(nss) == 1:
			shared[k.lang]++
		default:
			split[k.lang]++
		}
	}

	byLang := map[string]*types.Norm{}
	slices.Sort(tests)
	for _, id := range tests {
		n := m.g.nodes[id]
		nss := m.fileNS[id]
		lang := m.g.nodes[nss[0]].Attrs[attrLanguage]
		if shared[lang] <= split[lang] {
			continue
		}
		siblings := sources[dirLang{path.Dir(n.Source), lang}]
		if len(siblings) != 1 {
			continue
		}
		row := byLang[lang]
		if row == nil {
			row = &types.Norm{Family: types.NormTestPackageName, Scope: lang, Key: "its directory's package"}
			byLang[lang] = row
		}
		row.Cohort++
		if !slices.ContainsFunc(nss, func(ns string) bool { return siblings[ns] }) {
			row.Deviations = append(row.Deviations, types.NormSite{
				Subject: n.Source + " (package " + m.g.nodes[nss[0]].Label + ")", Source: n.Source,
			})
			continue
		}
		row.Agree++
		if len(row.Examples) < normExamples {
			row.Examples = append(row.Examples, n.Source)
		}
	}
	return derefRows(byLang)
}

func derefRows(m map[string]*types.Norm) []types.Norm {
	out := make([]types.Norm, 0, len(m))
	for _, r := range m {
		out = append(out, *r)
	}
	return out
}
