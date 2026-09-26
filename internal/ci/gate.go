// Gate redundancy: whether a ci gate run would re-verify what a recorded green
// gate already verified, so the CLI can defer it rather than run a duplicate.
//
// The decision has two inputs, each computed by the caller: whether an
// identical-or-equivalent gate already passed for this branch (Redundant), and
// whether the caller asked to skip the check (Forced). A redundant gate is
// REFUSED with exit 75, the same fast-refusal shape a contended workspace lock
// or machine budget uses: magus never proceeds by queuing, only by refusing or
// running. A gate that is not redundant always runs, silently. The machine pool
// is probed for the refusal message and decides nothing; see DecideGate.
//
// "Equivalent" means the change since the green gate's commit tiers trivial
// (internal/risk): prose nothing in the gate reads, and generated output whose
// generator the change left untouched. A merge commit in the range is never equivalent,
// however clean: a merge is the moment two verified histories combine into a
// tree neither gate ever saw, so it always re-gates.
//
// A deferral is never a success: the command either fully runs, or refuses
// with exit 75. There is no silent-skip-exit-0 state, and every refusal and
// advisory prints the full decision (the green gate matched, every changed
// path with its class and the declaration that classified it, and the pool
// state), so a reader can reconstruct and dispute it from the message alone.

package ci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// GateDecision is what the gate does about a redundancy finding.
type GateDecision int

const (
	// GateRun executes the gate with nothing printed.
	GateRun GateDecision = iota
	// GateAdvise prints the finding and executes the gate anyway.
	GateAdvise
	// GateRefuse does not execute; the refusal names the green gate and exits 75.
	GateRefuse
)

// GateFacts are what DecideGate combines. Zero value decides GateRun.
type GateFacts struct {
	// Redundant: a green gate is on record for this branch with an identical
	// input fingerprint, or with a delta that tiers trivial.
	Redundant bool
	// Forced: the caller passed the override flag; the check is off.
	Forced bool
	// Nested: this magus runs under another one. It never refuses, because the
	// pool it reads counts its own ancestors' claims as load.
	Nested bool
}

// DecideGate is the decision matrix, pure so it is testable without a store, a
// server, or a repository.
//
// Redundancy alone refuses; machine load is deliberately not an input. Gating the
// refusal on load too made it unreachable, because the load reading comes from the
// server and ordinary commands run without a persistent one.
func DecideGate(f GateFacts) GateDecision {
	if f.Forced || !f.Redundant {
		return GateRun
	}
	if f.Nested {
		return GateAdvise
	}
	return GateRefuse
}

// PoolSaturated reports whether a new run would be refused by the machine
// budget: an axis with a limit is fully held. A nil snapshot is an absent
// arbiter and reads as idle (fail open).
func PoolSaturated(m *types.MachineSnapshot) bool {
	if m == nil {
		return false
	}
	if m.BudgetSlots > 0 && m.HeldSlots >= m.BudgetSlots {
		return true
	}
	return m.BudgetMB > 0 && m.HeldMB >= m.BudgetMB
}

// GateStep is one (project, target) step's live cache key, as
// Magus.ComputeTargetKey mints it.
type GateStep struct {
	Project string
	Target  string
	Key     string
}

