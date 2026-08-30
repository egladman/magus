package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/types"
)

// allowLabel derives a dot-free config-key segment from a denied path or binary,
// so it can name a `sandbox.allow.<label>` entry. Falls back to "entry".
func allowLabel(target string) string {
	var b strings.Builder
	for _, r := range filepath.Base(target) {
		if r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "entry"
	}
	return b.String()
}

// denyHint renders the `magus config set` command(s) that allow a sandbox-denied
// operation on target (a path, or a resolved binary for exec). mode is "ro"
// (read/exec) or "rw" (write); rw needs the extra mode command.
func denyHint(mode, target string) string {
	label := allowLabel(target)
	cmd := fmt.Sprintf("sandbox blocked access to %s; allow it with:\n"+
		"        magus config set key=sandbox.allow.%s.path,value=%s", target, label, target)
	if mode == "rw" {
		cmd += fmt.Sprintf("\n        magus config set key=sandbox.allow.%s.mode,value=rw", label)
	}
	return cmd
}

// EmitDenyHint prints the "allow it with" remedy via the interactive hint channel
// (a no-op when hints are disabled). Call it at a sandbox denial site, before
// returning the diagnostic error, while the path/command is still typed and in
// scope — it doesn't survive being raised across a script VM, so a central
// handler could not reconstruct the target.
func EmitDenyHint(mode, target string) {
	interactive.Emit(os.Stderr, denyHint(mode, target))
}

// shimMarker pairs a PATH-shim runtime manager with the env var it reads at
// invocation time and the PATH segment its shim directory adds, per
// docs/reference/codes/sandbox/MGS2006.md. direnv is not listed: it has no
// shim directory on PATH (it hooks the shell prompt instead), so neither the
// name nor the path-segment marker this heuristic keys on applies to it.
var shimMarkers = []struct {
	manager     string
	envVar      string
	pathSegment string
}{
	{manager: "mise", envVar: "MISE_DATA_DIR", pathSegment: "/mise/shims"},
	{manager: "asdf", envVar: "ASDF_DIR", pathSegment: "/.asdf/shims"},
}

// detectShimSuspect reports the PATH-shim manager whose shim directory is still on
// PATH while the var it needs to resolve a tool version was dropped by policy - the
// combination MGS2006 names, since the shim binary stays reachable and fails
// silently (falling back to a system tool) instead of erroring loudly. ok is false
// when policy is nil (sandbox off) or neither marker matches.
func detectShimSuspect(policy *Policy) (manager, envVar string, ok bool) {
	if policy == nil {
		return "", "", false
	}
	var path string
	for _, kv := range policy.BaseEnv {
		if name, val, found := strings.Cut(kv, "="); found && name == "PATH" {
			path = val
			break
		}
	}
	for _, m := range shimMarkers {
		if strings.Contains(path, m.pathSegment) && slices.Contains(policy.EnvDropped, m.envVar) {
			return m.manager, m.envVar, true
		}
	}
	return "", "", false
}

// shimHint renders the MGS2006 message: manager's shim directory is on PATH but
// envVar, the var it reads to pick a tool version, was stripped from the child.
func shimHint(cmd, manager, envVar string) string {
	return fmt.Sprintf("%s\n  cmd=%s missing_var=%s",
		types.FormatDiagnostic(types.PathShimSuspected,
			fmt.Sprintf("%s shims appear stripped from PATH; the build is using system tools instead", manager)),
		cmd, envVar)
}

// EmitShimHint prints the MGS2006 hint when policy suggests a PATH-shim manager
// (mise, asdf) lost the var it needs while its shim directory is still on PATH (a
// no-op when nothing matches, or hints are disabled). Call it at the same site
// RecordEnvDropped runs, while cmd is still in scope.
func EmitShimHint(cmd string, policy *Policy) {
	manager, envVar, ok := detectShimSuspect(policy)
	if !ok {
		return
	}
	interactive.Emit(os.Stderr, shimHint(cmd, manager, envVar))
}
