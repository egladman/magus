// Package spells is everything a spell is: the language/runtime adapters magus
// builds, tests, lints and formats projects with, and the types describing them.
// The directory holds both halves - the built-in spell sources (.buzz) that ship
// embedded in the binary, and the Go types the engine speaks about any spell.
//
// There used to be three packages spelling "spell" and three representations of
// one: a live driver in types, a decoded Descriptor in internal/spellruntime, and a
// describe-time view in types again, the last two carrying the same facts reached
// from opposite directions. Collapsing them here is what lets the names drop their
// prefix - the package carries the noun, so it is spells.Op and spells.Driver
// rather than types.SpellOp and types.SpellDriver.
//
// The dependency runs one way: spells imports nothing from types, and types
// imports spells. That is deliberate and load-bearing. Descriptor.Ops is a
// map[string]Op, so if the describe-time views had stayed in types while Op lived
// here, the two packages would import each other and neither would compile.
package spells
