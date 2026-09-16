package doctor

import (
	"os/exec"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
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
func (r *runner) checkMergeDriverLoads() types.DoctorCheck {
	const name = "merge-driver-loads-workspace"

	registered := r.registeredMergeDriver()
	if registered == "" {
		return types.DoctorCheck{Name: name, Status: types.DoctorOK,
			Message: "no merge driver registered, so generated conflicts fall back to a hand merge"}
	}
	exe := driverExecutable(registered)
	if exe == "" {
		return types.DoctorCheck{Name: name, Status: types.DoctorFail,
			Message: "the registered merge driver names no executable: " + registered}
	}

	// The workspace this doctor is inspecting, so the answer is about the tree in front of
	// the reader rather than about the driver in the abstract.
	probe := exec.Command(exe, "ls")
	probe.Dir = r.ws.Root()
	if out, err := probe.CombinedOutput(); err != nil {
		return types.DoctorCheck{
			Name:   name,
			Status: types.DoctorFail,
			Message: "the registered merge driver cannot load this workspace, so every conflict in a " +
				"generated file falls back to markers with nothing naming the cause",
			Details: []string{
				"registered: " + registered,
				"it said: " + firstLine(string(out)),
				"point it at a magus that can read this tree: " + hint.Run.With("dogfood", ".") +
					", then ./hack/install-dogfood.sh",
			},
		}
	}
	return types.DoctorCheck{Name: name, Status: types.DoctorOK,
		Message: "the registered merge driver loads this workspace"}
}

// registeredMergeDriver returns the effective merge.magus.driver for this worktree, or ""
// when none is set. Effective, not --local: a worktree override is the whole point of
// install-dogfood, and reading the shared scope would report the wrong one.
func (r *runner) registeredMergeDriver() string {
	cmd := exec.Command("git", "config", "merge.magus.driver")
	cmd.Dir = r.ws.Root()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
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

// firstLine keeps a probe's complaint to the line that names the cause; a failing magus
// prints its diagnostic first and its usage after.
func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}
