package knowledge

import (
	"cmp"
	"maps"
	"slices"

	"github.com/egladman/magus/types"
)

// needsPair is an ordered pair of target names.
type needsPair struct{ from, to string }

// needsDirection counts, per ordered pair of target names, the projects
// defining both by whether the first's needs reach the second. A
// cross-project need orders projects, not targets, so it is not followed.
// A pair no project follows makes no row.
func (m precedentMiner) needsDirection() []types.Precedent {
	m.g.ensureAdj()
	byProject := map[string][]string{}
	for _, n := range m.g.nodes {
		if n.Kind == types.KindTarget && n.Source != "" {
			byProject[n.Source] = append(byProject[n.Source], n.Label)
		}
	}
	follow, depart := map[needsPair][]string{}, map[needsPair][]string{}
	for project, names := range byProject {
		for _, a := range names {
			reach := m.needsReach(project, a)
			for _, b := range names {
				switch p := (needsPair{a, b}); {
				case a == b:
				case reach[b]:
					follow[p] = append(follow[p], project)
				default:
					depart[p] = append(depart[p], project)
				}
			}
		}
	}
	out := make([]types.Precedent, 0, len(follow))
	for _, p := range slices.SortedFunc(maps.Keys(follow), compareNeedsPairs) {
		f, d := slices.Sorted(slices.Values(follow[p])), slices.Sorted(slices.Values(depart[p]))
		row := types.Precedent{
			Family: types.PrecedentNeedsDirection,
			Key:    types.PrecedentKey{From: p.from, To: p.to},
			Follow: len(f),
			Cohort: len(f) + len(d),
		}
		for _, project := range f[:min(len(f), precedentCited)] {
			row.Cited = append(row.Cited, needsCase(project, p.from))
		}
		for _, project := range d {
			row.Departures = append(row.Departures, needsCase(project, p.from))
		}
		out = append(out, row)
	}
	return out
}

// needsReach is every target of project that name's needs reach, directly
// or through other targets of the same project.
func (m precedentMiner) needsReach(project, name string) map[string]bool {
	reach := map[string]bool{}
	queue := []string{targetID(project, name)}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, e := range m.g.out[id] {
			to, ok := m.g.nodes[e.Target]
			if e.Relation != types.RelationDependsOn || !ok || to.Kind != types.KindTarget ||
				to.Source != project || reach[to.Label] {
				continue
			}
			reach[to.Label] = true
			queue = append(queue, e.Target)
		}
	}
	return reach
}

func needsCase(project, name string) types.Case {
	return types.Case{Node: targetID(project, name), Source: project}
}

func compareNeedsPairs(a, b needsPair) int {
	return cmp.Or(cmp.Compare(a.from, b.from), cmp.Compare(a.to, b.to))
}
