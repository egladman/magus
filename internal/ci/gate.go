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
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/ci/annotate"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/safetyscissors/budget"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/libs/text/plural"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/check"
	"github.com/egladman/magus/vcs"
)

// gateDecision is what the gate does about a redundancy finding.
type gateDecision int

const (
	// gateRun executes the gate with nothing printed.
	gateRun gateDecision = iota
	// gateAdvise prints the finding and executes the gate anyway.
	gateAdvise
	// gateRefuse does not execute; the refusal names the green gate and exits 75.
	gateRefuse
)

// gateFacts are what DecideGate combines. Zero value decides GateRun.
type gateFacts struct {
	// Redundant: a green gate is on record for this branch with an identical
	// input fingerprint, or with a delta that tiers trivial.
	Redundant bool
	// Forced: the caller passed the override flag; the check is off.
	Forced bool
	// Nested: this magus runs under another one. It never refuses, because the
	// pool it reads counts its own ancestors' claims as load.
	Nested bool
}

// decideGate is the decision matrix, pure so it is testable without a store, a
// server, or a repository.
//
// Redundancy alone refuses; machine load is deliberately not an input. Gating the
// refusal on load too made it unreachable, because the load reading comes from the
// server and ordinary commands run without a persistent one.
func decideGate(f gateFacts) gateDecision {
	if f.Forced || !f.Redundant {
		return gateRun
	}
	if f.Nested {
		return gateAdvise
	}
	return gateRefuse
}

// poolSaturated reports whether a new run would be refused by the machine
// budget: an axis with a limit is fully held. A nil snapshot is an absent
// arbiter and reads as idle (fail open).
func poolSaturated(m *budget.Snapshot) bool {
	if m == nil {
		return false
	}
	if m.BudgetSlots > 0 && m.HeldSlots >= m.BudgetSlots {
		return true
	}
	return m.BudgetMB > 0 && m.HeldMB >= m.BudgetMB
}

// gateStep is one (project, target) step's live cache key, as
// Magus.ComputeTargetKey mints it.
type gateStep struct {
	Project string
	Target  string
	Key     string
}

