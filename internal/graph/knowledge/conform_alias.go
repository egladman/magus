package knowledge

import (
	"cmp"
	"maps"
	"math"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// ImportAliases reports how each workspace package with an importer is imported: under its
// own name or an alias, how often an alias was needed, and which packages of the same name
// its importers also import. Packages are sorted by Dir. An importer is any file that
// references the package and does not define it, test files included; a generated file is
// its generator's choice and is not counted. A package is placed only where its index
// records a package clause (Go), so a language with no reader contributes nothing. With no
// symbol index, Indexed is false and Packages empty, never nil.
func (g *Graph) ImportAliases() types.ImportAliasReport {
	out := types.ImportAliasReport{Indexed: g.HasSymbols(), Packages: []types.AliasFacts{}}
	if !out.Indexed {
		return out
	}
	for _, p := range importAliases(g, func(file string) bool { return conformGenerated(g, types.ConformOptions{}, file) }) {
		if p.facts.Importers > 0 {
			out.Packages = append(out.Packages, p.facts)
		}
	}
	return out
}

// aliasPackage is one namespace an import can name: a workspace package, with its non-test
// defining files and the importers counted, or a package outside the workspace.
type aliasPackage struct {
	ns, dir, name, source string
	ours                  bool
	files                 []string
	importers             []aliasImport
	facts                 types.AliasFacts
}

// aliasImport is one file importing a package, under alias when it writes one.
type aliasImport struct{ file, alias string }

// importAliases measures every workspace package with a package clause, importers or none,
// in Dir order. skip leaves an importing file out.
func importAliases(g *Graph, skip func(file string) bool) []*aliasPackage {
	g.ensureAdj()
	pkgs := map[string]*aliasPackage{}
	for id, n := range g.nodes {
		if !g.isNamespace(id) {
			continue
		}
		p := &aliasPackage{ns: id, source: n.Source}
		defines := map[string]bool{}
		for _, e := range g.in[id] {
			file, ok := strings.CutPrefix(e.Source, types.KindFile+":")
			if !ok || e.Relation != types.RelationDefines {
				continue
			}
			defines[file] = true
			if !types.IsTestPath(file) {
				p.files = append(p.files, file)
				if d := path.Dir(file); p.dir == "" || d < p.dir {
					p.dir = d
				}
			}
		}
		slices.Sort(p.files)
		p.ours = len(p.files) > 0
		switch {
		case p.ours:
			p.name = n.Attrs[attrPackageName]
		case n.Attrs[attrLanguage] == "go":
			p.dir = importPath(id)
			p.name = externalPackageName(p.dir)
		}
		if p.name == "" {
			continue
		}
		for _, e := range g.in[id] {
			file, ok := strings.CutPrefix(e.Source, types.KindFile+":")
			if !ok || e.Relation != types.RelationReferences || skip(file) || defines[file] {
				continue
			}
			p.importers = append(p.importers, aliasImport{file: file, alias: e.Attrs[attrImportAlias]})
		}
		slices.SortFunc(p.importers, func(a, b aliasImport) int { return cmp.Compare(a.file, b.file) })
		pkgs[id] = p
	}

	byName := map[string][]*aliasPackage{}
	imports := map[string]map[string]bool{}
	for _, p := range pkgs {
		byName[p.name] = append(byName[p.name], p)
		for _, imp := range p.importers {
			if imports[imp.file] == nil {
				imports[imp.file] = map[string]bool{}
			}
			imports[imp.file][p.ns] = true
		}
	}
	fileNS := g.fileNamespaces()

	var out []*aliasPackage
	for _, p := range pkgs {
		if !p.ours {
			continue
		}
		f := types.AliasFacts{Dir: p.dir, Name: p.name, Importers: len(p.importers), Aliases: map[string]int{}}
		for _, imp := range p.importers {
			if imp.alias == "" {
				continue
			}
			f.Aliased++
			f.Aliases[imp.alias]++
			inside := slices.ContainsFunc(fileNS[fileID(imp.file)], func(ns string) bool {
				q, ok := pkgs[ns]
				return ok && q.name == p.name
			})
			if imp.alias != p.name && !inside {
				f.Needed++
			}
		}
		for _, q := range byName[p.name] {
			if q == p {
				continue
			}
			both := 0
			for _, imp := range p.importers {
				if imports[imp.file][q.ns] {
					both++
				}
			}
			if both > 0 {
				f.Clashes = append(f.Clashes, types.AliasClash{Dir: q.dir, Name: q.name, Both: both, Importers: len(q.importers), Ours: q.ours})
			}
		}
		slices.SortFunc(f.Clashes, func(a, b types.AliasClash) int { return cmp.Compare(a.Dir, b.Dir) })
		p.facts = f
		out = append(out, p)
	}
	slices.SortFunc(out, func(a, b *aliasPackage) int { return cmp.Or(cmp.Compare(a.dir, b.dir), cmp.Compare(a.ns, b.ns)) })
	return out
}

// importPath is a namespace symbol's package path, the descriptor its ID ends in.
func importPath(ns string) string {
	if p, ok := parseDescriptors(ns); ok && len(p) > 0 {
		return p[len(p)-1].Name
	}
	return ""
}

// majorVersion is the /vN element a module path ends in when its major version is 2 or
// more; the package's name is the element before it.
var majorVersion = regexp.MustCompile(`/v[2-9][0-9]*$`)

// externalPackageName is the name a package outside the workspace is imported under by
// default: the last element of its path, a major version suffix dropped.
func externalPackageName(pkgPath string) string {
	return path.Base(majorVersion.ReplaceAllString(pkgPath, ""))
}

// aliasSamples measures each judged package: one not generated and not excluded by o, with
// at least the key's MinSubject importers (1 when 0). alias-share samples every judged
// package with the percent of its importers that needed an alias; name-clash samples only
// a package blamed for a clash, with the most percent of its importers that also import a
// package of its name. A clash blames the package with fewer importers, both on a tie, and
// ours when the partner is outside the workspace. Items are package directories in order.
func aliasSamples(g *Graph, o types.ConformOptions) map[types.ConformKey][]types.ConformItem {
	out := map[types.ConformKey][]types.ConformItem{}
	if !g.HasSymbols() {
		return out
	}
	shareKey, clashKey := couplingKey(types.ConformAliasShare), couplingKey(types.ConformNameClash)
	shareLimit, _ := conformLimit(o, shareKey)
	clashLimit, _ := conformLimit(o, clashKey)
	for _, p := range importAliases(g, func(file string) bool { return conformGenerated(g, o, file) || conformExcludes(o, file) }) {
		if !slices.ContainsFunc(p.files, func(f string) bool { return !conformGenerated(g, o, f) && !conformExcludes(o, f) }) {
			continue
		}
		f := p.facts
		if f.Importers >= max(1, shareLimit.MinSubject) {
			out[shareKey] = append(out[shareKey], types.ConformItem{ID: f.Dir, Source: p.source, Measure: percent(f.Needed, f.Importers)})
		}
		if f.Importers < max(1, clashLimit.MinSubject) {
			continue
		}
		if share, blamed := clashShare(f); blamed {
			out[clashKey] = append(out[clashKey], types.ConformItem{ID: f.Dir, Source: p.source, Measure: share})
		}
	}
	return out
}

// clashShare is the most percent of f's importers that also import a package of its name,
// over the clashes that blame f.
func clashShare(f types.AliasFacts) (share int, blamed bool) {
	for _, c := range f.Clashes {
		if !c.Ours || f.Importers <= c.Importers {
			share, blamed = max(share, percent(c.Both, f.Importers)), true
		}
	}
	return share, blamed
}

func percent(n, of int) int {
	if of == 0 {
		return 0
	}
	return int(math.Round(100 * float64(n) / float64(of)))
}

// aliasSpellings are f's aliases, most used first, then by name.
func aliasSpellings(f types.AliasFacts) []string {
	return slices.SortedFunc(maps.Keys(f.Aliases), func(a, b string) int {
		return cmp.Or(cmp.Compare(f.Aliases[b], f.Aliases[a]), cmp.Compare(a, b))
	})
}
