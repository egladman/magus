package knowledge

import (
	"cmp"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// The precedent miner counts, over the whole index, the shapes a later rule would measure a
// change against. It reads the same facts the conformance lens does (namespaces, defines and
// references edges, calls, the declaration shape a language's reader reports) and applies the
// same gates, so a row it calls established is one the lens would too.

// precedentCited caps the following cases a row names.
const precedentCited = 3

// PrecedentOptions scopes [Graph.Precedents].
type PrecedentOptions struct {
	// Generated holds the workspace-relative paths that are generated output. Generated and
	// test code is never counted.
	Generated map[string]bool
}

// Precedents mines the precedents g's cases establish; g must carry its symbols. Rows come
// back ordered by family and scope, so two runs over one graph agree byte for byte. A graph
// with no symbols yields no rows, never an error.
func (g *Graph) Precedents(o PrecedentOptions) []types.Precedent {
	x := newNamingIndex(g, ConformanceChange{Generated: o.Generated})
	m := precedentMiner{g: g, x: x, fileNS: g.fileNamespaces()}
	pk := m.packages()
	rows := slices.Concat(m.depDirection(pk), m.depFanout(pk), m.errSentinelName(), m.testPackageName())
	for i := range rows {
		r := &rows[i]
		if r.Cohort > 0 {
			r.Share = float64(r.Follow) / float64(r.Cohort)
		}
		// A fan-out row's share is at least 0.95 by construction, since its key is the p95, so
		// only its cohort says anything.
		r.Established = r.Cohort >= conformanceMinCohort && (r.Family == types.PrecedentDepFanout || r.Share >= conformanceMinShare)
	}
	slices.SortFunc(rows, func(a, b types.Precedent) int {
		return cmp.Or(
			cmp.Compare(a.Family, b.Family),
			cmp.Compare(a.Scope.Language, b.Scope.Language),
			slices.Compare(a.Scope.Layers, b.Scope.Layers),
		)
	})
	return rows
}

type precedentMiner struct {
	g      *Graph
	x      *namingIndex
	fileNS map[string][]string
}

// precedentPackages places each workspace package: its directory and the top-level directory
// (its layer) that holds it.
type precedentPackages struct {
	// deps leaves out what generated files import: a generated package is imported like any
	// other, but what it imports is its generator's decision, even beside hand-written files.
	deps  packageGraph
	dir   map[string]string
	layer map[string]string
	// ns is each directory's first namespace, the node a dep case names.
	ns map[string]string
}

// packages takes each package's layer from the first segment of its directory, and "." for
// the workspace root. The path alone decides it: a top-level directory holding only nested
// projects may have no node of its own. No directory name is special.
func (m precedentMiner) packages() precedentPackages {
	pk := precedentPackages{
		deps: m.g.packageDepsExcept(m.x.generated), dir: map[string]string{}, layer: map[string]string{},
		ns: map[string]string{},
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
		}
	}
	for ns, dir := range pk.dir {
		if cur, ok := pk.ns[dir]; !ok || ns < cur {
			pk.ns[dir] = ns
		}
		pk.layer[ns], _, _ = strings.Cut(dir, "/")
	}
	return pk
}

// depCase names the package in dir as a case importing imports.
func (pk precedentPackages) depCase(dir string, imports ...string) types.Case {
	return types.Case{Node: pk.ns[dir], Source: dir, Imports: imports}
}

type dirEdge struct{ from, to string }

// depDirection counts, for each pair of layers, the package imports running each way. The
// majority direction is the key and the minority edges are the departures. Imports within
// one layer say nothing about direction and are not counted.
func (m precedentMiner) depDirection(pk precedentPackages) []types.Precedent {
	edges := map[[2]string]map[dirEdge]bool{}
	for from, tos := range pk.deps {
		lf := pk.layer[from]
		if lf == "" {
			continue
		}
		for to := range tos {
			lt := pk.layer[to]
			if lt == "" || lt == lf {
				continue
			}
			k := [2]string{lf, lt}
			if edges[k] == nil {
				edges[k] = map[dirEdge]bool{}
			}
			edges[k][dirEdge{pk.dir[from], pk.dir[to]}] = true
		}
	}
	pairs := map[[2]string]bool{}
	for k := range edges {
		pairs[[2]string{min(k[0], k[1]), max(k[0], k[1])}] = true
	}
	out := make([]types.Precedent, 0, len(pairs))
	for p := range pairs {
		fwd, back := p, [2]string{p[1], p[0]}
		if len(edges[back]) > len(edges[fwd]) {
			fwd, back = back, fwd
		}
		follow := slices.SortedFunc(maps.Keys(edges[fwd]), compareDirEdges)
		depart := slices.SortedFunc(maps.Keys(edges[back]), compareDirEdges)
		row := types.Precedent{
			Family: types.PrecedentDepDirection,
			Scope:  types.PrecedentScope{Layers: []string{p[0], p[1]}},
			Key:    types.PrecedentKey{From: fwd[0], To: fwd[1]},
			Follow: len(follow),
			Cohort: len(follow) + len(depart),
		}
		for _, e := range follow[:min(len(follow), precedentCited)] {
			row.Cited = append(row.Cited, pk.depCase(e.from, e.to))
		}
		for _, e := range depart {
			row.Departures = append(row.Departures, pk.depCase(e.from, e.to))
		}
		out = append(out, row)
	}
	return out
}

