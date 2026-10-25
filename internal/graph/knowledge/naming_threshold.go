package knowledge

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// checkThreshold is the Check.Name of thresholdCheck's findings.
const checkThreshold = "conformance-threshold"

// Scores of thresholdCheck's three findings: a subject past its value is a measurement, a known
// item the change made longer is a growth between the two, and a subject that passes only because
// the change moved the ratchet is the sharper fact.
const (
	thresholdPastScore  = 0.6
	thresholdGrewScore  = 0.65
	thresholdMovedScore = 0.7
)

// thresholdCheck reports a symbol the change adds, renames or re-signs that sits past the value
// [types.ConformanceOptions.Effective] enforces for one of the symbol-level conformance metrics, and one
// that sits past the value only the base's ratchet enforced: the change raised the ceiling or
// listed the symbol as known. It also reports a known item the change touches that measures more
// than at the base, since the ratchet matches a known item by ID and never compares its measure.
// It judges against the committed value or the anchor and never a live percentile, and it is
// silent in a workspace that names no ratchet.
type thresholdCheck struct{}

func (thresholdCheck) name() string { return checkThreshold }

func (thresholdCheck) run(x *namingIndex) []finding {
	o := x.change.Conformance
	if o.RatchetPath == "" || len(x.subjects)+len(x.change.Touched) == 0 {
		return nil
	}
	byItem := map[string]*namingDecl{}
	for _, s := range x.subjects {
		if id, ok := thresholdItemID(s); ok {
			byItem[id] = s
		}
	}
	head := x.g.Conformance(o)
	past := thresholdPast(head, byItem)
	out := thresholdGrew(x, o)
	for _, p := range past {
		out = append(out, finding{
			subject: p.subject.id,
			score:   thresholdPastScore,
			message: fmt.Sprintf("`%s` measures %d for %s, past the %s of %d",
				p.subject.label, p.measure, thresholdName(p.key), thresholdSource(p.source), p.value),
		})
	}
	if x.change.RatchetBase == nil {
		return out
	}
	baseOpts := o
	baseOpts.Ratchet = *x.change.RatchetBase
	for _, p := range thresholdPast(x.g.Conformance(baseOpts), byItem) {
		if slices.ContainsFunc(past, func(h thresholdHit) bool { return h.subject == p.subject && h.key == p.key }) {
			continue
		}
		now, _ := o.Effective(p.key)
		how := "lists it as known"
		if now > p.value {
			how = fmt.Sprintf("raises the ceiling from %d to %d", p.value, now)
		}
		out = append(out, finding{
			subject: p.subject.id,
			score:   thresholdMovedScore,
			message: fmt.Sprintf("`%s` measures %d for %s, past the %d the base enforced; this change %s",
				p.subject.label, p.measure, thresholdName(p.key), p.value, how),
		})
	}
	return out
}

// thresholdGrew reports each known item among the change's subjects and touched symbols that measures
// more than in the base graph. A known item never appears in a row's violations, so this reads
// the samples itself; a clone group is judged whole elsewhere and is skipped.
func thresholdGrew(x *namingIndex, o types.ConformanceOptions) []finding {
	if x.change.Base == nil || len(o.Ratchet.Known) == 0 {
		return nil
	}
	known := conformanceKnownItems(o.Ratchet.Known)
	byItem := thresholdKnownSubjects(x, known)
	if len(byItem) == 0 {
		return nil
	}
	head, base := conformanceSamples(x.g, o), conformanceSamples(x.change.Base, o)
	var out []finding
	for _, key := range slices.SortedFunc(maps.Keys(head), compareConformanceKeys) {
		if _, owned := ownCheck[key.Metric]; owned || key == cloneKey {
			continue
		}
		value, source := o.Effective(key)
		if source == "" {
			continue
		}
		before := map[string]int{}
		for _, it := range base[key] {
			before[it.ID] = it.Measure
		}
		for _, it := range head[key] {
			d, ok := byItem[it.ID]
			if !ok || it.Measure <= value || !known[key.Metric][it.ID] {
				continue
			}
			if was, inBase := before[it.ID]; inBase && was < it.Measure {
				out = append(out, finding{
					subject: d.id,
					score:   thresholdGrewScore,
					message: fmt.Sprintf("`%s` measures %d for %s, up from %d at the base, past the %s of %d",
						d.label, it.Measure, thresholdName(key), was, thresholdSource(source), value),
				})
			}
		}
	}
	return out
}

// thresholdKnownSubjects maps the item ID of each subject and touched symbol of the change that
// is a known item of any metric to its declaration.
func thresholdKnownSubjects(x *namingIndex, known map[types.ConformanceMetric]map[string]bool) map[string]*namingDecl {
	isKnown := func(id string) bool {
		for _, ids := range known {
			if ids[id] {
				return true
			}
		}
		return false
	}
	byItem := map[string]*namingDecl{}
	add := func(d *namingDecl) {
		if id, ok := thresholdItemID(d); ok && isKnown(id) {
			byItem[id] = d
		}
	}
	for _, s := range x.subjects {
		add(s)
	}
	for _, id := range x.change.Touched {
		if d, ok := x.byID[id]; ok {
			add(d)
		}
	}
	return byItem
}

// thresholdHit is one subject found past the value its row enforces.
type thresholdHit struct {
	subject *namingDecl
	key     types.ConformanceKey
	measure int
	value   int
	source  types.ConformanceSource
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
			ids := []string{v.ID}
			if row.Key.Metric == types.ConformanceCloneTokens {
				ids = strings.Split(v.ID, ", ")
			}
			for _, id := range ids {
				if s, ok := byItem[id]; ok {
					hits = append(hits, thresholdHit{subject: s, key: row.Key, measure: v.Measure, value: row.Value, source: row.Source})
				}
			}
		}
	}
	return hits
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

func thresholdSource(s types.ConformanceSource) string {
	if s == types.ConformanceFromRatchet {
		return "committed value"
	}
	return "configured anchor"
}
