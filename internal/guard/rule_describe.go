package guard

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/types"
)

// appliedDecision is the decision the working tree gives r: its workspace setting's, or
// the compiled one when it sets none.
func appliedDecision(r types.RuleDoc) string {
	if r.Workspace == "" {
		return r.Decision
	}
	decision, _, _ := strings.Cut(r.Workspace, ",")
	return decision
}

// RuleNames is the catalog's names, for the near-miss suggestion.
func RuleNames() []string {
	rules := Rules()
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Name)
	}
	return out
}

// WriteRuleTable renders the catalog as a table, denies first, and the one-line tally
// beneath it.
func WriteRuleTable(w io.Writer, rules []types.RuleDoc) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "DEFAULT\tWORKSPACE\tRULE\tCATCHES")
	counts := map[string]int{}
	for _, r := range rules {
		counts[appliedDecision(r)]++
		workspace := r.Workspace
		if workspace == "" {
			workspace = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Decision, workspace, r.Name, r.Catches)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\n%d refuse, %d explain, %d off. One rule: `%s`\n",
		counts["deny"], counts["advise"], counts["off"],
		hint.DescribeRule.With("<rule>"))
	return err
}

// WriteRuleDetail renders one rule.
//
// Deliberately short. The long rationale a verdict used to carry belongs in the rule's
// docs page, not here: this is the answer to "what is this thing", asked by someone who
// just read the slug off a refusal.
func WriteRuleDetail(w io.Writer, doc types.RuleDoc) {
	if doc.Workspace == "" {
		fmt.Fprintf(w, "%s (%s)\n\n", doc.Name, doc.Decision)
	} else {
		fmt.Fprintf(w, "%s (%s; default %s)\n\n", doc.Name, doc.Workspace, doc.Decision)
	}
	fmt.Fprintln(w, "catches")
	fmt.Fprintln(w, "  "+doc.Catches)
	fmt.Fprintln(w)
	if doc.Why != "" {
		fmt.Fprintln(w, "why")
		termkit.Prose(w, termkit.SystemProbe, doc.Why)
		fmt.Fprintln(w)
	}
	switch appliedDecision(doc) {
	case "off":
		fmt.Fprintln(w, "This workspace turns the rule off, so it says nothing.")
		return
	case "deny":
		fmt.Fprintln(w, "A deny blocks the call and names what to run instead. Nothing magus")
		fmt.Fprintln(w, "refuses is a capability it removes: every one has a covered equivalent.")
		return
	}
	fmt.Fprintln(w, "An advisory blocks nothing. It attaches context and is held to one")
	fmt.Fprintln(w, "firing per session, so a repeat says only the short form.")
}