// GateFingerprint condenses a gate's per-step cache keys into one identity.
// Derived from the step-key machinery rather than a fresh tree hash, so it
// inherits everything a real run keys on: sources, spell claims, tool
// versions, the env allowlist, and the charm set. Order-insensitive; an empty
// selection fingerprints to "".
func GateFingerprint(steps []GateStep) string {
	if len(steps) == 0 {
		return ""
	}
	lines := make([]string, len(steps))
	for i, s := range steps {
		lines[i] = s.Project + "\x00" + s.Target + "\x00" + s.Key
	}
	slices.Sort(lines)
	h := sha256.New()
	for _, l := range lines {
		_, _ = h.Write([]byte(l))
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// MergeFreeRange reports whether walking history (newest first) from its head
// down to green crosses no merge commit. green at the head is a trivially
// merge-free range. A green commit the walk never reaches reports false: a
// range that cannot be bounded cannot be vouched for.
func MergeFreeRange(history []types.Commit, green string) bool {
	for _, c := range history {
		if c.ID == green {
			return true
		}
		if len(c.Parents) > 1 {
			return false
		}
	}
	return false
}

// InheritOff reports whether any project declared gate_inherit false, the
// workspace's off-switch for CI verdict inheritance. One declaration turns it
// off workspace-wide, the same reach a gate_low_risk declaration has.
//
// A project that DROPPED an option counts as off too. Load tolerates a key it does not
// recognize so a magusfile from the future cannot deadlock the binary that would build
// its successor, but several options are opt-outs whose absence is the permissive answer:
// an ignored `gate_inherit = false` reads as inheritance on, and an ignored
// `gate_low_risk = []` restores the prose globs the author deleted. The binary cannot
// tell which kind it dropped, because not knowing the key is the whole premise. So the
// one decision that can SKIP CI declines whenever the magusfile was not fully understood.
func InheritOff(projects []*types.Project) bool {
	return slices.ContainsFunc(projects, func(p *types.Project) bool {
		return p.GateInheritOff || len(p.IgnoredOptions) > 0
	})
}

// PRMergeFreeRange is MergeFreeRange with the head commit exempt from the
// merge rule. A CI provider tests a pull request as a synthetic merge of the
// branch into its base (GitHub's refs/pull/N/merge), so under a PR checkout
// the head is ALWAYS a merge, and it is the harness's rather than the
// delta's: the classification diffs the tree it produced against green, so
// everything it folded in is accounted for. A merge anyone pushed sits below
// the synthetic head and still refuses.
func PRMergeFreeRange(history []types.Commit, green string) bool {
	if len(history) == 0 {
		return false
	}
	if history[0].ID == green {
		return true
	}
	return MergeFreeRange(history[1:], green)
}

// InheritFinding is a fired verdict-inheritance decision: the green run being
// inherited, its head commit, and the tiered delta a reader disputes it by.
type InheritFinding struct {
	Run    string
	Commit string
	Report types.RiskReport
}

// InheritProbe gathers the CI verdict-inheritance inputs. Dependencies are
// functions, so the decision is testable with a stubbed provider and no
// repository.
type InheritProbe struct {
	// Disabled: the workspace declared gate_inherit false; nothing is probed.
	Disabled bool
	// LastGreenRun asks the CI provider for the branch/PR's newest green run
	// of this same pipeline. ok=false is the ordinary no-answer case.
	LastGreenRun func(ctx context.Context) (run, commit string, ok bool)
	// History lists commits from HEAD, newest first, deep enough to reach a
	// green run worth inheriting.
	History func(ctx context.Context) ([]types.Commit, error)
	// Assess tiers the change between the working tree and the green commit.
	Assess func(ctx context.Context, green string) (types.RiskReport, error)
}

// Evaluate decides. ok=false means the plan proceeds exactly as it would
// have before this feature existed, with no output at all; ok=true carries
// the full finding, because an inherited verdict is never a silent skip.
func (p InheritProbe) Evaluate(ctx context.Context) (InheritFinding, bool) {
	if p.Disabled || p.LastGreenRun == nil || p.History == nil || p.Assess == nil {
		return InheritFinding{}, false
	}
	run, commit, ok := p.LastGreenRun(ctx)
	if !ok || commit == "" {
		return InheritFinding{}, false
	}
	history, err := p.History(ctx)
	if err != nil || !PRMergeFreeRange(history, commit) {
		return InheritFinding{}, false
	}
	rep, err := p.Assess(ctx, commit)
	if err != nil || rep.Tier != types.RiskTrivial {
		return InheritFinding{}, false
	}
	return InheritFinding{Run: run, Commit: commit, Report: rep}, true
}

// AnnotationText renders the finding for a CI annotation: the inherited run,
// its commit, and every changed path with its tier and class, so the
// annotation alone lets a reader reconstruct and dispute the decision.
func (f InheritFinding) AnnotationText() string {
	var b strings.Builder
	b.WriteString("verdict inherited from run " + f.Run + " (commit " + shortRev(f.Commit) +
		"): the change since that green run tiers trivial")
	if len(f.Report.Evidence) == 0 {
		b.WriteString("\nno paths changed since that run")
		return b.String()
	}
	for _, line := range f.Report.Lines() {
		b.WriteString("\n" + line)
	}
	return b.String()
}

// SummaryMarkdown renders the finding for the workflow's job summary, under
// the same explicitness contract: every file, its tier and class, and what
// decided it, never a count.
func (f InheritFinding) SummaryMarkdown() string {
	var b strings.Builder
	b.WriteString("### Inherited verdict\n\n")
	b.WriteString("The shard fan-out was short-circuited: the change since this " +
		"branch's last green CI run tiers trivial, so that run's verdict stands.\n\n")
	b.WriteString("Inherited run: " + f.Run + " at commit `" + shortRev(f.Commit) + "`.\n\n")
	if len(f.Report.Evidence) == 0 {
		b.WriteString("No paths changed since that run.\n")
		return b.String()
	}
	b.WriteString("| Changed path | Tier | Class | Decided by |\n| --- | --- | --- | --- |\n")
	for _, e := range f.Report.Evidence {
		b.WriteString("| `" + e.Path + "` | " + string(e.Tier) + " | " + e.Class + " | " + e.Why + " |\n")
	}
	b.WriteString("\nTo dispute a row, its last column names the declaration or mechanism " +
		"that decided it; to turn inheritance off, declare `gate_inherit = false` in magus.project.\n")
	return b.String()
}

// shortRev abbreviates a commit id for the inheritance report.
func shortRev(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
