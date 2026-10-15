package knowledge

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/types"
)

// Scores of couplingCheck's findings: a shared function past the rule is sharper when the
// change is the merge that shared it, and a function bent for one caller is the weakest.
const (
	overSharedScore           = 0.6
	overSharedIntroducedScore = 0.7
	bentScore                 = 0.5
)

// couplingCheck is the mirror of a clone: a function shared by callers that do not share its
// knobs. It reports a function the change adds, renames or re-signs that is over-shared
// (sharedUse.overShared) and listed in no mode-knobs known entry. It also reports a
// re-signed function with two or more caller packages or caller files whose body branches
// on a parameter, or that offers an option one caller alone uses: a branch only one caller
// takes is a second function under one name. The index holds no base signature, so every knob of a re-signed
// function is named, whether or not the change added it.
//
// Thresholds are read through Effective, never a constant or a live percentile. With no
// symbol index it is silent; a language whose reader renders no parameters yields no knob.
type couplingCheck struct{}

func (couplingCheck) name() string { return types.CheckSharedLoop }

func (couplingCheck) run(x *namingIndex) []finding {
	only := map[string]bool{}
	for _, s := range x.subjects {
		if s.callable() {
			only[s.id] = true
		}
	}
	if len(only) == 0 {
		return nil
	}
	o := x.change.Conform
	uses := map[string]sharedUse{}
	for _, u := range x.g.sharedUses(o, only) {
		uses[u.ID] = u
	}
	known := conformKnownItems(o.Ratchet.Known)[types.ConformModeKnobs]
	var ci *conformIndex
	var out []finding
	for _, s := range x.subjects {
		item, ok := thresholdItemID(s)
		if !only[s.id] || !ok {
			continue
		}
		u := uses[item]
		if u.overShared(o) {
			if known[item] {
				continue
			}
			score := overSharedScore
			if x.change.Introduced[s.id] {
				score = overSharedIntroducedScore
			}
			out = append(out, finding{subject: s.id, score: score, message: overSharedMessage(s.label, u), details: couplingDetails(o, u)})
			continue
		}
		if x.change.Introduced[s.id] || x.change.Renamed[s.id] != "" {
			continue
		}
		if ci == nil {
			ci = newConformIndex(x.g, o)
		}
		files := ci.callerFiles(s.id)
		knobs := u.Knobs
		if len(u.Callers) < 2 {
			knobs = modeKnobs(x.g, x.g.nodes[s.id], s.shape, nil)
		}
		if len(knobs) == 0 || len(u.Callers) < 2 && len(files) < 2 {
			continue
		}
		out = append(out, finding{
			subject: s.id,
			score:   bentScore,
			message: fmt.Sprintf("`%s` now %s, and %d files call it (%s): a branch only one caller takes is a second function under one name. Keep the shared part small and give the branch its own function, unless every caller needs the fix it carries",
				s.label, bentPhrase(knobs), len(files), listed(files)),
		})
	}
	return out
}

func overSharedMessage(label string, u sharedUse) string {
	return fmt.Sprintf("`%s` (%d lines) is shared by %d packages %s (%s), and %s. Share it only when its callers have the same failure semantics and policy knobs, so a bug in one is a bug in the other; otherwise give each caller its own copy with one line saying why, or record why it stays shared as a mode-knobs known entry",
		label, u.Body, len(u.Callers), hopsApart(u.Spread), listed(u.Callers), knobPhrase(u.Knobs))
}

// couplingDetails names every caller, then each value the rule read and where it came from.
func couplingDetails(o types.ConformOptions, u sharedUse) []string {
	details := []string{"callers: " + strings.Join(u.Callers, ", ")}
	for _, m := range []types.ConformMetric{types.ConformSharedBody, types.ConformCallerSpread, types.ConformSharedReach} {
		v, src := o.Effective(couplingKey(m))
		details = append(details, fmt.Sprintf("%s source: %d, the %s", m, v, thresholdSource(src)))
	}
	return details
}

func hopsApart(spread int) string {
	switch spread {
	case spreadUnrelated:
		return "that meet only through it"
	case 1:
		return "1 import hop apart"
	}
	return fmt.Sprintf("%d import hops apart", spread)
}

// listed names up to conformanceExamples of xs, and says when there are more.
func listed(xs []string) string {
	s := strings.Join(capList(xs, conformanceExamples), ", ")
	if len(xs) > conformanceExamples {
		s += ", ..."
	}
	return s
}

func knobPhrase(knobs []modeKnob) string {
	parts := make([]string, len(knobs))
	for i, k := range knobs {
		parts[i] = "`" + k.Name + "` is set only by " + k.Setter
		if k.Kind == knobBranch {
			parts[i] = "it branches on `" + k.Name + "`"
		}
	}
	return strings.Join(parts, ", ")
}

func bentPhrase(knobs []modeKnob) string {
	parts := make([]string, len(knobs))
	for i, k := range knobs {
		parts[i] = "offers `" + k.Name + "`, which only " + k.Setter + " uses"
		if k.Kind == knobBranch {
			parts[i] = "branches on `" + k.Name + "`"
		}
	}
	return strings.Join(parts, " and ")
}
