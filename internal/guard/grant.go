package guard

import (
	"context"
	"fmt"
	"log/slog"
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
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/codec"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
)

// GrantableRules are the rules `magus job grant` may lower for one job, sorted. Each is a
// deny whose remedy a person may waive for a stretch of work; a rule that guards a boundary
// someone else drew never is.
func GrantableRules() []string {
	return []string{string(denyRuleHooksOff)}
}

func callerRules(ctx context.Context, deps Dependencies, at location, standing leaseStanding, adviseUnjudged bool) (Dependencies, map[string]jobtypes.Grant) {
	deps.worker = standing.declared && standing.state.Live() && workerRole(standing.row, deps.caller) != ""
	deps, granted := grantedRules(ctx, deps, at, standing)
	if adviseUnjudged {
		name := string(denyRuleUnjudgedStep)
		setting := deps.Builtins[name]
		if setting.Decision != builtin.Off {
			setting.Decision = builtin.Advise
			deps.Builtins = maps.Clone(deps.Builtins)
			if deps.Builtins == nil {
				deps.Builtins = make(map[string]builtin.Setting)
			}
			deps.Builtins[name] = setting
		}
	}
	return deps, granted
}

// grantedRules lowers to advise, for this call alone, each grantable rule a live grant names
// for the acting lease, and returns the grants that did. A grants file that cannot be read
// lowers nothing, so every rule keeps refusing.
func grantedRules(ctx context.Context, deps Dependencies, at location, standing leaseStanding) (Dependencies, map[string]jobtypes.Grant) {
	if !standing.declared || !standing.state.Live() {
		return deps, nil
	}
	store := job.NewStore(at.jobs())
	grants, err := store.Grants(ctx)
	if err != nil {
		return deps, nil
	}
	tries, err := store.Attempts(ctx)
	if err != nil {
		return deps, nil
	}
	attempt := jobtypes.NewAttempts(tries).LiveN(standing.row.ID)
	now := time.Now().Unix()
	var granted map[string]jobtypes.Grant
	for _, g := range grants {
		if !slices.Contains(GrantableRules(), g.Rule) || !g.Lowers(standing.row, attempt, now) {
			continue
		}
		if granted == nil {
			granted = map[string]jobtypes.Grant{}
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
func grantNote(g jobtypes.Grant) string {
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

// gateWaiversFileName sits beside the job store, so every checkout of the repository reads
// one set of waivers.
const gateWaiversFileName = "gate-waivers.jsonl"

func gateWaivers(store *job.Store) (file.JSONL[gateWaiver], error) {
	jobs, err := store.Path()
	if err != nil {
		return file.JSONL[gateWaiver]{}, err
	}
	return file.NewJSONL(codec.JSONLConfig[gateWaiver]{
		Log:  slog.With(attr.Component("magus")),
		Path: filepath.Join(filepath.Dir(jobs), gateWaiversFileName),
		Perm: 0o644,
		Key:  gateWaiver.Key,
	})
}

// waiveGate waives the push gate for commit, a full revision id, and no other
// commit. A later waiver of it replaces this one. It refuses an empty reason
// and a process carrying a lease.
func waiveGate(ctx context.Context, store *job.Store, commit, reason string) (gateWaiver, error) {
	if strings.TrimSpace(reason) == "" {
		return gateWaiver{}, fmt.Errorf("guard: a waiver says why the gate was skipped: pass --reason")
	}
	return appendGateWaiver(ctx, store, gateWaiver{Commit: commit, Reason: reason})
}

// revokeGateWaiver ends commit's waiver, recording who ended it. Revoking a waiver that is
// not there still writes the line.
func revokeGateWaiver(ctx context.Context, store *job.Store, commit string) (gateWaiver, error) {
	return appendGateWaiver(ctx, store, gateWaiver{Commit: commit, Revoked: true})
}

func appendGateWaiver(ctx context.Context, store *job.Store, w gateWaiver) (gateWaiver, error) {
	if actor := store.Actor(); actor.Bound() {
		return gateWaiver{}, fmt.Errorf("guard: a waiver is a person's keystroke, and this process acts under lease %q: a person types `%s job grant --commit` at their own terminal", actor.Lease, hint.BinaryName())
	}
	log, err := gateWaivers(store)
	if err != nil {
		return gateWaiver{}, err
	}
	w.By, w.At = recorder.StampOrigin(ctx, jobtypes.Origin{}), time.Now().Unix()
	return w, log.Update(ctx, func([]gateWaiver) []gateWaiver { return []gateWaiver{w} })
}

// gateWaiverAt is the waiver covering commit in the workspace at, if one does. A waivers
// file that cannot be read covers nothing, so the push gate keeps refusing.
func gateWaiverAt(at location, commit string) (gateWaiver, bool) {
	log, err := gateWaivers(job.NewStore(at.jobs()))
	if err != nil {
		return gateWaiver{}, false
	}
	waivers, err := log.Load()
	if err != nil {
		return gateWaiver{}, false
	}
	i := slices.IndexFunc(waivers, func(w gateWaiver) bool { return w.Covers(commit) })
	if i < 0 {
		return gateWaiver{}, false
	}
	return waivers[i], true
}

// waiverNote names the waiver that let a push of commit through: who typed it, when, and why.
func waiverNote(commit string, w gateWaiver) string {
	return fmt.Sprintf("magus workspace: %s goes out ungated under the waiver %s typed at %s: %s",
		commit, w.By.Label(), time.Unix(w.At, 0).UTC().Format(time.RFC3339), w.Reason)
}

// minWaiverRevision is the shortest revision a waiver matches against: shorter, an
// abbreviation could name more than one commit.
const minWaiverRevision = 7

// gateWaiver is one line of the gate waivers file. It lets one commit go out with no passing
// local gate, or revokes that. The last line per Commit wins.
type gateWaiver struct {
	// Commit is the full revision id the waiver covers, and no other.
	Commit string `json:"commit" yaml:"commit"`
	// By is who typed the waiver, as the trail records any origin.
	By jobtypes.Origin `json:"by" yaml:"by"`
	// At is the unix second the line was written.
	At int64 `json:"at" yaml:"at"`
	// Reason is why the person skipped the gate, in their words.
	Reason string `json:"reason,omitempty" yaml:"reason,omitempty"`
	// Revoked ends the waiver the line keys.
	Revoked bool `json:"revoked,omitzero" yaml:"revoked,omitempty"`
}

// Key names the waiver a line writes: its commit.
func (w gateWaiver) Key() string { return w.Commit }

// Covers reports whether w lets commit out ungated. commit may be the full id or an
// abbreviation of it at least seven characters long; a revoked waiver covers nothing.
func (w gateWaiver) Covers(commit string) bool {
	if w.Revoked || len(commit) < minWaiverRevision || len(w.Commit) < minWaiverRevision {
		return false
	}
	return strings.HasPrefix(w.Commit, commit) || strings.HasPrefix(commit, w.Commit)
}
