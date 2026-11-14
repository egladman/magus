package doctor

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/check"
	"github.com/egladman/magus/vcs"
)

// checkMergeDriverLoads reports a registered merge driver that cannot load this workspace.
//
// The driver is what settles a conflict in a generated file, and it is registered as a
// path in git config rather than resolved per invocation. Nothing has been checking that
// the binary at that path still works HERE, and the failure is silent by construction:
// git runs the command, the command exits non-zero, git reads that as "could not merge"
// and leaves conflict markers. The result looks exactly like an ordinary hand-merge, so
// the driver being broken and the driver being absent are indistinguishable at the point
// a person notices.
//
// Measured 2026-09-05 in this repo: 142 worktrees shared one registration pointing at a
// v0.3.0 build, which predates the magusfile keys this workspace declares and so failed
// at load on every conflict.
//
// It probes with `-h`, which returns before the child opens a workspace, and then with a
// real workspace load, because those answer different questions. A binary can dispatch
// `vcs merge-driver` perfectly and still be unable to read this magusfile, and that is
// exactly the shape this exists to name: EnsureMergeDriver accepts a driver on the
// strength of the first probe alone.
//
// The diff drivers are installed with the merge driver, so a registration missing any of
// them fails here too, and the remedy is the init verb that installs them.
func (r *runner) checkMergeDriverLoads(ctx context.Context) check.Check {
	const name = "merge-driver-loads-workspace"

	registered := r.registeredMergeDriver(ctx)
	if registered == "" {
		return check.Check{Name: name, Status: check.OK,
			Message: "no merge driver registered, so generated conflicts fall back to a hand merge"}
	}
	exe := driverExecutable(registered)
	if exe == "" {
		return check.Check{Name: name, Status: check.Fail,
			Message: "the registered merge driver names no executable: " + registered}
	}
	missing, err := vcs.MissingDiffDrivers(ctx, r.ws.Root())
	if err != nil {
		return check.Check{Name: name, Status: check.Fail, Message: err.Error()}
	}

	// The workspace this doctor is inspecting, so the answer is about the tree in front of
	// the reader rather than about the driver in the abstract.
	probe := exec.Command(exe, "ls")
	probe.Dir = r.ws.Root()
	if out, err := probe.CombinedOutput(); err != nil {
		return check.Check{
			Name:   name,
			Status: check.Fail,
			Message: "the registered merge driver cannot load this workspace, so every conflict in a " +
				"generated file falls back to markers with nothing naming the cause",
			Details: []string{
				"registered: " + registered,
				"it said: " + firstLine(string(out)),
				fmt.Sprintf("point it at a magus that can read this tree: rebuild one (%s) or `magus self update`, then register the driver again", hint.Run.With("build", ".")),
			},
		}
	}
	if len(missing) > 0 {
		return check.Check{
			Name:   name,
			Status: check.Fail,
			Message: "the merge driver is registered without the diff drivers installed beside it, so " +
				"hunk headers name no declaration and a footprint cannot say what a change touched",
			Details: append(missing, r.installRemedy(ctx)),
		}
	}
	return check.Check{Name: name, Status: check.OK,
		Message: "the registered merge driver loads this workspace"}
}

// registeredMergeDriver returns the merge driver the workspace's VCS would run, or "" when
// none is registered or the backend registers none. It is the effective registration, so a
// worktree override, which is the whole point of install-dogfood, is the one reported.
func (r *runner) registeredMergeDriver(ctx context.Context) string {
	res, err := vcs.Resolve(ctx, r.ws.Root(), "", r.ws.VCSOptions())
	if err != nil || res.VCS == nil {
		return ""
	}
	cmd, err := res.VCS.MergeDriverCommand(ctx, r.ws.Root())
	if err != nil {
		return ""
	}
	return cmd
}

// installRemedy names the init command that installs the diff drivers; when no VCS
// resolves it says so rather than guessing a backend.
func (r *runner) installRemedy(ctx context.Context) string {
	res, err := vcs.Resolve(ctx, r.ws.Root(), "", r.ws.VCSOptions())
	if err != nil {
		return "no VCS resolved for this workspace, so there is no backend to install them for: " + err.Error()
	}
	if res.VCS == nil || res.Source == types.VCSSourceDefault {
		return "no VCS resolved for this workspace (nothing claims it and none is named), so there is no backend to install them for"
	}
	return fmt.Sprintf("install them with `%s`", hint.Init.With("--vcs", res.Name))
}

// driverExecutable pulls the program out of a registered driver command, unwrapping the
// quoting vcs/git.go adds for a path containing spaces.
func driverExecutable(registered string) string {
	if rest, ok := strings.CutPrefix(registered, `"`); ok {
		exe, _, found := strings.Cut(rest, `"`)
		if !found {
			return ""
		}
		return exe
	}
	exe, _, _ := strings.Cut(registered, " ")
	return exe
}

// checkOwedRegeneration reports regenerations the merge driver recorded that no run has
// settled yet. Each names generated files still holding one side of a merge, so a
// commit made now carries stale output. The record outlives a settle hook that failed
// or was bypassed with --no-verify, and this is where that becomes visible.
func (r *runner) checkOwedRegeneration(ctx context.Context) check.Check {
	const name = "owed-regeneration"
	owed, err := vcs.OwedRegenerations(ctx, r.ws.Root())
	if err != nil {
		return check.Check{Name: name, Status: check.Fail, Message: err.Error()}
	}
	if len(owed) == 0 {
		return check.Check{Name: name, Status: check.OK, Message: "no merge left a regeneration owed"}
	}
	path, _ := vcs.OwedRegenerationPath(ctx, r.ws.Root())
	details := make([]string, 0, len(owed)+2)
	for _, o := range owed {
		detail := hint.Run.With(o.Target+":rw", o.Project)
		if len(o.Paths) > 0 {
			detail += fmt.Sprintf(" (%d kept file(s): %s)", len(o.Paths), strings.Join(o.Paths, ", "))
		}
		details = append(details, detail)
	}
	details = append(details,
		fmt.Sprintf("settle them with `%s`, which runs them, stages the result and clears the record", hint.VCSResolve.String()),
		"record: "+path)
	return check.Check{
		Name:    name,
		Status:  check.Fail,
		Message: fmt.Sprintf("%d regeneration(s) a merge kept one side for have not run, so those generated files are stale", len(owed)),
		Details: details,
	}
}

// firstLine keeps a probe's complaint to the line that names the cause; a failing magus
// prints its diagnostic first and its usage after.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
