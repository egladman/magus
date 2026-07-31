package types

import (
	"testing"

	"github.com/egladman/magus/spells"
)

// The two units of work must keep identical Key signatures so a future consumer can
// declare one interface over both. Asserted from the test rather than by an exported
// interface: nothing consumes them yet, and Go declares an interface where it is used.
//
// It matters more now that they live in separate packages: spells.Op and types.Target
// can no longer drift into each other's shape by accident, and nothing else holds them
// to a signature a single interface could still cover.
func TestKeySignaturesMatch(t *testing.T) {
	t.Parallel()
	var (
		_ interface{ Key() []string } = spells.Op{}
		_ interface{ Key() []string } = Target{}
	)
}
