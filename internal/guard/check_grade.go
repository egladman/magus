package guard

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/types"
)

// NewCheckGrader grades a declared check against the guard's rules for the workspace at the
// root it is handed. Each root's rules load on the first check graded there, so a fork
// that declares none pays nothing, and are reused until a file they were read from
// changes: a grader lives as long as the Store, or the daemon, it was made for, and
// grading against rules an edit has since replaced would admit or refuse the wrong check.
// runCLI installs one with job.SetCheckGrader for every door that opens a Store naming
// none, such as magus\workspace().putJob. `magus job fork` grades with a [forkGrader]
// instead, which judges alike and keeps what it opened for the rest of the fork.
func NewCheckGrader(load RulesLoader) job.CheckGrader {
	return newRulesByRoot(load, nil).grade
}

// RulesLoader loads the guard's dependencies for the workspace at root, with the rules
// they came from, nil when none loaded. The CLI holds the wiring that builds them.
type RulesLoader func(ctx context.Context, root string) (Dependencies, *WorkspaceRules)

func newRulesByRoot(load RulesLoader, opens *WorkspaceMemo) *rulesByRoot {
	return &rulesByRoot{load: load, loaded: map[string]gradingRules{}, opens: opens}
}

// rulesByRoot holds each workspace's rules while their files stay unchanged.
type rulesByRoot struct {
	load   RulesLoader
	mu     sync.Mutex
	loaded map[string]gradingRules
	// opens is handed to every judgment as Dependencies.Opens: nil for the
	// installed grader, which outlives any one tree, and one fork's memo for a fork's.
	opens *WorkspaceMemo
}

func (r *rulesByRoot) grade(ctx context.Context, root string, row types.Job) job.CheckVerdict {
	return gradeCheck(ctx, r.current(ctx, root).judging(r.opens), root, row)
}

func (r *rulesByRoot) current(ctx context.Context, root string) gradingRules {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g, ok := r.loaded[root]; ok && g.unchanged() {
		return g
	}
	// The rules outlive this grade, so its cancellation must not decide them.
	g := loadGradingRules(context.WithoutCancel(ctx), r.load, root)
	r.loaded[root] = g
	return g
}

// gradingRules is a workspace's guard dependencies and the files its rules were read from.
type gradingRules struct {
	deps Dependencies
	// sources are the files the load read, with the id of the bytes it saw. Empty when no
	// rules loaded: nothing says which file to watch, so the next grade loads again.
	sources []interp.SourceFile
}

// judging is g's dependencies for one judgment, which keeps the workspaces it opens in
// opens when that is not nil.
func (g gradingRules) judging(opens *WorkspaceMemo) Dependencies {
	deps := g.deps
	deps.Opens = opens
	return deps
}

func loadGradingRules(ctx context.Context, load RulesLoader, root string) gradingRules {
	deps, rules := load(ctx, root)
	if rules != nil {
		return gradingRules{deps: deps, sources: rules.Policy().Sources}
	}
	if deps.Policy == nil {
		return gradingRules{deps: deps}
	}
	// A policy snapshot hit only when its key covered the bytes on disk.
	// So those bytes are the ones the rules came from.
	return gradingRules{deps: deps, sources: sourcesOnDisk(deps.Policy().Files)}
}

// unchanged reports whether every file the rules were read from still holds the bytes the
// load saw. A file that cannot be read counts as changed, so the next load decides.
func (g gradingRules) unchanged() bool {
	if len(g.sources) == 0 {
		return false
	}
	for _, s := range g.sources {
		data, err := os.ReadFile(s.Path)
		if err != nil || interp.ContentID(data) != s.ContentID {
			return false
		}
	}
	return true
}

