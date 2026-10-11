package register

import (
	"fmt"
	"sync/atomic"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
)

var seq atomic.Uint64

// Spell registers a spell built from opts in project.DefaultSpellRegistry under
// name plus a suffix unique to the process, and returns the registered name for
// magus.WithSpell. The spell is unregistered when the test ends.
//
// The registry is process-wide. The suffix only keeps two registrations of the same
// name apart (parallel tests, or one test run with -count); the spell itself is
// visible to every test in the process while it is registered. A parallel test that
// lists or looks up spells sees another test's spell, so a test that does either must
// not run in parallel with one that registers.
//
// opts are the spells package's own: typically spells.WithTargets and a
// spells.WithInvoker that records what ran.
func Spell(tb testkit.TB, name string, opts ...spells.Option) string {
	tb.Helper()
	unique := fmt.Sprintf("%s-%d", name, seq.Add(1))
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(unique, opts...))
	tb.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(unique) })
	return unique
}
