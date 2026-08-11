package buzz

import "github.com/egladman/magus/libs/diagnostics"

// This file is gopherbuzz's own diagnostic-code namespace: the BZZ#### family. It uses the SAME shared
// mechanism as magus (github.com/egladman/magus/libs/diagnostics) but declares an ENTIRELY SEPARATE catalog - no
// code is shared with magus's MGS codes, and the docs live in gopherbuzz's own tree, not magus's. The
// codes give a buzz author the same lookupable, documented errors magus targets get. gopherbuzz's error
// TEXT already differs from upstream buzz (only interpreter BEHAVIOR must match), so the codes render
// INLINE in the error string.
//
// Ranges: 1000 = type-check errors (checker.go), 2000 = session/runtime errors (imports,
// fibers), 3000 = warnings (parser.go).

// bzzDocsBase is where BZZ code docs live - inside gopherbuzz's OWN source tree, kept separate from
// magus's docs/codes.
const bzzDocsBase = "https://github.com/egladman/magus/blob/main/libs/gopherbuzz/docs/codes/"

// bzz is gopherbuzz's diagnostic domain: every BZZ code maps to a doc page under bzzDocsBase.
var bzz = diagnostics.New(func(c diagnostics.Code) string { return bzzDocsBase + string(c) + ".md" })

// BZZ diagnostic codes. Each names a distinct, documented buzz error kind. There is deliberately NO
// catch-all code: a type error the checker has not classified carries NO code at all (just its message),
// matching Rust and TypeScript, where an error either earns a specific code or has none. A code is a
// lookup handle for a documented failure, not a completeness checkbox.
const (
	// Type-check errors (checker.go).
	UndefinedName    diagnostics.Code = "BZZ1001" // reference to a variable or function that is not in scope
	UndefinedType    diagnostics.Code = "BZZ1002" // reference to a type name that is not defined
	NonBoolCondition diagnostics.Code = "BZZ1003" // an if/while/for condition whose type is not bool
	ArgumentError    diagnostics.Code = "BZZ1004" // a call with the wrong count, an unknown/duplicate name, or a missing argument
	TypeMismatch     diagnostics.Code = "BZZ1005" // an assignment, return, yield, or operand whose type does not match what is expected
	UnhandledRaise   diagnostics.Code = "BZZ1006" // a call to a !> function from a caller that neither declares !> nor catches it

	// Session / runtime errors (session.go).
	UnresolvedImport diagnostics.Code = "BZZ2001" // an import that cannot be resolved to a module or file
	FiberMisuse      diagnostics.Code = "BZZ2002" // resume/resolve called wrong: not a fiber, missing argument, or a running fiber

	// Warnings (parser.go). Unlike every code above, a warning never fails Exec/Compile -
	// see Severity.
	UnusedImport diagnostics.Code = "BZZ3001" // an import whose namespace binding is never referenced
)

// allBZZCodes enumerates every BZZ code, in ascending order. Kept in sync with the const block above by
// TestAllBZZCodesEnumerated; it is the source of truth for the doc-coverage drift test.
var allBZZCodes = []diagnostics.Code{
	UndefinedName, UndefinedType, NonBoolCondition, ArgumentError, TypeMismatch, UnhandledRaise,
	UnresolvedImport, FiberMisuse,
	UnusedImport,
}

// Severity classifies a BZZ diagnostic. The zero value is SeverityError, so every
// diagnostic built before Severity existed - and every one the checker still builds
// without setting it explicitly - keeps its current meaning: it fails Exec/Compile
// exactly as before this type was introduced. Only a diagnostic that opts in
// (currently just UnusedImport) is a warning, which Exec/Compile must never fail on.
type Severity int

const (
	SeverityError Severity = iota
	SeverityWarning
)

// String renders the severity the way a diagnostic message prefixes it ("warning: ");
// SeverityError renders as "" since an error carries no prefix today (see typeError.Error).
func (sv Severity) String() string {
	if sv == SeverityWarning {
		return "warning"
	}
	return "error"
}
