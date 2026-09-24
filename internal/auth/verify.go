package auth

import (
	"crypto/subtle"

	"github.com/egladman/magus/types"
)

// Verify authenticates presented on the daemon's LOOPBACK listener and returns the credential
// it is, grant included. It routes by class, so each store is consulted only for its own
// class: mgo_ against the operator file, mgs_ against the token store, and mgl_ and mgx_
// refused outright, because a share token authenticates only on its own listener and an
// exchange code is never a bearer. It does no authorization; the caller compares the Grant
// with its route's Need.
//
// Both stores are read on every call (the token store through its change-keyed cache), so a
// rotate, mint or revoke takes effect without a restart, and a load error fails closed. A
// store that fails to load (a retired connectors.d, say) refuses stored tokens only: the
// operator token never opens it.
func Verify(presented string) (types.Credential, bool) {
	class, ok := classOf(presented)
	if !ok {
		return types.Credential{}, false
	}
	switch class {
	case types.ClassOperator:
		tok, err := LoadOperator()
		if err != nil {
			return types.Credential{}, false
		}
		if subtle.ConstantTimeCompare([]byte(digest(presented)), []byte(digest(tok))) != 1 {
			return types.Credential{}, false
		}
		return operatorCredential(tok), true
	case types.ClassStored:
		dir, err := StoreDir()
		if err != nil {
			return types.Credential{}, false
		}
		store, err := LoadStore(dir)
		if err != nil {
			return types.Credential{}, false
		}
		t, ok := store.Lookup(presented)
		if !ok {
			return types.Credential{}, false
		}
		return t.Credential(), true
	}
	return types.Credential{}, false
}
