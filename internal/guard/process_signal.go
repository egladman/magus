package guard

import (
	"path"
	"slices"

	"github.com/egladman/magus/internal/hint"
)

const denyRuleProcessSignal denyRuleName = "process-signal"

// Signal-list flags print documentation even when followed by a signal name.
// After -- they are operands, so `kill -- -l` still sends a signal.
func signalList(c hint.Invocation) bool {
	for _, arg := range c.Args {
		if arg == "--" {
			break
		}
		if slices.Contains([]string{"-l", "-L", "--list", "--table"}, arg) {
			return true
		}
	}
	return false
}

func processProbe(c hint.Invocation) bool {
	if path.Base(c.Name) != "kill" || signalList(c) {
		return false
	}
	for i, arg := range c.Args {
		if arg == "--" {
			break
		}
		if arg == "-0" || arg == "--signal=0" {
			return true
		}
		if (arg == "-s" || arg == "-n" || arg == "--signal") && i+1 < len(c.Args) {
			return c.Args[i+1] == "0"
		}
	}
	return false
}

func processSignalFires(cmds []hint.Invocation) bool {
	for _, c := range cmds {
		switch path.Base(c.Name) {
		case "kill":
			if len(c.Args) > 0 && !signalList(c) && !processProbe(c) {
				return true
			}
		case "pkill", "killall":
			if len(c.Args) > 0 {
				return true
			}
		}
	}
	return false
}

var processSignalVerdict = ShellVerdict{
	Deny: "a PID or process-name pattern does not prove you own the process; use the host's cancel action for the shell task you started, or ask the person to stop it.",
	Why:  "A broad pkill can terminate another session's fork or the host-managed MCP connection. A slow or locked call is not permission to signal its holder.",
	Rule: denyRule{Name: denyRuleProcessSignal},
}.withRemedy(
	"a PID or name does not prove ownership; cancel your shell task through its host, or ask the person to stop it. Inspect the tracked work once.",
	hint.NextForDenyRemedy(string(denyRuleProcessSignal), hint.Status.Argv(),
		"status reads the live pool once and exits; it does not stop any process."),
)
