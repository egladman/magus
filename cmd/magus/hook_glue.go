package main

import (
	"slices"

	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/termkit"
)

// hookGlueShaped reports `magus buzz -s` with stdin not a terminal: the shape every
// shipped hook wiring runs.
func hookGlueShaped(sub string, subArgs []string) bool {
	if sub != "buzz" || termkit.StdinIsTerminal() {
		return false
	}
	own, _ := splitForwarded(subArgs)
	return slices.Contains(own, "-s")
}

// splitForwarded splits buzz's own args from the script's, after `--`.
func splitForwarded(subArgs []string) (own, forwarded []string) {
	at := slices.Index(subArgs, "--")
	if at < 0 {
		return subArgs, nil
	}
	return subArgs[:at], subArgs[at+1:]
}

// answerFailedGlue turns the exit of a hook glue that failed with cause into the
// guard's answer to the event it was handed, since a host runs the call of a hook that
// exits anything but 2, and the reason to print. A script that was handed no hook event
// keeps code and has no reason.
func answerFailedGlue(stdin *guard.HookStdin, rootFlag string, subArgs []string, cause error, code int) (int, string) {
	_, forwarded := splitForwarded(subArgs)
	root, _ := workspace.FindRoot(rootFlag) // a root not found names the release as the repair
	answer, reason, isHook := guard.FailedGlue(root, stdin.Event(), slices.Contains(forwarded, "--agent-name"), cause)
	if !isHook {
		return code, ""
	}
	return answer.ExitCode(), reason
}
