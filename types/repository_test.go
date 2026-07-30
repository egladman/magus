package types

// Both kinds of work a target dispatches to must satisfy CacheRepository; a compile-time
// assertion is the cheapest way to keep that true as either side changes.
var (
	_ CacheRepository = SpellOp{}
	_ CacheRepository = Target{}
)
