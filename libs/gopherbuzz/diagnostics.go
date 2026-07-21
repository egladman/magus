package buzz

import "github.com/egladman/magus/libs/diag"

// This file is gopherbuzz's own diagnostic-code namespace: the BZZ#### family. It uses the SAME shared
// mechanism as magus (github.com/egladman/magus/libs/diag) but declares an ENTIRELY SEPARATE catalog - no
// code is shared with magus's MGS codes, and the docs live in gopherbuzz's own tree, not magus's. The
// codes give a buzz author the same lookupable, documented errors magus targets get. gopherbuzz's error
// TEXT already differs from upstream buzz (only interpreter BEHAVIOR must match), so the codes render
// INLINE in the error string.
//
// Ranges: 1000 = type-check errors (checker.go), 2000 = session/runtime errors (imports, fibers).

// bzzDocsBase is where BZZ code docs live - inside gopherbuzz's OWN source tree, kept separate from
// magus's docs/codes.
const bzzDocsBase = "https://github.com/egladman/magus/blob/main/libs/gopherbuzz/docs/codes/"

// bzz is gopherbuzz's diagnostic domain: every BZZ code maps to a doc page under bzzDocsBase.
var bzz = diag.New(func(c diag.Code) string { return bzzDocsBase + string(c) + ".md" })

// BZZ diagnostic codes. Each names a distinct, documented buzz error kind. There is deliberately NO
// catch-all code: a type error the checker has not classified carries NO code at all (just its message),
// matching Rust and TypeScript, where an error either earns a specific code or has none. A code is a
// lookup handle for a documented failure, not a completeness checkbox.
const (
	// Type-check errors (checker.go).
	UndefinedName    diag.Code = "BZZ1001" // reference to a variable or function that is not in scope
	UndefinedType    diag.Code = "BZZ1002" // reference to a type name that is not defined
	NonBoolCondition diag.Code = "BZZ1003" // an if/while/for condition whose type is not bool
	ArgumentError    diag.Code = "BZZ1004" // a call with the wrong count, an unknown/duplicate name, or a missing argument
	TypeMismatch     diag.Code = "BZZ1005" // an assignment, return, yield, or operand whose type does not match what is expected

	// Session / runtime errors (session.go).
	UnresolvedImport diag.Code = "BZZ2001" // an import that cannot be resolved to a module or file
	FiberMisuse      diag.Code = "BZZ2002" // resume/resolve called wrong: not a fiber, missing argument, or a running fiber
)

// allBZZCodes enumerates every BZZ code, in ascending order. Kept in sync with the const block above by
// TestAllBZZCodesEnumerated; it is the source of truth for the doc-coverage drift test.
var allBZZCodes = []diag.Code{
	UndefinedName, UndefinedType, NonBoolCondition, ArgumentError, TypeMismatch,
	UnresolvedImport, FiberMisuse,
}
