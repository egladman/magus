package types

import "testing"

// The two units of work must keep identical Key signatures so a future consumer can
// declare one interface over both. Asserted from the test rather than by an exported
// interface: nothing consumes them yet, and Go declares an interface where it is used.
func TestKeySignaturesMatch(t *testing.T) {
	t.Parallel()
	var (
		_ interface{ Key() []string } = SpellOp{}
		_ interface{ Key() []string } = Target{}
	)
}
