package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus"
	internalci "github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/proc"
	runPkg "github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// gateMergeScanLimit bounds the history walk that looks for a merge commit
// between the green gate and HEAD. A green gate older than this many commits
// simply re-gates.
const gateMergeScanLimit = 200

// gatePoolProbeTimeout bounds the admission-daemon probe. The daemon is an
// accelerant, never a capability gate: a probe that cannot answer in time
// reads as idle and the gate runs.
var gatePoolProbeTimeout = 2 * time.Second

// gateRedundancy carries one ci invocation's gate identity between the
// pre-run check and the post-run record: the branch, the commit, the input
// fingerprint minted from the same per-step cache keys the run mints, and the
// selection it covers. nil is the inert state - every method tolerates it -
// so a caller can thread one unconditionally.
type gateRedundancy struct {
	m        *magus.Magus
	root     string
	drv      types.VCSDriver
	target   string
	ref      string
	commit   string
	fp       string
	projects []string
	charms   []string
}

// gateFinding is one redundancy match: the green gate, and either an identical
// fingerprint (Delta empty, Identical true) or an all-low-risk delta.
type gateFinding struct {
	rec       sessions.GateRecord
	identical bool
	delta     internalci.GateDelta
}

// prepareGateRedundancy computes the gate identity for a ci invocation, or
// nil when the invocation is not one the check can vouch for: not the ci
// target, a dry run, a shard or otherwise partial run, no VCS branch to key
// on, or a selection whose cache keys cannot be computed. nil means the run
// proceeds exactly as before this feature existed, with no output at all.
func prepareGateRedundancy(ctx context.Context, m *magus.Magus, target string, targets []types.Target, charms []string, partial bool) *gateRedundancy {
	if target != types.TargetCI || partial || globalCfg.DryRun {
		return nil
	}
	res, err := vcs.Resolve(ctx, m.Root(), "", m.VCSOptions())
	if err != nil || res.VCS == nil || res.Source == types.VCSSourceDisabled {
		return nil
	}
	meta, err := res.VCS.Metadata(ctx, m.Root())
	if err != nil || meta.Ref == "" || meta.ID == "" {
		return nil
	}
	steps := make([]internalci.GateStep, 0, len(targets))
	projects := make([]string, 0, len(targets))
	for _, t := range targets {
		key, _, err := m.ComputeTargetKey(ctx, t.Path, t.Name, charms)
		if err != nil {
			return nil
		}
		steps = append(steps, internalci.GateStep{Project: t.Path, Target: t.Name, Key: key})
		projects = append(projects, t.Path)
	}
	slices.Sort(projects)
	sorted := slices.Clone(charms)
	slices.Sort(sorted)
	return &gateRedundancy{
		m:        m,
		root:     m.Root(),
		drv:      res.VCS,
		target:   target,
		ref:      meta.Ref,
		commit:   meta.ID,
		fp:       internalci.GateFingerprint(steps),
		projects: projects,
		charms:   sorted,
	}
}

// evaluate applies the redundancy decision before the gate runs. A nil error
// means the gate RUNS - there is no skip-and-succeed state. A non-nil error is
// the MGS3010 refusal, exiting 75; the refusal is also recorded in the session
// store as a deferred gate record, so the decision is interrogable later. The
// advisory case prints the identical finding to stderr and still returns nil.
func (g *gateRedundancy) evaluate(ctx context.Context, disabled bool) error {
	if g == nil {
		return nil
	}
	finding, redundant := g.finding(ctx, disabled)
	var saturated bool
	pool := "not probed"
	if redundant && !disabled {
		saturated, pool = gatePoolProbe(ctx)
	}
	facts := internalci.GateFacts{
		Redundant: redundant,
		Saturated: saturated,
		Forced:    disabled,
		Nested:    runPkg.CurrentLevel() > 0,
	}
	switch internalci.DecideGate(facts) {
	case internalci.GateRefuse:
		g.recordDeferral(ctx, finding.rec)
		return gateRefusal{types.DiagnosticErrorf(types.RedundantGateDeferred,
			"not running the %s gate for branch %s: it is redundant and this machine's build pool is saturated.\n%s\n  machine pool: %s\n  override: pass --no-redundancy-check to run it here anyway\n  alternative: push; the pull request runs the identical check",
			g.target, g.ref, g.renderFinding(finding), pool)}
	case internalci.GateAdvise:
		fmt.Fprintf(os.Stderr,
			"magus: this %s gate re-verifies one that already passed; running anyway.\n%s\n  machine pool: %s\n",
			g.target, g.renderFinding(finding), pool)
	}
	return nil
}

