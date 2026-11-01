//go:build e2e

package harness

import (
	"fmt"
	"os"
	"testing"

	"github.com/egladman/magus/libs/testkit/e2e"
)

// hostDriver finds, isolates, wires and drives one agent host headless.
type hostDriver struct {
	id     string // the harness spell and its --agent-name
	binary string
	model  string
	// login names the logins the host reads, for a refused turn's skip.
	login string
	// briefTurns replaces the brief conversation for a host that reaches
	// the brief only through a compaction.
	briefTurns []string

	install e2e.Binary
	// unauthenticated is the host's own words when it refused a turn for
	// want of a login. Every later scenario skips, since nothing logs in.
	unauthenticated string

	// env isolates the host's own config and state from the user's.
	env func(t *testing.T, w *workspace) []string
	// wire adds what magus leaves to the user, such as the MCP entry.
	wire func(t *testing.T, w *workspace)
	// turn sends one prompt into conv and returns the printed reply text.
	turn func(t *testing.T, w *workspace, conv *conversation, prompt string) (string, e2e.Result)
	// askTurn sends askPrompt under one ask case's mode.
	askTurn func(t *testing.T, w *workspace, c askCase) askRun
}

func envModel(name, fallback string) string {
	if m := os.Getenv(fmt.Sprintf("AGENT_HARNESS_%s_MODEL", name)); m != "" {
		return m
	}
	return fallback
}

// locate resolves the binary from AGENT_HARNESS_<HOST>_BIN, then candidates,
// then PATH.
func (h *hostDriver) locate(envName string, candidates ...e2e.Candidate) {
	variable := fmt.Sprintf("AGENT_HARNESS_%s_BIN", envName)
	first := e2e.Candidate{Path: os.Getenv(variable), Source: variable}
	h.install = e2e.LocateBinary(h.binary, scrubbedEnv(), append([]e2e.Candidate{first}, candidates...)...)
}
