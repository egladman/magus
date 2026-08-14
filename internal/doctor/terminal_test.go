package doctor

import (
	"testing"

	"github.com/egladman/magus/internal/config"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckTerminalUnderTest pins the case every CI run and every piped
// invocation takes: no terminal on either end.
//
// It must be OK, not advice. A workspace built in CI is not degraded and the
// reader has nothing to act on - warning there would train people to ignore the
// check, which is the one outcome that makes it worthless.
func TestCheckTerminalUnderTest(t *testing.T) {
	c := (&runner{}).checkTerminal()
	assert.Equal(t, "terminal capabilities", c.Name)
	assert.Equal(t, types.DoctorOK, c.Status, "a pipe is not a fault")
	assert.Contains(t, c.Message, "plain output")
	require.NotEmpty(t, c.Details, "it still reports what it saw, so the reason is legible")
	assert.Contains(t, c.Details[0], "TERM=")
}

// TestCheckTerminalNeverFails is the standing rule for this check: none of the
// capabilities it reports is something a workspace can get wrong, and magus
// degrades on every one of them. Fail is reserved for a workspace nobody's
// taste can rescue.
func TestCheckTerminalNeverFails(t *testing.T) {
	for _, term := range []string{"", "dumb", "xterm-256color", "screen-256color"} {
		t.Setenv("TERM", term)
		assert.NotEqual(t, types.DoctorFail, (&runner{}).checkTerminal().Status, "TERM=%q", term)
	}
}

func TestCheckTerminalReportsNoColorWithoutBlaming(t *testing.T) {
	// NO_COLOR is a preference the reader set. It is worth reporting, because
	// somebody wondering where the colour went should find the answer here, but
	// it is never a fault.
	t.Setenv("NO_COLOR", "1")
	c := (&runner{}).checkTerminal()
	assert.NotEqual(t, types.DoctorFail, c.Status)
}

// TestCheckTerminalReportsTheLogFormatFirst pins the ordering that matters.
//
// The format decides whether there is an interactive surface at all - json and
// text install a structured handler, so a perfectly capable terminal shows no
// band. "Why is there no status line" is answered by the format more often than
// by anything about the terminal, so the check says so and stops rather than
// listing capabilities that cannot be used.
func TestCheckTerminalReportsTheLogFormatFirst(t *testing.T) {
	for _, format := range []string{"json", "text"} {
		r := &runner{opts: options{cfg: config.Config{Log: config.Log{Format: format}}}}
		c := r.checkTerminal()
		assert.Equal(t, types.DoctorOK, c.Status, "a structured format is a choice, not a fault")
		assert.Contains(t, c.Message, format)
		assert.Contains(t, c.Message, "draws no interactive surface")
		require.NotEmpty(t, c.Details)
		assert.Contains(t, c.Details[len(c.Details)-1], "set log.format to pretty",
			"and names the remedy")
	}
}

func TestCheckTerminalNamesWhereTheFormatCameFrom(t *testing.T) {
	// The remedy depends on it: an environment variable overrides magus.yaml, so
	// a reader editing the file would change nothing and not know why.
	t.Setenv("MAGUS_LOG_FORMAT", "json")
	r := &runner{opts: options{cfg: config.Config{Log: config.Log{Format: "json"}}}}
	assert.Contains(t, r.checkTerminal().Details[0], "MAGUS_LOG_FORMAT")

	t.Setenv("MAGUS_LOG_FORMAT", "")
	assert.Contains(t, r.checkTerminal().Details[0], "from config")
}

func TestCheckTerminalDefaultsToPretty(t *testing.T) {
	// An unset format is pretty, so an empty config must not read as "no
	// interactive surface".
	r := &runner{opts: options{cfg: config.Config{}}}
	assert.NotContains(t, r.checkTerminal().Message, "draws no interactive surface")
}
