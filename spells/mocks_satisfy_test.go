// Nothing else forces these mocks to actually satisfy the interfaces they were
// generated from. mockery's testify template emits a plain struct with methods and
// no `var _ Iface = (*Mock)(nil)` line, so a mock that has gone stale - an interface
// gained a method, nobody re-ran `magus run generate` - still COMPILES. It would fail
// only at a use site, and since these are published for downstream consumers rather
// than used by our own tests, the first build to break could well be someone else's.
//
// The assertions live here rather than beside the mocks because a gen/ dir is
// generated-only; a hand-written file inside one would be erased by the next run.

package spells_test

import (
	"github.com/egladman/magus/spells"
	mocks "github.com/egladman/magus/spells/gen/mocks"
)

var (
	_ spells.Driver = (*mocks.MockDriver)(nil)
)
