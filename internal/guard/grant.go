package guard

import (
	"context"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/trail"
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
		if !slices.Contains(GrantableRules(), g.Rule) || !g.Lowers(standing.row, now) {
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
		until = fmt.Sprintf("until %s or the job ends", time.Unix(g.Until, 0).UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("magus workspace: not refused, under the grant %s typed at %s for job %s's %s, %s.",
		g.By.Label(), time.Unix(g.At, 0).UTC().Format(time.RFC3339), g.Job, g.Rule, until)
}

// denyAgentGrant refuses `magus job grant` in any spelling. Every call the hook judges is an
// agent's, the root's included, and a grant is a person's approval, so an agent's grant is
// never one.
func denyAgentGrant(command string, d Dialect) denial {
	return denyPersonsVerb(command, d, "job", "grant",
		"A grant lowers a guard rule for one job on a person's say-so. The guard judges only agents' calls, so a grant reaching it was not a person's, and the job store refuses one from a process carrying a lease as well.")
}

// denyPersonsVerb refuses `magus <noun> <verb>` in any spelling with why: a verb only a
// person runs.
func denyPersonsVerb(command string, d Dialect, noun, verb, why string) denial {
	cmds, ok := ParseCommandsDialect(command, d)
	if !ok {
		return denial{}
	}
	for _, c := range cmds {
		if path.Base(c.Name) != "magus" {
			continue
		}
		if words := magusSubcommandWords(c.Args); len(words) >= 2 && words[0] == noun && words[1] == verb {
			return denial{
				Say: fmt.Sprintf("magus workspace: only a person runs `%s %s %s`; ask the person to type it.", hint.BinaryName(), noun, verb),
				Why: why,
			}
		}
	}
	return denial{}
}

// denyRuleWaiverIsAPersons refuses `magus gate waive` to an agent, as grant-is-a-persons
// refuses `magus job grant`.
const denyRuleWaiverIsAPersons denyRuleName = "waiver-is-a-persons"

// gateWaiversFileName sits beside the job store, so every checkout of the repository reads
// one set of waivers.
const gateWaiversFileName = "gate-waivers.jsonl"

func gateWaivers(store *job.Store) (file.JSONL[types.GateWaiver], error) {
	jobs, err := store.Path()
	if err != nil {
		return file.JSONL[types.GateWaiver]{}, err
	}
	return file.JSONL[types.GateWaiver]{
		Path: filepath.Join(filepath.Dir(jobs), gateWaiversFileName),
		Key:  types.GateWaiver.Key,
	}, nil
}

// WaiveGate records a person's waiver of the push gate for commit, a full revision id: a
// push or `gh pr create` publishing that commit passes push-ungated, and no other commit
// does. A later waiver of the same commit replaces it. It refuses an empty reason and a
// process carrying a lease.
func WaiveGate(ctx context.Context, store *job.Store, commit, reason string) (types.GateWaiver, error) {
	if strings.TrimSpace(reason) == "" {
		return types.GateWaiver{}, fmt.Errorf("guard: a waiver says why the gate was skipped: pass --reason")
	}
	return appendGateWaiver(ctx, store, types.GateWaiver{Commit: commit, Reason: reason})
}

// RevokeGateWaiver ends commit's waiver, recording who ended it. Revoking a waiver that is
// not there still writes the line.
func RevokeGateWaiver(ctx context.Context, store *job.Store, commit string) (types.GateWaiver, error) {
	return appendGateWaiver(ctx, store, types.GateWaiver{Commit: commit, Revoked: true})
}

func appendGateWaiver(ctx context.Context, store *job.Store, w types.GateWaiver) (types.GateWaiver, error) {
	if actor := store.Actor(); actor.Bound() {
		return types.GateWaiver{}, fmt.Errorf("guard: a waiver is a person's keystroke, and this process acts under lease %q: a person types `%s gate waive` at their own terminal", actor.Lease, hint.BinaryName())
	}
	log, err := gateWaivers(store)
	if err != nil {
		return types.GateWaiver{}, err
	}
	w.By, w.At = trail.StampOrigin(ctx, types.Origin{}), time.Now().Unix()
	return w, log.Update(ctx, func([]types.GateWaiver) []types.GateWaiver { return []types.GateWaiver{w} })
}

// gateWaiverAt is the waiver covering commit in the workspace at, if one does. A waivers
// file that cannot be read covers nothing, so the push gate keeps refusing.
func gateWaiverAt(at location, commit string) (types.GateWaiver, bool) {
	log, err := gateWaivers(job.NewStore(at.jobs()))
	if err != nil {
		return types.GateWaiver{}, false
	}
	waivers, err := log.Load()
	if err != nil {
		return types.GateWaiver{}, false
	}
	i := slices.IndexFunc(waivers, func(w types.GateWaiver) bool { return w.Covers(commit) })
	if i < 0 {
		return types.GateWaiver{}, false
	}
	return waivers[i], true
}

// waiverNote names the waiver that let a push of commit through: who typed it, when, and why.
func waiverNote(commit string, w types.GateWaiver) string {
	return fmt.Sprintf("magus workspace: %s goes out ungated under the waiver %s typed at %s: %s",
		commit, w.By.Label(), time.Unix(w.At, 0).UTC().Format(time.RFC3339), w.Reason)
}

// denyAgentWaive refuses `magus gate waive` in any spelling, for the reason denyAgentGrant
// refuses a grant: a waiver is a person's approval, and every call the hook judges is an
// agent's.
func denyAgentWaive(command string, d Dialect) denial {
	return denyPersonsVerb(command, d, "gate", "waive",
		"A waiver lets one commit out past push-ungated on a person's say-so. The guard judges only agents' calls, so a waiver reaching it was not a person's, and the waivers store refuses one from a process carrying a lease as well.")
}
