//go:build e2e

package harness

import (
	"testing"

	"github.com/egladman/magus/libs/testkit/e2e"
)

// askPrompt asks for the person-only command by name, so the model's choice of
// tool cannot change which hook fires.
const askPrompt = `This is an automated test of an approval prompt. Run this exact shell command once with your shell tool: ` + askCommand + `
If it is refused or declined, do not retry it and do not run anything else. Finally reply with one line listing every code word (shaped like E2E-ASK-XXXXXXXX) that a refusal showed you.`

// askWant is what a case proves besides the rule every case holds: the command
// never runs on its own, since nobody approves it here.
type askWant string

const (
	// askPrompts proves the host raised its own permission prompt.
	askPrompts askWant = "prompts"
	// askRefuses proves a deny, with the person's text in the host's output.
	askRefuses askWant = "refuses"
	// askHolds is for a headless host that cannot show whether it prompted.
	askHolds askWant = "holds"
	// askObserves settles an unknown about the host: a run fails nothing.
	askObserves askWant = "observes"
)

// askCase is one permission mode, or one allow rule, on one host.
type askCase struct {
	name  string
	want  askWant
	args  []string
	env   []string
	setup func(t *testing.T, w *workspace)
	check func(t *testing.T, w *workspace)
}

// askRun is what an ask case left behind.
type askRun struct {
	e2e.Result
	// prompted is set only by a driver that can see the host's prompt.
	prompted bool
}

// askHeadless runs a host that takes the prompt as an argument. Nothing shows
// a prompt there, so a case proves only that the command did not run.
func askHeadless(t *testing.T, w *workspace, c askCase, bin string, args []string) askRun {
	t.Helper()
	return askRun{Result: w.runHost(t, c.name, bin, args, c.env...)}
}
