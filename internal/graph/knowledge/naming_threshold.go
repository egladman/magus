package knowledge

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// checkThreshold is the Check.Name of thresholdCheck's findings.
const checkThreshold = "conform-threshold"

// Scores of thresholdCheck's two findings: a subject past its value is a measurement, and a
// subject that passes only because the change moved the ratchet is the sharper fact.
const (
	thresholdPastScore  = 0.6
	thresholdMovedScore = 0.7
)

// thresholdCheck reports a symbol the change adds, renames or re-signs that sits past the value
// [types.ConformOptions.Effective] enforces for one of the symbol-level conform metrics, and one
// that sits past the value only the base's ratchet enforced: the change raised the ceiling or
// listed the symbol as known. It judges against the committed value or the anchor and never a
// live percentile, and it is silent in a workspace that names no ratchet.
type thresholdCheck struct{}

func (thresholdCheck) name() string { return checkThreshold }

func (thresholdCheck) run(x *namingIndex) []finding {
	o := x.change.Conform
	if o.RatchetPath == "" || len(x.subjects) == 0 {
		return nil
	}
	byItem := map[string]*namingDecl{}
	for _, s := range x.subjects {
		if id, ok := thresholdItemID(s); ok {
			byItem[id] = s
		}
	}
	head := x.g.Conform(o)
	past := thresholdPast(head, byItem)
	var out []finding
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
	for _, p := range thresholdPast(x.g.Conform(baseOpts), byItem) {
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

// thresholdHit is one subject found past the value its row enforces.
type thresholdHit struct {
	subject *namingDecl
	key     types.ConformKey
	measure int
	value   int
	source  types.ConformSource
}

// thresholdPast finds the subjects among the violations of out. A clone group is one violation
// naming its members, so a subject in a group is past the clone-tokens value.
func thresholdPast(out types.ConformOutput, byItem map[string]*namingDecl) []thresholdHit {
	var hits []thresholdHit
	for _, row := range out.Rows {
		for _, v := range row.Violations {
			ids := []string{v.ID}
			if row.Key.Metric == types.ConformCloneTokens {
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

// thresholdItemID names a subject the way a conform item does, "vcs/hg.go#hgVCS.Bisect".
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

func thresholdName(k types.ConformKey) string {
	return string(k.Metric) + " " + string(k.Population)
}

func thresholdSource(s types.ConformSource) string {
	if s == types.ConformFromRatchet {
		return "committed value"
	}
	return "configured anchor"
}
