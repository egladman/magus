package guard

import (
	"path"

	"github.com/egladman/magus/internal/hint"
)

const denyRuleProcessLookup denyRuleName = "process-lookup"

func processLookupFires(cmds []hint.Invocation) bool {
	for _, c := range cmds {
		switch path.Base(c.Name) {
		case "pgrep", "pidof":
			return true
		}
	}
	return false
}

var processLookupVerdict = ShellVerdict{
	Deny: "a name lookup cannot tell your process from another session's; wait on the background-task notification for work you started, or run `magus server status`.",
	Why:  "A name pattern matches every process of that name on the host, including other sessions', and the host refuses the lookup. Work you started announces its own end; `magus job wait <job>` waits on a job.",
	Rule: denyRule{Name: denyRuleProcessLookup},
}.withRemedy(
	"a name lookup cannot tell your process from another session's; wait on the background-task notification for work you started, or read the tracked work once.",
	hint.NextForDenyRemedy(string(denyRuleProcessLookup), hint.ServerStatus.Argv(),
		"status reads the live pool once and exits; `magus job wait <job>` waits on a job."),
)
