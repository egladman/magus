package guard

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"time"

	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
)

// GrantableRules are the rules `magus job grant` may lower for one job, sorted. Each is a
// deny whose remedy a person may waive for a stretch of work; a rule that guards a boundary
// someone else drew never is.
func GrantableRules() []string {
	return []string{string(denyRuleHooksOff)}
}

// grantedRules lowers to advise, for this call alone, each grantable rule a live grant names
// for the acting lease, and returns the grants that did. A grants file that cannot be read
// lowers nothing, so every rule keeps refusing.
func grantedRules(deps Dependencies, at location, standing leaseStanding) (Dependencies, map[string]types.JobGrant) {
	if !standing.declared || !standing.state.Live() {
		return deps, nil
	}
	grants, err := job.NewStore(at.jobs()).Grants()
	if err != nil {
		return deps, nil
	}
	now := time.Now().Unix()
	var granted map[string]types.JobGrant
	for _, g := range grants {
		if !slices.Contains(GrantableRules(), g.Rule) || !g.LiveFor(standing.row, now) {
			continue
		}
		if granted == nil {
			granted = map[string]types.JobGrant{}
			deps.Builtins = maps.Clone(deps.Builtins)
			if deps.Builtins == nil {
				deps.Builtins = map[string]builtin.Setting{}
			}
		}
		granted[g.Rule] = g
		deps.Builtins[g.Rule] = builtin.Setting{Decision: builtin.Advise}
	}
	return deps, granted
}

// grantNote names the grant that turned a refusal into advice: who typed it, when, and
// when it ends.
func grantNote(g types.JobGrant) string {
	until := "until the job ends"
	if g.Until != 0 {
		until = "until " + time.Unix(g.Until, 0).UTC().Format(time.RFC3339) + " or the job ends"
	}
	return fmt.Sprintf("magus workspace: not refused, under the grant %s typed at %s for job %s's %s, %s.",
		g.By.Label(), time.Unix(g.At, 0).UTC().Format(time.RFC3339), g.Job, g.Rule, until)
}

// denyAgentGrant refuses `magus job grant` in any spelling. Every call the hook judges is an
// agent's, the root's included, and a grant is a person's approval, so an agent's grant is
// never one.
func denyAgentGrant(command string, d Dialect) denial {
	cmds, ok := ParseCommandsDialect(command, d)
	if !ok {
		return denial{}
	}
	for _, c := range cmds {
		if path.Base(c.Name) != "magus" {
			continue
		}
		if words := magusSubcommandWords(c.Args); len(words) >= 2 && words[0] == "job" && words[1] == "grant" {
			return denial{
				Say: "magus workspace: `" + hint.BinaryName() + " job grant` is a person's keystroke, so no agent runs it; ask the person to type it.",
				Why: "A grant lowers a guard rule for one job on a person's say-so. The guard judges only agents' calls, so a grant reaching it was not a person's, and the job store refuses one from a process carrying a lease as well.",
			}
		}
	}
	return denial{}
}
