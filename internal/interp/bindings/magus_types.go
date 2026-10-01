package bindings

import (
	"github.com/egladman/magus/internal/spell"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	vm "github.com/egladman/magus/libs/gopherbuzz/vm"
)

// DeclareMagusTypes declares every magus\ type into sess, externs included, and binds
// the records' runtime definitions (spell.DeclareMagusTypes). Call it after the magus
// namespace is registered, or magus\ enum cases do not resolve at run time. See
// RegisterSpellSourceModules.
func DeclareMagusTypes(sess *buzz.Session) {
	spell.DeclareMagusTypes(sess, nil)
}

// RegisterMagusRecordTypes makes `import "magus"` resolve to a module with no functions,
// declaring only its records and enums, for a host that runs Buzz which imports magus to
// name those types (magus/figure) and has no workspace to back a call. A magus\ function
// call fails the type check rather than at run time.
func RegisterMagusRecordTypes(sess *buzz.Session) {
	sess.SetNativeModule("magus", vm.NewMap())
	spell.DeclareMagusTypes(sess, func(string) bool { return false })
}
