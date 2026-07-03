// Package ward runs "kind-coherence" checks on a resolved op: it verifies that
// the op's argv does not contradict the op's declared kind. The two flagship
// violations are mirror images of one bug (the argv lies about the kind):
//
//   - a SERVICE op that detaches (docker run -d): the process forks away from
//     magus, so foreground blocking, stdout capture, readiness probing, and the
//     stop command all become meaningless.
//   - a COMMAND op that never exits (a --watch flag): a run-to-completion op that
//     never terminates hangs the run.
//
// These are self-contradictions, not style preferences, so they are ERRORS with no
// flag-level suppression: the fix is to change the op's kind (a detached service is
// a command op; a watch is a service op), not to silence the check. Because a bare
// flag like -d is tool-specific (dnsmasq -d means the opposite - stay in the
// foreground), each check scopes itself to the tools where the flag has the
// asserted meaning, keyed off the command's bin rather than matched universally.
//
// Today the checks are built in and bin-scoped; the package is the seam where
// spell-declared wards would register, since a spell knows its own tool.
package ward

import (
	"strings"

	"github.com/egladman/magus/internal/serviceident"
	"github.com/egladman/magus/types"
)

// Check returns the kind-coherence violations for a resolved op (empty when the op
// is coherent). opName is used only to phrase the diagnostic.
func Check(opName string, op types.SpellOp) []*types.DiagnosticError {
	var out []*types.DiagnosticError
	if op.IsService() {
		if flag, ok := detachFlag(op.Command); ok {
			out = append(out, types.DiagnosticErrorf(types.ServiceOpDetached,
				"service op %q detaches with %q: magus forks a service in the foreground and "+
					"supervises it, so detaching breaks stdout capture, readiness, and stop. "+
					"Drop the detach flag, or make this a command op if you really want it detached.",
				opName, flag))
		}
	} else {
		if flag, ok := watchFlag(op.Command); ok {
			out = append(out, types.DiagnosticErrorf(types.CommandOpNeverExits,
				"command op %q runs a watcher with %q: a command op runs to completion, so a "+
					"never-exiting watch process hangs the run. Make this a service op instead.",
				opName, flag))
		}
	}
	return out
}

// detachFlag reports the detach flag present in a container run command, scoped to
// container runtimes so a bare -d in an unrelated tool (dnsmasq -d = foreground) is
// not misread. It recognizes -d, --detach, --detach=true, and combined short-flag
// blocks like -itd.
func detachFlag(cmd types.Command) (string, bool) {
	if !serviceident.IsContainerRuntime(cmd.Bin) {
		return "", false
	}
	for _, a := range cmd.Args {
		switch {
		case a == "-d", a == "--detach", a == "--detach=true":
			return a, true
		case strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && len(a) > 1 && strings.ContainsRune(a[1:], 'd'):
			return a, true
		}
	}
	return "", false
}

// watchTools are the tools whose --watch (or -w) means a long-running watch loop.
// Scoped so a --watch that means something else elsewhere is not misread, and so
// the flag is checked against the effective tool even when run via a runner.
var watchTools = map[string]bool{
	"tsc": true, "vitest": true, "jest": true, "vite": true,
	"webpack": true, "rollup": true, "esbuild": true, "cargo-watch": true,
}

// watchFlag reports a watch flag on a command whose tool runs a watch loop. It
// looks at the bin and, for runners like npx/pnpm/yarn/bunx, the first tool
// argument, then requires an explicit --watch (the unambiguous long form).
func watchFlag(cmd types.Command) (string, bool) {
	if !invokesWatchTool(cmd) {
		return "", false
	}
	for _, a := range cmd.Args {
		if a == "--watch" || strings.HasPrefix(a, "--watch=") || a == "-w" {
			return a, true
		}
	}
	return "", false
}

// invokesWatchTool reports whether the command's effective tool is a known watcher,
// looking through common runners (npx, pnpm, yarn, bunx) to their first argument.
func invokesWatchTool(cmd types.Command) bool {
	bin := serviceident.Basename(cmd.Bin)
	if watchTools[bin] {
		return true
	}
	switch bin {
	case "npx", "bunx", "pnpm", "yarn", "bun", "deno":
		for _, a := range cmd.Args {
			if strings.HasPrefix(a, "-") {
				continue // skip runner flags to reach the tool name
			}
			return watchTools[a]
		}
	}
	return false
}