// gateFingerprint condenses a gate's per-step cache keys into one identity.
// Derived from the step-key machinery rather than a fresh tree hash, so it
// inherits everything a real run keys on: sources, spell claims, tool
// versions, the env allowlist, and the charm set. Order-insensitive; an empty
// selection fingerprints to "".
func gateFingerprint(steps []gateStep) string {
	if len(steps) == 0 {
		return ""
	}
	lines := make([]string, len(steps))
	for i, s := range steps {
		lines[i] = fmt.Sprintf("%s\x00%s\x00%s", s.Project, s.Target, s.Key)
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

// inheritOff reports whether any project declared gate_inherit false, the
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
func inheritOff(projects []*types.Project) bool {
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
	Report types.TierReport
}

// inheritProbe gathers the CI verdict-inheritance inputs. Dependencies are
// functions, so the decision is testable with a stubbed provider and no
// repository.
type inheritProbe struct {
	// Disabled: the workspace declared gate_inherit false; nothing is probed.
	Disabled bool
	// LastGreenRun asks the CI provider for the branch/PR's newest green run
	// of this same pipeline. ok=false is the ordinary no-answer case.
	LastGreenRun func(ctx context.Context) (run, commit string, ok bool)
	// History lists commits from HEAD, newest first, deep enough to reach a
	// green run worth inheriting.
	History func(ctx context.Context) ([]types.Commit, error)
	// Assess tiers the change between the working tree and the green commit.
	Assess func(ctx context.Context, green string) (types.TierReport, error)
}

// Evaluate decides. ok=false means the plan proceeds exactly as it would
// have before this feature existed, with no output at all; ok=true carries
// the full finding, because an inherited verdict is never a silent skip.
func (p inheritProbe) Evaluate(ctx context.Context) (InheritFinding, bool) {
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
	if err != nil || rep.Tier != check.RiskTrivial {
		return InheritFinding{}, false
	}
	return InheritFinding{Run: run, Commit: commit, Report: rep}, true
}

// AnnotationText renders the finding for a CI annotation: the inherited run,
// its commit, and every changed path with its tier and class, so the
// annotation alone lets a reader reconstruct and dispute the decision.
func (f InheritFinding) AnnotationText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "verdict inherited from run %s (commit %s): the change since that green run tiers trivial", f.Run, shortRev(f.Commit))
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
	fmt.Fprintf(&b, "Inherited run: %s at commit `%s`.\n\n", f.Run, shortRev(f.Commit))
	if len(f.Report.Evidence) == 0 {
		b.WriteString("No paths changed since that run.\n")
		return b.String()
	}
	b.WriteString("| Changed path | Tier | Class | Decided by |\n| --- | --- | --- | --- |\n")
	for _, e := range f.Report.Evidence {
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", e.Path, string(e.Tier), e.Class, e.Why)
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

// GateMergeScanLimit bounds the history walk that looks for a merge commit
// between the green gate and HEAD. A green gate older than this many commits
// re-gates.
const GateMergeScanLimit = 200

// GateWorkspace is the part of a workspace the gate reads: its root and VCS options, the
// per-step cache keys the fingerprint is minted from, the affected set the scope is
// measured against, and the risk assessment that tiers a change.
type GateWorkspace interface {
	types.RiskAssessor
	Root() string
	All() []*types.Project
	VCSOptions() types.VCSOptions
	ComputeTargetKey(ctx context.Context, projectPath, target string, charms []string) (key string, lines []string, err error)
	ExpandAffectedSet(ctx context.Context, target string, baseRef string) (targets []types.Target, source string, fellBack bool, res *types.AffectedResult, err error)
}

// GateEnv is what the gate takes from the process it runs in rather than from the
// workspace.
type GateEnv struct {
	// DryRun makes the invocation not a gate: it executes nothing.
	DryRun bool
	// Nested is a magus running under another magus. It reads its ancestors' claims as
	// load, so it advises where a top-level gate would refuse.
	Nested bool
	// Version is the magus version a gate record is filed under.
	Version string
	// PoolProbe reports whether the broker's pool is saturated and what it saw. The
	// answer is printed, never decisive; nil reads as not probed.
	PoolProbe func(context.Context) (saturated bool, state string)
	// Repairs lists those of a ci run's recorded repairs that a checkout still holds
	// uncommitted.
	Repairs func(ctx context.Context, drv types.VCSDriver, dir string, files []types.RepairedFile) ([]types.RepairedFile, error)
}

// GateRedundancy carries one ci invocation's gate identity between the
// pre-run check and the post-run record: the branch, the commit, the input
// fingerprint minted from the same per-step cache keys the run mints, and the
// selection it covers. nil is the inert state (every method tolerates it),
// so a caller can thread one unconditionally.
type GateRedundancy struct {
	ws       GateWorkspace
	root     string
	drv      types.VCSDriver
	target   string
	ref      string
	commit   string
	fp       string
	projects []string
	charms   []string
	// undeclared is the subset of projects nothing but an undeclared changed file
	// selected; set by the affected path, empty on the `magus run ci` one, which
	// names its own selection.
	undeclared []string
	// tier is what size narrowed the gate to; empty runs the full gate.
	tier check.Tier
	// scope is what this gate's verdict covers; see gateScope.
	scope sessions.GateScope
	// poolProbe reads the broker's load for the refusal message; nil reads as not probed.
	poolProbe func(context.Context) (bool, string)
	// repairs lists those of a ci run's recorded repairs a checkout still holds uncommitted.
	repairs func(ctx context.Context, drv types.VCSDriver, dir string, files []types.RepairedFile) ([]types.RepairedFile, error)
	// version is the magus version a gate record is filed under.
	version string
	// nested is GateEnv.Nested.
	nested bool
}

// gateFinding is one redundancy match: the green gate, and either an identical
// fingerprint (no delta, identical true) or a delta that tiers trivial, as one
// tier line per path.
type gateFinding struct {
	rec       sessions.GateRecord
	identical bool
	delta     []string
}

// IsGateInvocation reports whether this invocation is THE gate: the ci target, run whole
// rather than as a shard, and actually executing.
//
// The redundancy check and lock supersession (MGS3014) both key on it, and they have to
// agree on what a gate is. One would otherwise defer a run the other would abort, on two
// definitions that drifted.
func IsGateInvocation(target string, partial, dryRun bool) bool {
	return target == types.TargetCI && !partial && !dryRun
}

// PrepareGateRedundancy computes the gate identity for a ci invocation, or
// nil when the invocation is not one the check can vouch for: not the ci
// target, a dry run, a shard or otherwise partial run, no VCS branch to key
// on, or a selection whose cache keys cannot be computed. nil means the run
// proceeds exactly as before this feature existed, with no output at all.
func PrepareGateRedundancy(ctx context.Context, ws GateWorkspace, env GateEnv, target string, targets []types.Target, charms []string, partial bool) *GateRedundancy {
	if !IsGateInvocation(target, partial, env.DryRun) {
		return nil
	}
	res, err := vcs.Resolve(ctx, ws.Root(), "", ws.VCSOptions())
	if err != nil || res.VCS == nil || res.Source == types.VCSSourceDisabled {
		return nil
	}
	meta, err := res.VCS.Metadata(ctx, ws.Root())
	if err != nil || meta.Ref == "" || meta.ID == "" {
		return nil
	}
	steps := make([]gateStep, 0, len(targets))
	projects := make([]string, 0, len(targets))
	for _, t := range targets {
		key, _, err := ws.ComputeTargetKey(ctx, t.Path, t.Name, charms)
		if err != nil {
			return nil
		}
		steps = append(steps, gateStep{Project: t.Path, Target: t.Name, Key: key})
		projects = append(projects, t.Path)
	}
	slices.Sort(projects)
	sorted := slices.Clone(charms)
	slices.Sort(sorted)
	return &GateRedundancy{
		ws:        ws,
		root:      ws.Root(),
		drv:       res.VCS,
		target:    target,
		ref:       meta.Ref,
		commit:    meta.ID,
		fp:        gateFingerprint(steps),
		projects:  projects,
		charms:    sorted,
		scope:     gateScope(ctx, ws, target, projects),
		poolProbe: env.PoolProbe,
		repairs:   env.Repairs,
		version:   env.Version,
		nested:    env.Nested,
	}
}

// gateScope is affected when projects hold every project the branch affects against its
// default base, and project otherwise. The default base, not the run's own: the push rule
// asks about the branch CI builds, so `affected ci --base <rev>` over a narrower range
// does not cover it, and `run ci` over every affected project does.
func gateScope(ctx context.Context, ws GateWorkspace, target string, projects []string) sessions.GateScope {
	affected, _, _, _, err := ws.ExpandAffectedSet(ctx, target, "")
	if err != nil {
		slog.InfoContext(ctx, "recording this gate as project scope: the affected set could not be read",
			attr.Error(err), attr.Component("gate redundancy"))
		return sessions.GateScopeProject
	}
	return scopeOver(affected, projects)
}

// scopeOver is affected when projects hold every affected target's project.
func scopeOver(affected []types.Target, projects []string) sessions.GateScope {
	for _, t := range affected {
		if !slices.Contains(projects, t.Path) {
			return sessions.GateScopeProject
		}
	}
	return sessions.GateScopeAffected
}

// NoteUndeclaredSeeds records which of this gate's projects were selected only through
// files no project declares, for the record written after the run. Nil-safe, like every
// other method here: the gate is inert on a non-ci or dry run.
func (g *GateRedundancy) NoteUndeclaredSeeds(projects []string) {
	if g == nil {
		return
	}
	g.undeclared = projects
}

// Evaluate applies the redundancy decision before the gate runs. A nil error
// means the gate RUNS: there is no skip-and-succeed state. A non-nil error is
// the MGS3010 refusal, exiting 75; the refusal is also recorded in the session
// store as a deferred gate record, so the decision is interrogable later. The
// advisory case prints the identical finding to stderr and still returns nil.
func (g *GateRedundancy) Evaluate(ctx context.Context, disabled bool) error {
	if g == nil {
		return nil
	}
	// Not a redundancy check, so --no-redundancy-check does not skip it.
	if err := g.uncommittedRepair(ctx); err != nil {
		return err
	}
	finding, redundant := g.finding(ctx, disabled)
	// Probed for the message, not the decision: a refusal should say what the machine was
	// doing. DecideGate does not read it.
	pool := "not probed"
	if redundant && !disabled && g.poolProbe != nil {
		_, pool = g.poolProbe(ctx)
	}
	facts := gateFacts{
		Redundant: redundant,
		Forced:    disabled,
		Nested:    g.nested,
	}
	switch decideGate(facts) {
	case gateRefuse:
		g.recordDeferral(ctx, finding.rec)
		return gateRefusal{types.DiagnosticErrorf(types.RedundantGateDeferred,
			"not running the %s gate on %s: it re-verifies a gate that already passed; pass --no-redundancy-check to run it anyway\n%s",
			g.target, g.ref, fmt.Sprintf("%s\n  machine pool: %s", g.renderFinding(finding), pool))}
	case gateAdvise:
		slog.InfoContext(ctx, fmt.Sprintf("this %s gate re-verifies one that already passed; running anyway.\n%s\n  machine pool: %s",
			g.target, g.renderFinding(finding), pool), attr.Notice(""), attr.Component("magus"))
	}
	return nil
}

// uncommittedRepair refuses with MGS3057 while a file the newest repairing
// ci run left still holds those bytes uncommitted, on any branch. It records
// nothing, so the repair's record stays newest. An unanswered check is said.
func (g *GateRedundancy) uncommittedRepair(ctx context.Context) error {
	rec, held, err := g.heldRepairs(ctx)
	switch {
	case err != nil:
		slog.InfoContext(ctx, "not checking for an uncommitted ci repair", attr.Error(err), attr.Component("gate redundancy"))
	case len(held) > 0:
		paths := make([]string, len(held))
		for i, f := range held {
			paths[i] = f.Path
		}
		return types.DiagnosticErrorf(types.UncommittedRepair,
			"not running the %s gate: run %s repaired %s still uncommitted: %s. Commit them and run ci again; an uncommitted repair gets no verdict",
			g.target, cmp.Or(rec.Inv, "unrecorded"), plural.Count(len(held), "file", "files"), strings.Join(paths, ", "))
	}
	return nil
}

// heldRepairs is the newest record of a repairing ci run and those of its
// files that still hold the repaired bytes uncommitted.
func (g *GateRedundancy) heldRepairs(ctx context.Context) (sessions.GateRecord, []types.RepairedFile, error) {
	dir, err := sessions.Dir(g.root)
	if err != nil {
		return sessions.GateRecord{}, nil, err
	}
	fold, err := session.ReadAll(dir)
	if err != nil {
		return sessions.GateRecord{}, nil, err
	}
	rec, ok := sessions.LatestRepair(fold, g.target)
	if !ok {
		return rec, nil, nil
	}
	if g.drv == nil || g.repairs == nil {
		return rec, nil, fmt.Errorf("run %s recorded repairs and no VCS driver or repair probe is in hand", cmp.Or(rec.Inv, "unrecorded"))
	}
	held, err := g.repairs(ctx, g.drv, g.root, rec.RepairedFiles)
	return rec, held, err
}

// finding looks up the newest gate verdict for this branch and reports whether
// this run would re-verify it: an identical input fingerprint, or a delta that
// tiers trivial.
func (g *GateRedundancy) finding(ctx context.Context, disabled bool) (gateFinding, bool) {
	if disabled {
		return gateFinding{}, false
	}
	dir, err := sessions.Dir(g.root)
	if err != nil {
		return gateFinding{}, false
	}
	fold, err := session.ReadAll(dir)
	if err != nil {
		return gateFinding{}, false
	}
	rec, ok := sessions.LatestGate(fold, g.ref, g.target)
	if !ok || rec.Outcome != session.OutcomePass || rec.Commit == "" {
		return gateFinding{}, false
	}
	if rec.Fingerprint != "" && rec.Fingerprint == g.fp {
		return gateFinding{rec: rec, identical: true}, true
	}
	if !slices.Equal(g.charms, rec.Charms) {
		return gateFinding{}, false
	}
	for _, p := range g.projects {
		if !slices.Contains(rec.Projects, p) {
			return gateFinding{}, false
		}
	}
	if g.commit != rec.Commit {
		history, err := g.drv.History(ctx, g.root, types.HistoryQuery{Limit: GateMergeScanLimit})
		if err != nil || !MergeFreeRange(history, rec.Commit) {
			return gateFinding{}, false
		}
	}
	rep, err := g.ws.AssessRisk(ctx, g.target, types.TierOptions{Base: rec.Commit})
	if err != nil || rep.Tier != check.RiskTrivial {
		return gateFinding{}, false
	}
	return gateFinding{rec: rec, delta: rep.Lines()}, true
}

// Size tiers the change against base once the gate is going to run, and returns the
// report when a gate smaller than full suffices. nil runs the full gate: an inert gate,
// a forced run, a change that could not be assessed, or one that tiers full.
func (g *GateRedundancy) Size(ctx context.Context, base string, forced bool) *types.TierReport {
	if g == nil || forced {
		return nil
	}
	rep, err := g.ws.AssessRisk(ctx, g.target, types.TierOptions{Base: base})
	if err != nil {
		slog.DebugContext(ctx, "change not assessed; running the full gate", attr.Error(err))
		return nil
	}
	if rep.Tier == check.RiskFull {
		return nil
	}
	g.tier = rep.Tier
	return &rep
}

// RenderSizing prints a sized gate in the redundancy refusal's shape: the decision and
// its override, every changed path with its tier, and the commands that run instead.
func RenderSizing(target string, rep types.TierReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s gate sized %s against %s; override: --no-redundancy-check\n", target, rep.Tier, shortRev(rep.Base))
	for _, line := range rep.Lines() {
		fmt.Fprintf(&b, "  %s\n", line)
	}
	gate := "none"
	if cmds := rep.Commands(); len(cmds) > 0 {
		gate = strings.Join(cmds, " && ")
	}
	fmt.Fprintf(&b, "  gate: %s\n", gate)
	for _, s := range rep.Gate {
		if s.Op != "" {
			fmt.Fprintf(&b, "  narrowed: %s in %s %s runs %d package(s): %s\n",
				s.Op, strings.Join(s.Projects, " "), s.Target, len(s.Packages), strings.Join(s.Packages, " "))
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// renderFinding prints every input a reader needs to reconstruct the decision:
// the matched green gate, and the delta with each file's tier, class and the
// declaration or mechanism that decided it.
func (g *GateRedundancy) renderFinding(f gateFinding) string {
	return renderGreenGate(f.rec) + g.renderDelta(f)
}

// renderGreenGate is the matched green gate's line, and the projects it covered that only
// undeclared files selected.
func renderGreenGate(rec sessions.GateRecord) string {
	out := fmt.Sprintf("  green gate: run %s, branch %s, commit %s, recorded %s (%s ago), fingerprint %s",
		cmp.Or(rec.Inv, "unrecorded"), rec.Ref, shortRev(rec.Commit), rec.At.UTC().Format(time.RFC3339), gateAge(rec.At), shortFingerprint(rec.Fingerprint))
	return out + undeclaredSeeds(rec.UndeclaredSeeds)
}

// undeclaredSeeds names the projects a gate covered that only undeclared files selected.
func undeclaredSeeds(seeds []string) string {
	if len(seeds) == 0 {
		return ""
	}
	return fmt.Sprintf("\n  that gate covered %d project(s) selected only by files nothing declares (%s): %s",
		len(seeds), types.UndeclaredSeedingFile, strings.Join(seeds, ", "))
}

// renderDelta is what changed since the matched gate, each file with its tier and class.
func (g *GateRedundancy) renderDelta(f gateFinding) string {
	switch {
	case f.identical:
		return fmt.Sprintf("\n  delta since that gate: none; this run's input fingerprint %s matches it exactly", shortFingerprint(g.fp))
	case len(f.delta) == 0:
		return "\n  delta since that gate: no changed paths"
	}
	return "\n  delta since that gate, every file:\n    " + strings.Join(f.delta, "\n    ")
}

// Record files the gate's verdict in the per-repository session store, so a
// sibling worktree's next gate can see it. ctx should be the invocation
// context, so the record joins to the run's journal by invocation id. An
// interrupted or timed-out run records nothing: neither is a verdict on the
// inputs. Store errors are logged, never returned: a store that can fail a
// build is worse than none.

// Record persists this run's verdict, so a later gate on the same inputs can defer to it.
//
// A run that was CUT SHORT records nothing. Testing runErr alone is not enough to see
// that: a cancelled run can return no error at all, because the targets it had already
// dispatched report their own cancellation and the run itself then has nothing to add. A
// gate killed at six seconds by a terminating shell took that path and recorded a PASS
// over nine projects it never ran, and MGS3010 refused every later gate on that commit
// on the strength of it.
//
// cutShort is read by the CALLER, at the instant the run returns, rather than here. The
// invocation's context is cancelled asynchronously by the signal handler, so reading it
// one call deeper widens the window in which a run that genuinely finished is mistaken
// for one that did not.
//
// The residual window is the run's own last instant, and it is deliberately resolved
// toward NOT recording: a dropped verdict costs one re-run, while a recorded verdict for
// a run that did not happen makes MGS3010 refuse every later gate on that commit.
func (g *GateRedundancy) Record(ctx context.Context, runErr error, cutShort bool, failed []journal.Event) {
	if g == nil || cutShort || errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		return
	}
	outcome, unrun := gateOutcome(runErr, failed)
	var repair *types.UncommittedRepairError
	var repaired []types.RepairedFile
	if errors.As(runErr, &repair) {
		repaired = repair.Files
	}
	g.append(ctx, sessions.GateResult{
		Target:          g.target,
		Scope:           g.scope,
		Ref:             g.ref,
		Commit:          g.commit,
		Outcome:         outcome,
		Unrun:           unrun,
		Fingerprint:     g.fp,
		Projects:        g.projects,
		Charms:          g.charms,
		UndeclaredSeeds: g.undeclared,
		Tier:            string(g.tier),
		Inv:             journal.InvocationIDFromContext(ctx),
		RepairedFiles:   repaired,
	})
}

// recordDeferral persists the refusal itself, pointing at the green gate it
// deferred to, so `magus session`'s store carries the decision and not only
// the runs.
func (g *GateRedundancy) recordDeferral(ctx context.Context, green sessions.GateRecord) {
	g.append(ctx, sessions.GateResult{
		Target:      g.target,
		Scope:       g.scope,
		Ref:         g.ref,
		Commit:      g.commit,
		Outcome:     sessions.OutcomeDeferred,
		Fingerprint: g.fp,
		Projects:    g.projects,
		Charms:      g.charms,
		DeferredTo:  green.Commit,
	})
}

func (g *GateRedundancy) append(ctx context.Context, rec sessions.GateResult) {
	dir, err := sessions.Dir(g.root)
	if err != nil {
		return
	}
	err = sessions.RecordGate(dir, rec, session.InvocationStart{Origin: trail.LocalOrigin(ctx), Workspace: g.root, Command: "gate", Version: g.version})
	if err != nil {
		slog.With(attr.Component("gate redundancy")).DebugContext(ctx, "record not written",
			slog.String("store", dir), attr.Error(err))
	}
}

// PlanInheritance is the CI-shaped entry of the verdict-inheritance decision
// for `magus affected ci --plan`. It fires only when the wired CI provider is
// active (the github spell answers only under Actions) and names a green run
// whose delta tiers trivial; every other path (no provider, no green run, a
// merge in the range, a delta above trivial, gate_inherit false) returns nil,
// and the plan proceeds with no output from this feature.
//
// The assessment runs HEAD's tree against the GREEN run's commit through
// AssessChange, the same one the local redundancy check uses, so both features
// share one definition of trivial. The annotation emitted on a hit is the run-page
// half of the explicitness contract; the summary rides the plan output to the
// workflow's report job.
func PlanInheritance(ctx context.Context, ws GateWorkspace) *InheritFinding {
	if inheritOff(ws.All()) {
		return nil
	}
	ann := annotate.Detect()
	src, ok := ann.(annotate.RunSource)
	if !ok {
		return nil
	}
	res, err := vcs.Resolve(ctx, ws.Root(), "", ws.VCSOptions())
	if err != nil || res.VCS == nil || res.Source == types.VCSSourceDisabled {
		return nil
	}
	drv := res.VCS
	probe := inheritProbe{
		LastGreenRun: func(context.Context) (string, string, bool) {
			green, ok := src.LastGreenRun()
			return green.Run, green.Commit, ok
		},
		History: func(ctx context.Context) ([]types.Commit, error) {
			return drv.History(ctx, ws.Root(), types.HistoryQuery{Limit: GateMergeScanLimit})
		},
		Assess: func(ctx context.Context, green string) (types.TierReport, error) {
			return ws.AssessRisk(ctx, types.TargetCI, types.TierOptions{Base: green})
		},
	}
	finding, ok := probe.Evaluate(ctx)
	if !ok {
		return nil
	}
	_ = ann.Annotate(annotate.Annotation{ // a lost annotation leaves the plan and its exit status as they were
		Level:   annotate.LevelNotice,
		Title:   "magus: CI verdict inherited",
		Message: finding.AnnotationText(),
	})
	return &finding
}

// DescribePool is whether the pool is saturated, with the capacity behind either answer, so
// the finding can print what the probe saw.
func DescribePool(m *budget.Snapshot) (saturated bool, state string) {
	desc := fmt.Sprintf("%d of %d slots held", m.HeldSlots, m.BudgetSlots)
	if !poolSaturated(m) {
		return false, "idle: " + desc
	}
	return true, "saturated: " + desc
}

// gateRefusal states the process status the MGS3010 refusal asks for. Its own
// type rather than sharing the machine gate's, for the reason that one and the
// lock's do not share: two decisions that happen to agree on 75 must not move
// together. It wraps, so errors.Is against the diagnostic code keeps matching.
type gateRefusal struct{ error }

func (gateRefusal) ExitCode() int { return 75 }

func (e gateRefusal) Unwrap() error { return e.error }

func shortFingerprint(fp string) string {
	if fp == "" {
		return "unrecorded"
	}
	if len(fp) > 12 {
		return fp[:12]
	}
	return fp
}

// gateAge renders how long ago the green gate ran, at a resolution a reader
// acts on: seconds under a minute, else minutes (and hours) with the zero
// seconds trimmed.
func gateAge(at time.Time) string {
	d := time.Since(at)
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	return strings.TrimSuffix(d.Round(time.Minute).String(), "0s")
}