// finding looks up the newest gate verdict for this branch and reports whether
// this run would re-verify it: an identical input fingerprint, or a delta that
// classifies entirely low-risk.
func (g *gateRedundancy) finding(ctx context.Context, disabled bool) (gateFinding, bool) {
	if disabled {
		return gateFinding{}, false
	}
	dir, err := sessions.Dir(g.root)
	if err != nil {
		return gateFinding{}, false
	}
	fold, err := sessions.ReadAll(dir)
	if err != nil {
		return gateFinding{}, false
	}
	rec, ok := sessions.LatestGate(fold, g.ref, g.target)
	if !ok || rec.Outcome != sessions.OutcomePass || rec.Commit == "" {
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
		history, err := g.drv.History(ctx, g.root, gateMergeScanLimit)
		if err != nil || !internalci.MergeFreeRange(history, rec.Commit) {
			return gateFinding{}, false
		}
	}
	changed, err := g.drv.ChangedFiles(ctx, g.root, rec.Commit)
	if err != nil {
		return gateFinding{}, false
	}
	delta := g.classifier().Classify(ctx, changed, rec.Commit)
	if !delta.LowRiskOnly() {
		return gateFinding{}, false
	}
	return gateFinding{rec: rec, delta: delta}, true
}

// renderFinding prints every input a reader needs to reconstruct the decision:
// the matched green gate, and the delta with each file's class and the
// declaration or mechanism that classified it.
func (g *gateRedundancy) renderFinding(f gateFinding) string {
	var b strings.Builder
	inv := f.rec.Inv
	if inv == "" {
		inv = "unrecorded"
	}
	fmt.Fprintf(&b, "  green gate: run %s, branch %s, commit %s, recorded %s (%s ago), fingerprint %s",
		inv, f.rec.Ref, shortCommit(f.rec.Commit), f.rec.At.UTC().Format(time.RFC3339), gateAge(f.rec.At), shortFingerprint(f.rec.Fingerprint))
	if f.identical {
		b.WriteString("\n  delta since that gate: none; this run's input fingerprint " + shortFingerprint(g.fp) + " matches it exactly")
		return b.String()
	}
	if len(f.delta.Paths) == 0 {
		b.WriteString("\n  delta since that gate: no changed paths")
		return b.String()
	}
	b.WriteString("\n  delta since that gate, every file:")
	for _, line := range f.delta.Lines() {
		b.WriteString("\n    " + line)
	}
	return b.String()
}

// classifier adapts the workspace to the risk classifier: describe-file roles,
// the effective gate_low_risk prose globs, blob-at-revision reads, and
// working-tree reads.
func (g *gateRedundancy) classifier() internalci.ChangeClassifier {
	c := internalci.ChangeClassifier{
		Role: func(ctx context.Context, paths []string) (map[string]string, error) {
			entries, err := g.m.ClassifyFiles(ctx, paths)
			if err != nil {
				return nil, err
			}
			roles := make(map[string]string, len(entries))
			for _, e := range entries {
				roles[e.Path] = e.Role
			}
			return roles, nil
		},
		Prose:  internalci.ProseScopes(g.m.All()),
		Syntax: spells.CommentSyntaxIndex(resolvedSpells(g.m.All())),
		Working: func(p string) (string, error) {
			b, err := os.ReadFile(filepath.Join(g.root, filepath.FromSlash(p)))
			return string(b), err
		},
	}
	if reader, ok := g.drv.(types.RevisionFileReader); ok {
		c.At = func(ctx context.Context, rev, p string) (string, error) {
			return reader.ReadFileAt(ctx, g.root, rev, p)
		}
	}
	return c
}

