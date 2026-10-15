package knowledge

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// Scores of aliasCheck's findings: an aliased import of a package its importers rename, a
// little sharper when the change also adds that package, and a new package taking an
// existing one's name.
const (
	aliasPastScore       = 0.5
	aliasIntroducedScore = 0.6
	aliasTakenScore      = 0.5
)

// aliasCheck reads an import that must be aliased as advice that a package name is wrong. It
// reports two cases. One is an aliased import, in a file holding a symbol the change adds,
// renames or re-signs, of a package past the alias-share or name-clash value
// [types.ConformOptions.Effective] enforces and listed in no known entry of that metric.
// The other is a package the change introduces, every declaration of it new, whose package
// clause an existing non-generated package already declares: the cheapest moment to rename it. The
// index holds no base, so an aliased import already in the file is reported as well as one
// the change adds. Each finding goes on the file's or package's first subject by ID. With
// no symbol index it is silent, and a language with no package clause reader yields nothing.
type aliasCheck struct{}

func (aliasCheck) name() string { return types.CheckImportAlias }

func (aliasCheck) run(x *namingIndex) []finding {
	if len(x.subjects) == 0 {
		return nil
	}
	o := x.change.Conform
	skip := func(file string) bool { return conformGenerated(x.g, o, file) || conformExcludes(o, file) }
	pkgs := map[string]*aliasPackage{}
	byName := map[string][]*aliasPackage{}
	for _, p := range importAliases(x.g, skip) {
		pkgs[p.ns] = p
		byName[p.name] = append(byName[p.name], p)
	}
	measured := map[types.ConformMetric]map[string]int{}
	for k, items := range aliasSamples(x.g, o) {
		measured[k.Metric] = map[string]int{}
		for _, it := range items {
			measured[k.Metric][it.ID] = it.Measure
		}
	}
	known := conformKnownItems(o.Ratchet.Known)
	introduced := x.introducedPackages()

	byFile := map[string]*namingDecl{}
	for _, s := range x.subjects {
		if cur, ok := byFile[s.file()]; !ok || s.id < cur.id {
			byFile[s.file()] = s
		}
	}
	var out []finding
	for _, file := range slices.Sorted(maps.Keys(byFile)) {
		for _, e := range x.g.out[fileID(file)] {
			p, ok := pkgs[e.Target]
			if e.Relation != types.RelationReferences || !ok || e.Attrs[attrImportAlias] == "" {
				continue
			}
			var past []string
			for _, m := range []types.ConformMetric{types.ConformNameClash, types.ConformAliasShare} {
				value, src := o.Effective(couplingKey(m))
				if v, ok := measured[m][p.dir]; ok && src != "" && v > value && !known[m][p.dir] {
					past = append(past, fmt.Sprintf("%s source: %d, past the %s of %d", m, v, thresholdSource(src), value))
				}
			}
			if len(past) == 0 {
				continue
			}
			score := aliasPastScore
			if introduced[p.ns] != nil {
				score = aliasIntroducedScore
			}
			details := aliasImporters(p)
			details = append(details, past...)
			details = append(details, "rename the package, or record a known entry with the reason")
			out = append(out, finding{subject: byFile[file].id, score: score, message: aliasMessage(p.facts), details: details})
		}
	}

	for _, ns := range slices.Sorted(maps.Keys(introduced)) {
		p := pkgs[ns]
		if p == nil || !slices.ContainsFunc(p.files, func(f string) bool { return !skip(f) }) {
			continue
		}
		var taken []string
		for _, q := range byName[p.name] {
			if q != p && introduced[q.ns] == nil && slices.ContainsFunc(q.files, func(f string) bool { return !skip(f) }) {
				taken = append(taken, fmt.Sprintf("%s (%d importers)", q.dir, len(q.importers)))
			}
		}
		if len(taken) == 0 {
			continue
		}
		slices.Sort(taken)
		out = append(out, finding{
			subject: introduced[ns].id,
			score:   aliasTakenScore,
			message: fmt.Sprintf("%s takes the package name %s, which %s already declares: consider another name before anything imports both",
				p.dir, p.name, strings.Join(taken, ", ")),
		})
	}
	return out
}

// introducedPackages maps each namespace the change introduces, every declaration the index
// places in it new, to its first subject by ID.
func (x *namingIndex) introducedPackages() map[string]*namingDecl {
	fresh := map[string]bool{}
	for _, d := range x.byID {
		if _, seen := fresh[d.scope]; !seen {
			fresh[d.scope] = true
		}
		fresh[d.scope] = fresh[d.scope] && x.change.Introduced[d.id]
	}
	out := map[string]*namingDecl{}
	for _, s := range x.subjects {
		if cur := out[s.scope]; fresh[s.scope] && (cur == nil || s.id < cur.id) {
			out[s.scope] = s
		}
	}
	return out
}

// aliasMessage states how a package is imported under other names: the share and the one
// spelling to name it after, or the spellings to choose between, and the clash that blames it.
func aliasMessage(f types.AliasFacts) string {
	if share, blamed := clashShare(f); blamed {
		partner := ""
		for _, c := range f.Clashes {
			if percent(c.Both, f.Importers) == share {
				partner = c.Dir
				break
			}
		}
		return fmt.Sprintf("%s shares the package name %s with %s, and %d%% of its %d importers import both: consider renaming %s",
			f.Dir, f.Name, partner, share, f.Importers, f.Dir)
	}
	spellings := aliasSpellings(f)
	if len(spellings) == 1 {
		return fmt.Sprintf("%s is aliased by %d of its %d importers, always as %s: consider naming the package %s",
			f.Dir, f.Aliased, f.Importers, spellings[0], spellings[0])
	}
	return fmt.Sprintf("%s is aliased by %d of its %d importers in %d spellings (%s): pick one name",
		f.Dir, f.Aliased, f.Importers, len(spellings), strings.Join(spellings, ", "))
}

// aliasImporters names every importer of p, with the alias it writes.
func aliasImporters(p *aliasPackage) []string {
	out := make([]string, len(p.importers))
	for i, imp := range p.importers {
		out[i] = "importer: " + imp.file
		if imp.alias != "" {
			out[i] += " as " + imp.alias
		}
	}
	return out
}
