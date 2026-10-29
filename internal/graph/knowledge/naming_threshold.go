package knowledge

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/types"
)

// checkThreshold is the Check.Name of thresholdCheck's findings.
const checkThreshold = "conformance-threshold"

// Scores of thresholdCheck's two findings: a subject past its value is a measurement, and one
// the change made measure more than at the base is the sharper fact.
const (
	thresholdPastScore = 0.6
	thresholdGrewScore = 0.65
)

// thresholdCheck reports a symbol the change adds, renames or re-signs, or with a base graph
// touches, that sits past the anchor [types.ConformanceOptions.Effective] enforces. An item the
// base already holds past by as much is the base's, not the change's: the conformance ceiling,
// judged against the base revision rather than a record of counts.
type thresholdCheck struct{}

func (thresholdCheck) name() string { return checkThreshold }

func (thresholdCheck) run(x *namingIndex) []finding {
	o := x.change.Conformance
	byItem := map[string]*namingDecl{}
	for _, s := range x.subjects {
		if id, ok := thresholdItemID(s); ok {
			byItem[id] = s
		}
	}
	if x.change.Base != nil {
		for _, id := range x.change.Touched {
			if d, ok := x.byID[id]; ok {
				if item, ok := thresholdItemID(d); ok {
					byItem[item] = d
				}
			}
		}
	}
	if len(byItem) == 0 {
		return nil
	}
	var before map[types.ConformanceKey]map[string]int
	if x.change.Base != nil {
		before = thresholdMeasures(x.change.Base.Conformance(o))
	}
	var out []finding
	for _, p := range thresholdPast(x.g.Conformance(o), byItem) {
		was, inBase := before[p.key][p.item]
		switch {
		case inBase && was >= p.measure:
			continue
		case inBase:
			out = append(out, finding{
				subject: p.subject.id,
				score:   thresholdGrewScore,
				message: fmt.Sprintf("`%s` measures %d for %s, up from %d at the base, past the configured anchor of %d",
					p.subject.label, p.measure, thresholdName(p.key), was, p.value),
			})
		default:
			out = append(out, finding{
				subject: p.subject.id,
				score:   thresholdPastScore,
				message: fmt.Sprintf("`%s` measures %d for %s, past the configured anchor of %d",
					p.subject.label, p.measure, thresholdName(p.key), p.value),
			})
		}
	}
	return out
}

// thresholdHit is one subject found past the value its row enforces, under its item ID.
type thresholdHit struct {
	subject *namingDecl
	item    string
	key     types.ConformanceKey
	measure int
	value   int
}

// ownCheck holds the metrics another check reports with its own evidence, so a subject past
// one is named once. The coupling metrics are conjuncts of shared-loop: past any one alone is
// what a codebase keeps on purpose.
var ownCheck = map[types.ConformanceMetric]string{
	types.ConformanceCallSequence: types.CheckCallSequence,
	types.ConformanceSharedBody:   types.CheckSharedLoop,
	types.ConformanceSharedReach:  types.CheckSharedLoop,
	types.ConformanceCallerSpread: types.CheckSharedLoop,
	types.ConformanceModeKnobs:    types.CheckSharedLoop,
	types.ConformanceAliasShare:   types.CheckImportAlias,
	types.ConformanceNameClash:    types.CheckImportAlias,
}

// thresholdPast finds the subjects among the violations of out, past a metric no other check
// owns. A clone group is one violation naming its members, so a subject in a group is past the
// clone-tokens value.
func thresholdPast(out types.ConformanceOutput, byItem map[string]*namingDecl) []thresholdHit {
	var hits []thresholdHit
	for _, row := range out.Rows {
		if _, owned := ownCheck[row.Key.Metric]; owned {
			continue
		}
		for _, v := range row.Violations {
			for _, id := range thresholdMembers(row.Key, v) {
				if s, ok := byItem[id]; ok {
					hits = append(hits, thresholdHit{subject: s, item: id, key: row.Key, measure: v.Measure, value: row.Value})
				}
			}
		}
	}
	return hits
}

// thresholdMeasures is the measure of every item past its value in out, per key, a clone
// group's under each member.
func thresholdMeasures(out types.ConformanceOutput) map[types.ConformanceKey]map[string]int {
	measures := map[types.ConformanceKey]map[string]int{}
	for _, row := range out.Rows {
		for _, v := range row.Violations {
			for _, id := range thresholdMembers(row.Key, v) {
				if measures[row.Key] == nil {
					measures[row.Key] = map[string]int{}
				}
				measures[row.Key][id] = v.Measure
			}
		}
	}
	return measures
}

// thresholdMembers is the item IDs one violation names: each member of a clone group, else its
// own ID.
func thresholdMembers(k types.ConformanceKey, v types.ConformanceItem) []string {
	if k.Metric == types.ConformanceCloneTokens {
		return strings.Split(v.ID, ", ")
	}
	return []string{v.ID}
}

// thresholdItemID names a subject the way a conformance item does, "vcs/hg.go#hgVCS.Bisect".
func thresholdItemID(d *namingDecl) (string, bool) {
	p, ok := parseDescriptors(d.id)
	if !ok {
		return "", false
	}
	local := afterNamespace(p)
	if len(local) == 0 || d.file() == "" {
		return "", false
	}
	return symbolItemID(d.file(), local), true
}

func thresholdName(k types.ConformanceKey) string {
	return fmt.Sprintf("%s %s", string(k.Metric), string(k.Population))
}
