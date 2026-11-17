package guard

import (
	"fmt"
	"io"
	"strings"

	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

// guardDenyExitCode is what a denied command exits with.
//
// A hook that reports a deny and exits 0 blocks NOTHING: the host sees success
// and runs the command anyway, so the guard looks enforced and is not. 2 rather
// than 1 is what the dominant host reads as "block and show the reason to the
// model". The collision with the usage code is harmless: a guard that could not
// parse its input has not judged the command either.
const guardDenyExitCode = 2

// verdictExit turns a deny or an ask into a blocking exit. Applies to every format: an
// `-o json` caller with a zero status would be told the same lie in a different
// shape. An ask blocks too because the exit status cannot carry the person's approval: a
// glue that reads only the status must stop, and one that renders the host's prompt reads
// the decision off stdout.
//
// Stderr gets the reason's first line, and only when stdout does not already carry it as
// prose, i.e. every format but text. A host shows a blocking hook's stderr to the person
// and the agent alike, so it gets the verdict alone; stdout carries the rest.
func verdictExit(errOut io.Writer, opts output.Options, verdict verdict.Judgment) error {
	if verdict.Decision != "deny" && verdict.Decision != "ask" {
		return nil
	}
	if opts.Format != output.Text {
		headline, _, _ := strings.Cut(verdict.Reason, "\n")
		fmt.Fprintln(errOut, headline)
	}
	return exit.Reported(guardDenyExitCode)
}

// writeVerdict renders a verdict through the standard output arm.
func (e HookEnv) writeVerdict(out io.Writer, opts output.Options, verdict verdict.Judgment) error {
	if opts.Format == output.Text {
		switch verdict.Decision {
		case "deny":
			fmt.Fprintf(out, "%s %s\n", decisionLabel("deny", verdict.Rule), verdict.Reason)
		case "ask":
			fmt.Fprintf(out, "%s %s\n", decisionLabel("ask", verdict.Rule), verdict.Reason)
		case "advise":
			fmt.Fprintf(out, "%s %s\n", decisionLabel("advise", verdict.Rule), verdict.Context)
		default:
			fmt.Fprintln(out, "defer")
		}
		return nil
	}
	return e.Emit(out, opts, string(verdict.Decision), verdict)
}

// decisionLabel opens a text verdict with the decision and, where the rule can identify
// itself, its name: `deny [stage-all]`.
//
// The name was reachable only through `-o json` before this, which meant a person could
// read a refusal and still have nothing to look up, grep a trail for, or cite when
// reporting it as a false positive. Every other verdict magus reaches carries a code a
// reader can take somewhere; this was the exception.
//
// Bracketed rather than appended so it survives a line-wrap next to the decision it
// qualifies, and omitted entirely when the rule is anonymous: an empty `[]` would promise
// an identifier that does not exist.
func decisionLabel(decision, rule string) string {
	if rule == "" {
		return decision + ":"
	}
	return fmt.Sprintf("%s [%s]:", decision, rule)
}

// guardInputLimit bounds the payload one hook call may carry.
//
// An MCP params object is caller-controlled and can be megabytes, the raw-event wiring
// forwards the WHOLE event rather than one extracted command string, and the template
// holds a second copy of it under the host's hook timeout. Overflow is a read FAILURE
// rather than a truncation, for the reason readGuardInput gives: a truncated payload is
// exactly the "not the command the guard was shown" case.
const guardInputLimit = 1 << 20

// readGuardInput reports the payload, and the read failure separately from an empty one.
// "No input" is a host that sent nothing and "the read broke" is a payload magus was
// supposed to receive, and only the second one means the command about to run is not the
// command the guard was shown.
func readGuardInput(in io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(in, guardInputLimit+1))
	if err != nil {
		return "", err
	}
	if len(b) > guardInputLimit {
		return "", fmt.Errorf("the payload is larger than the %d-byte limit, so it was not read whole", guardInputLimit)
	}
	return strings.TrimSpace(string(b)), nil
}
