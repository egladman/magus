package bindings

import (
	"github.com/egladman/magus/internal/spell"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
)

// DeclareMagusTypes declares every magus\ type into sess, externs included, and binds
// the records' runtime definitions (spell.DeclareMagusTypes). Call it after the magus
// namespace is registered, or magus\ enum cases do not resolve at run time. See
// RegisterSpellSourceModules.
func DeclareMagusTypes(sess *buzz.Session) {
	spell.DeclareMagusTypes(sess, nil)
}
