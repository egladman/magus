package fixture

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/agent"
)

// WireHarness makes d resolve as a harness spell named d.ID until the test ends, then
// puts back the loader it replaced. Any other id misses, so a test that wires one
// harness never sees another by accident.
func WireHarness(tb testing.TB, d agent.HarnessDescriptor) {
	tb.Helper()
	previous := agent.RegisterHarnessSpellLoader(func(_ context.Context, id string) (agent.HarnessDescriptor, string, bool, error) {
		if id != d.ID {
			return agent.HarnessDescriptor{}, "", false, nil
		}
		return d, "spell:" + d.ID, true, nil
	})
	tb.Cleanup(func() { agent.RegisterHarnessSpellLoader(previous) })
}
