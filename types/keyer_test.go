package types

// Both kinds of work a target dispatches to must satisfy Keyer; a compile-time
// assertion is the cheapest way to keep that true as either side changes.
var (
	_ Keyer = SpellOp{}
	_ Keyer = Target{}
)