// record files the gate's verdict in the per-repository session store, so a
// sibling worktree's next gate can see it. ctx should be the invocation
// context, so the record joins to the run's journal by invocation id. An
// interrupted or timed-out run records nothing: neither is a verdict on the
// inputs. Store errors are logged, never returned - a store that can fail a
// build is worse than none.
// resolvedSpells flattens every project's resolved spells so the syntax index
// covers workspace spells alongside the built-ins.
func resolvedSpells(projects []*types.Project) []*spells.Spell {
	var out []*spells.Spell
	for _, p := range projects {
		out = append(out, p.ResolvedSpells...)
	}
	return out
}

func (g *gateRedundancy) record(ctx context.Context, runErr error) {
	if g == nil || errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		return
	}
	outcome := sessions.OutcomePass
	if runErr != nil {
		outcome = sessions.OutcomeFail
	}
	g.append(ctx, sessions.GateResult{
		Target:      g.target,
		Ref:         g.ref,
		Commit:      g.commit,
		Outcome:     outcome,
		Fingerprint: g.fp,
		Projects:    g.projects,
		Charms:      g.charms,
		Inv:         journal.InvocationIDFromContext(ctx),
	})
}

// recordDeferral persists the refusal itself, pointing at the green gate it
// deferred to, so `magus session`'s store carries the decision and not only
// the runs.
func (g *gateRedundancy) recordDeferral(ctx context.Context, green sessions.GateRecord) {
	g.append(ctx, sessions.GateResult{
		Target:      g.target,
		Ref:         g.ref,
		Commit:      g.commit,
		Outcome:     sessions.OutcomeDeferred,
		Fingerprint: g.fp,
		Projects:    g.projects,
		Charms:      g.charms,
		DeferredTo:  green.Commit,
	})
}

func (g *gateRedundancy) append(ctx context.Context, rec sessions.GateResult) {
	dir, err := sessions.Dir(g.root)
	if err != nil {
		return
	}
	err = sessions.RecordGate(dir, rec, sessions.SessionStart{Workspace: g.root, Command: "gate", Version: version})
	if err != nil {
		slog.DebugContext(ctx, "gate redundancy: record not written",
			slog.String("store", dir), slog.String("error", err.Error()))
	}
}

// gatePoolProbe is swappable so a test can decide saturation without a
// daemon; the machine gate's wait timings are vars for the same reason.
var gatePoolProbe = gatePoolSaturated

// gatePoolSaturated asks the admission daemon whether a new run would queue,
// and always says what it saw, so the finding can print the pool state behind
// either answer. Every failure - no socket, no answer, a server that
// arbitrates no budget - reads as idle: the daemon is an accelerant, never a
// capability gate.
func gatePoolSaturated(ctx context.Context) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, gatePoolProbeTimeout)
	defer cancel()
	addr, ok := proc.LookupStableSocket(ctx)
	if !ok {
		return false, "no admission daemon reachable; treated as idle"
	}
	reply, err := proc.QueryStatus(ctx, addr)
	if err != nil {
		return false, "the admission daemon did not answer; treated as idle"
	}
	if reply.Machine == nil {
		return false, "the reachable server arbitrates no machine budget; treated as idle"
	}
	m := reply.Machine
	desc := strconv.Itoa(m.HeldSlots) + " of " + strconv.Itoa(m.BudgetSlots) + " slots held"
	if n := len(m.Waiters); n > 0 {
		runs := "runs"
		if n == 1 {
			runs = "run"
		}
		desc += ", " + strconv.Itoa(n) + " " + runs + " queued"
	}
	if !internalci.PoolSaturated(m) {
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

func shortCommit(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

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
