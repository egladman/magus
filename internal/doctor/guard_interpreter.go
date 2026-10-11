package doctor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// ownBinaryToken is how a committed hook command names the workspace's own binary.
const ownBinaryToken = "./magus"

// configsNamingOwnBinary lists the hook configs whose command runs ./magus.
//
// A hook command names its interpreter literally, chosen when the harness was applied, and
// nothing re-checks it afterwards. A checkout that loses ./magus has every such hook fail
// before a line of glue runs, which is the one breakage no notice arm inside the glue can
// report, and resolving ./magus-then-PATH the way the scripts do grades it healthy.
//
// Reads the descriptors rather than the verified inventory: verification runs the wired
// command, so it goes quiet in precisely the state this looks for.
func configsNamingOwnBinary(ctx context.Context, root string, wired ...string) []string {
	var out []string
	for _, cfg := range agent.HarnessConfigPaths(ctx, root, wired...) {
		body, err := os.ReadFile(cfg)
		if err != nil {
			continue
		}
		if bytes.Contains(body, []byte(ownBinaryToken)) {
			out = append(out, cfg)
		}
	}
	return out
}

// hookBinaryFrom is the magus the hook glue resolves for a call made in dir: the ./magus
// beside the nearest magus.yaml above it (the nearest magusfile.buzz only where no magus.yaml
// exists), else the first magus on PATH, and "" when neither runs.
//
// It mirrors resolveBin in docs/guides/integrations/agents/lib/hook.buzz, because the
// glue is Buzz run by the host and doctor cannot call it. The root is not the project
// directory: console/, docs/ and libs/* each carry a magusfile.buzz and no binary, and the
// binary a call from one of them runs is the question this exists to answer.
func hookBinaryFrom(dir string) string {
	if root := nearestDirWith(dir, "magus.yaml"); root != "" {
		return runnableOrPath(root)
	}
	return runnableOrPath(nearestDirWith(dir, "magusfile.buzz"))
}

// runnableOrPath is root's ./magus when it runs, else the magus on PATH.
func runnableOrPath(root string) string {
	if root != "" {
		candidate := filepath.Join(root, "magus")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate
		}
	}
	found, err := exec.LookPath("magus")
	if err != nil {
		return ""
	}
	return found
}

// nearestDirWith is the nearest directory at or above dir holding the file name, "" when
// none does.
func nearestDirWith(dir, name string) string {
	for at := dir; ; at = filepath.Dir(at) {
		if info, err := os.Stat(filepath.Join(at, name)); err == nil && !info.IsDir() {
			return at
		}
		if filepath.Dir(at) == at {
			return ""
		}
	}
}

// checkGuardBinaryEverywhere is the guard-binary check run from the root and from every
// project directory. The root's answer stands unless it already fails; a worktree with no
// ./magus fails before the PATH fallback can call it healthy; and any project directory
// whose hook would run a different build, or none, fails the check too.
func (r *runner) checkGuardBinaryEverywhere(projects []*types.Project) types.Check {
	root := r.ws.Root()
	if info, err := os.Stat(filepath.Join(root, "magus")); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		if wt, ok := worktreeWithoutBinary(root); ok {
			return wt
		}
	}
	got := r.checkGuardBinary()
	if got.Status == types.CheckFail {
		return got
	}
	if skew, ok := projectBinarySkew(r.runCtx(), root, projects, r.ownVersion()); ok {
		return skew
	}
	return got
}

// worktreeWithoutBinary fails a linked worktree that holds no ./magus. Its hooks then judge
// with whatever magus is on PATH, which is the build most likely to be older than the tree
// and fail to load it; and a checkout that cannot load its policy is one the guard can only
// advise on. A main checkout stays an ordinary state to report elsewhere: a build machine
// legitimately has no ./magus yet.
func worktreeWithoutBinary(root string) (types.Check, bool) {
	if info, err := os.Lstat(filepath.Join(root, ".git")); err != nil || info.IsDir() {
		return types.Check{}, false
	}
	details := []string{
		"its guard hooks judge with the magus on PATH, which may not load this tree",
		"the main session builds one ./magus per base and places a copy here",
	}
	if info, err := os.Stat(filepath.Join(root, "cmd", "magus")); err == nil && info.IsDir() {
		details = append(details,
			"place it: <main checkout>/magus buzz hack/dev/bootstrap-worktree.buzz -- --job <id> --from <main checkout>",
			"on your own: build one with "+hint.Run.With("build", ".")+", or bootstrap it with the command the guard prints")
	}
	return types.Check{
		Name:    guardBinaryCheck,
		Status:  types.CheckFail,
		Message: "this worktree has no ./magus",
		Details: details,
	}, true
}

