package commands

import (
	"errors"

	"github.com/egladman/magus/libs/diagnostics"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/types"
)

// RegisterDiagnosticCatalogs reserves each shipped catalog's prefix; a clash is a build defect.
func RegisterDiagnosticCatalogs() error {
	return errors.Join(diagnostics.RegisterCatalog(types.DiagnosticOwnerMagus, types.AllDiagnosticCodes()),
		diagnostics.RegisterCatalog("gopherbuzz", buzz.Codes()))
}
