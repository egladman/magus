package magus

import (
	"reflect"

	"github.com/egladman/magus/internal/contract"
	"github.com/egladman/magus/types"
)

// explainStale names the relink for a failed load of a checkout of magus
// whose contract differs from this binary's. Any other failure, and every
// failure where the two agree, is the tree's own and returns as it came.
func explainStale(err error, root string) error {
	skew, cerr := contract.Check(root)
	if cerr != nil || !skew.Differs() {
		return err
	}
	return types.WrapDiagnostic(types.WorkspaceNeedsNewerMagus, err, "%s\n%s", err, skew.Diagnostic().Error())
}

// ownModule is the module this package was compiled from, read off a type so a renamed fork
// still recognizes its own tree. Only the binary-versus-own-source skew check reads it.
var ownModule = reflect.TypeFor[Magus]().PkgPath()