// gradeCheck judges row's check as the guard would judge its holder typing it where it is
// taken ([job.WorkingCheckout]), the row standing in for a lease not stored yet.
//
// Only a deny of the check itself refuses. A deny that holds just as well for the
// compiling check describes the binary or the workspace (a stale hook binary) and not the
// command, so it is not graded and the verdict's NotGraded says why: a refusal has to
// come from a verdict on the check. Rules that would not load defer the grade instead
// (Deferred): a binary built at the checkout may load them, so the fork keeps its row.
// A deny from a judgment that ran out of time defers too.
func gradeCheck(ctx context.Context, deps Dependencies, root string, row types.Job) job.CheckVerdict {
	if deps.LoadFailure != nil {
		why := fmt.Sprintf("the workspace's rules did not load: %s", deps.LoadFailure)
		slog.WarnContext(ctx, fmt.Sprintf("check `%s` was not graded, so nothing says its worker may run it; the checkout's own binary may load the rules this one cannot: %s", row.Check, why),
			attr.Unchecked())
		return job.CheckVerdict{Deferred: why}
	}
	dir := job.WorkingCheckout(root, row)
	if row.Worktree.Standing() && row.CheckoutRoot == "" {
		// Until the fork makes the checkout, root, the repository it is made from, stands in.
		if _, err := os.Stat(dir); err != nil {
			worktree := *row.Worktree
			worktree.Root, dir = root, root
			row.Worktree = &worktree
		}
		row.CheckoutRoot = dir
	}
	judge := func(pending types.Job) (types.GuardJudgment, bool) {
		return JudgeLapsed(ctx, deps, Request{
			Input:      ownBinaryPrefix + workerCheckLine(*pending.Check),
			DryRun:     true,
			Lease:      pending.ID,
			PendingRow: &pending,
			Dir:        dir,
		})
	}
	deferLapse := func(v types.GuardJudgment) job.CheckVerdict {
		first, _, _ := strings.Cut(strings.TrimSpace(v.Reason), "\n")
		why := fmt.Sprintf("the guard ran out of time grading it: %s", first)
		slog.WarnContext(ctx, fmt.Sprintf("check `%s` was not graded, so nothing says its worker may run it: %s", row.Check, why), attr.Unchecked())
		return job.CheckVerdict{Deferred: why}
	}
	declared, lapsed := judge(row)
	if declared.Decision != string(types.GuardDeny) {
		return job.CheckVerdict{}
	}
	if lapsed {
		return deferLapse(declared)
	}
	baseline := row
	compiling := job.CompilingCheck()
	baseline.Check = &compiling
	baselineVerdict, lapsed := judge(baseline)
	if baselineVerdict.Decision == string(types.GuardDeny) && lapsed {
		return deferLapse(baselineVerdict)
	}
	if baselineVerdict.Decision == string(types.GuardDeny) {
		first, _, _ := strings.Cut(strings.TrimSpace(baselineVerdict.Reason), "\n")
		return job.CheckVerdict{NotGraded: fmt.Sprintf("the guard denies this worker `%s` as well, so the deny describes the binary or the workspace and not the check: %s", compiling, first)}
	}
	return job.CheckVerdict{Denied: true, Rule: declared.Rule, Reason: declared.Reason}
}

// reportForkFacts prints what a top-level fork leaves waiting. It never stops

// sourcesOnDisk names each file by the id of the bytes it holds now.
// It is nil when a file cannot be read, so the next grade loads again.
func sourcesOnDisk(paths []string) []interp.SourceFile {
	out := make([]interp.SourceFile, len(paths))
	for i, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		out[i] = interp.SourceFile{Path: p, ContentID: interp.ContentID(data)}
	}
	return out
}

// ownBinaryPrefix is how a holder types a check: with the ./magus of its own checkout,
// not whichever binary the PATH holds.
const ownBinaryPrefix = "./"

// workerCheckLine renders c with --budget-wait 0, which a worker's run needs.
// A script check has no such flag and is rendered as stored.
func workerCheckLine(c types.LeaseCheck) string {
	if c.Script != "" {
		return c.String()
	}
	return WorkerCheckCommand(c)
}

// outputsIn resolves a ref against the output store of the checkout at root. That
