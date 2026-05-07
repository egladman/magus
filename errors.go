package magus

import "github.com/egladman/magus/types"

// Sentinel errors re-exported from magus/types.
var (
	ErrAffectedFallback   = types.ErrAffectedFallback
	ErrDiag               = types.ErrDiag
	ErrNoCache            = types.ErrNoCache
	ErrNotFound           = types.ErrNotFound
	ErrSpellNameRequired  = types.ErrSpellNameRequired
	ErrSpellNotRegistered = types.ErrSpellNotRegistered
	ErrUnknownProject     = types.ErrUnknownProject
	ErrUnregisteredDep    = types.ErrUnregisteredDep
	ErrVCSUnknown         = types.ErrVCSUnknown
	ErrVCSUnsupported     = types.ErrVCSUnsupported
)

// Error types re-exported from magus/types.
type (
	DiagnosticCode       = types.DiagnosticCode
	DiagnosticError      = types.DiagnosticError
	SpellError           = types.SpellError
	SpellErrors          = types.SpellErrors
	UnregisteredDep      = types.UnregisteredDep
	UnregisteredDepError = types.UnregisteredDepError
)