// projectBinarySkew resolves the hook binary from every project directory, not only the
// root, and fails when any call site would run a magus that is not this build or does
// not answer at all. A project that resolves to the PATH magus while the root holds a
// current ./magus is the failure this finds: the hook there judges with a build the
// checked tree never saw.
//
// Returns false when every directory resolves to a binary that matches, or this binary's
// own version is unknown.
func projectBinarySkew(ctx context.Context, root string, projects []*types.Project, own string) (types.Check, bool) {
	checked := map[string]bool{}
	for _, p := range projects {
		if p == nil || p.Dir == "" {
			continue
		}
		bin := hookBinaryFrom(p.Dir)
		if bin == "" {
			return types.Check{
				Name:    guardBinaryCheck,
				Status:  types.CheckFail,
				Message: "no magus resolves for a hook run in " + p.Dir + ", so no guard rule judges a call made there",
				Details: []string{"build one: " + hint.Run.With("build", ".")},
			}, true
		}
		if checked[bin] || bin == filepath.Join(root, "magus") {
			continue
		}
		checked[bin] = true
		if skew, ok := interpreterSkew(ctx, bin, own); ok {
			skew.Details = append([]string{"resolved from the project directory " + p.Dir}, skew.Details...)
			return skew, true
		}
	}
	return types.Check{}, false
}

// interpreterSkew fails when the magus a hook would run as its interpreter is a different
// build from this one, or cannot say which build it is.
//
// The interpreter is the half the verdict binary's freshness says nothing about: a stale
// one never reaches the glue that would name it, so the hook hangs or errors and the host
// runs the call unjudged. A FAIL, because the wiring is not honored as written.
//
// Returns false when there is nothing to compare: this binary's own version is unknown.
func interpreterSkew(ctx context.Context, bin, own string) (types.Check, bool) {
	if own == "" {
		return types.Check{}, false
	}
	// The hook's own budget, not a tighter one: an interpreter that answers within it runs
	// every hook, so failing it sooner reports a hang that is only a loaded machine.
	probeCtx, cancel := context.WithTimeout(ctx, agent.ProbeTimeout(ctx))
	defer cancel()
	out, err := exec.CommandContext(probeCtx, bin, "version").Output()
	// `magus <version> (<commit>) built <date>`, the first line every release has printed.
	fields := strings.Fields(strings.SplitN(string(out), "\n", 2)[0])
	if err != nil || len(fields) < 2 || fields[0] != "magus" {
		reason := "printed no version"
		if err != nil {
			reason = err.Error()
		}
		return types.Check{
			Name:    guardBinaryCheck,
			Status:  types.CheckFail,
			Message: "the hook interpreter " + bin + " did not answer `version` (" + reason + "), so no guard hook it runs can render a verdict",
			Details: []string{"rebuild: " + hint.Run.With("build", ".")},
		}, true
	}
	if fields[1] == own {
		return types.Check{}, false
	}
	return types.Check{
		Name:   guardBinaryCheck,
		Status: types.CheckFail,
		Message: fmt.Sprintf("the hook interpreter is %s (%s) and this magus is %s, so hooks run a build other than the one checked here",
			bin, fields[1], own),
		Details: []string{
			"a hook runs the session root's ./magus when it is executable, else the first magus on PATH",
			"rebuild ./magus (" + hint.Run.With("build", ".") + "), or put the right magus first on PATH; a live session keeps the binary it started with",
		},
	}, true
}