func compareDirEdges(a, b dirEdge) int {
	return cmp.Or(cmp.Compare(a.from, b.from), cmp.Compare(a.to, b.to))
}

// depFanout is, per layer, the distribution of how many distinct packages in it each
// importing package imports. The cohort is the packages that import the layer at all: a
// package that never does says nothing about how many is usual.
func (m precedentMiner) depFanout(pk precedentPackages) []types.Precedent {
	// Keyed by directory, not namespace: a language that makes each file a module has many
	// namespaces per directory, and counting each would add one import per importing file.
	imports := map[string]map[string]map[string]bool{}
	for from, tos := range pk.deps {
		if pk.layer[from] == "" {
			continue
		}
		for to := range tos {
			lt := pk.layer[to]
			if lt == "" || pk.dir[to] == pk.dir[from] {
				continue
			}
			if imports[lt] == nil {
				imports[lt] = map[string]map[string]bool{}
			}
			if imports[lt][pk.dir[from]] == nil {
				imports[lt][pk.dir[from]] = map[string]bool{}
			}
			imports[lt][pk.dir[from]][pk.dir[to]] = true
		}
	}
	out := make([]types.Precedent, 0, len(imports))
	for layer, byDir := range imports {
		dirs := slices.SortedFunc(maps.Keys(byDir), func(a, b string) int {
			return cmp.Or(cmp.Compare(len(byDir[b]), len(byDir[a])), cmp.Compare(a, b))
		})
		counts := make([]int, len(dirs))
		for i, d := range dirs {
			counts[i] = len(byDir[d])
		}
		slices.Sort(counts)
		// Nearest rank, ceil(0.95n)-1 in integers. A floor index is the max for every cohort
		// under 21, so no case there could ever depart.
		bound := counts[(95*len(counts)+99)/100-1]
		row := types.Precedent{
			Family: types.PrecedentDepFanout,
			Scope:  types.PrecedentScope{Layers: []string{layer}},
			Key:    types.PrecedentKey{MaxImports: bound},
			Cohort: len(dirs),
		}
		for _, d := range dirs {
			c := pk.depCase(d, slices.Sorted(maps.Keys(byDir[d]))...)
			if len(c.Imports) > bound {
				row.Departures = append(row.Departures, c)
				continue
			}
			row.Follow++
			if len(row.Cited) < precedentCited {
				row.Cited = append(row.Cited, c)
			}
		}
		out = append(out, row)
	}
	return out
}

// errSentinelName counts the values a language's reader types as error, by whether the
// name's first word starts with err.
func (m precedentMiner) errSentinelName() []types.Precedent {
	byLang := map[string]*types.Precedent{}
	for _, id := range slices.Sorted(maps.Keys(m.x.byID)) {
		d := m.x.byID[id]
		if d.shape.Kind != declValue || d.shape.Meaning != "error" {
			continue
		}
		row := byLang[d.language]
		if row == nil {
			row = &types.Precedent{
				Family: types.PrecedentErrSentinelName,
				Scope:  types.PrecedentScope{Language: d.language},
				Key:    types.PrecedentKey{Prefix: "err"},
			}
			byLang[d.language] = row
		}
		row.Cohort++
		c := types.Case{Node: id, Source: d.source}
		if !strings.HasPrefix(d.folded[0], row.Key.Prefix) {
			row.Departures = append(row.Departures, c)
			continue
		}
		row.Follow++
		if len(row.Cited) < precedentCited {
			row.Cited = append(row.Cited, c)
		}
	}
	return derefPrecedents(byLang)
}

// testPackageName counts test files by whether they declare the package their directory's
// other sources declare. Only a language that packages by directory is judged: one whose
// directories of several sources almost always share one namespace. A language that gives
// every file its own module has no package for a test to agree with.
func (m precedentMiner) testPackageName() []types.Precedent {
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

	byLang := map[string]*types.Precedent{}
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
			row = &types.Precedent{Family: types.PrecedentTestPackageName, Scope: types.PrecedentScope{Language: lang}}
			byLang[lang] = row
		}
		row.Cohort++
		c := types.Case{Node: id, Source: n.Source}
		if !slices.ContainsFunc(nss, func(ns string) bool { return siblings[ns] }) {
			row.Departures = append(row.Departures, c)
			continue
		}
		row.Follow++
		if len(row.Cited) < precedentCited {
			row.Cited = append(row.Cited, c)
		}
	}
	return derefPrecedents(byLang)
}

func derefPrecedents(m map[string]*types.Precedent) []types.Precedent {
	out := make([]types.Precedent, 0, len(m))
	for _, r := range m {
		out = append(out, *r)
	}
	return out
}
