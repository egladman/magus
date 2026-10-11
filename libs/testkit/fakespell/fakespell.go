package fakespell

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
)

var seq atomic.Uint64

// Register registers a spell built from opts in project.DefaultSpellRegistry under
// name plus a suffix unique to the process, and returns the registered name for
// magus.WithSpell. The spell is unregistered when the test ends.
//
// The registry is process-wide, so the suffix is what lets parallel tests, or one
// test run with -count, register the same name without colliding. opts are the spells
// package's own: typically spells.WithTargets and a spells.WithInvoker that records
// what ran.
func Register(tb testing.TB, name string, opts ...spells.Option) string {
	tb.Helper()
	unique := fmt.Sprintf("%s-%d", name, seq.Add(1))
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(unique, opts...))
	tb.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(unique) })
	return unique
}
