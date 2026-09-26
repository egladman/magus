package spells

import "github.com/egladman/magus/types/enum"

// The case lists of this package's closed string types, excluding each zero value.
// cmd/magus-utils's boundaryEnums names the same values for the Buzz mirror, and
// TestBoundaryEnumsMatchTheirSets holds the two to one list.

var patchOpKinds = enum.Set[PatchOpKind]{"add", "remove", "replace", "move", "copy", "test"}

func (v PatchOpKind) Values() []string { return patchOpKinds.Strings() }
func (v PatchOpKind) Valid() bool      { return patchOpKinds.Valid(v) }
func (v PatchOpKind) String() string   { return enum.String(v) }

var versionComponents = enum.Set[VersionComponent]{"major", "minor", "patch"}

func (v VersionComponent) Values() []string { return versionComponents.Strings() }
func (v VersionComponent) Valid() bool      { return versionComponents.Valid(v) }
func (v VersionComponent) String() string   { return enum.String(v) }

var diagnosticFormats = enum.Set[DiagnosticFormat]{"gnu"}

func (v DiagnosticFormat) Values() []string { return diagnosticFormats.Strings() }
func (v DiagnosticFormat) Valid() bool      { return diagnosticFormats.Valid(v) }
func (v DiagnosticFormat) String() string   { return enum.String(v) }

var symbolFormats = enum.Set[SymbolFormat]{"scip"}

func (v SymbolFormat) Values() []string { return symbolFormats.Strings() }
func (v SymbolFormat) Valid() bool      { return symbolFormats.Valid(v) }
func (v SymbolFormat) String() string   { return enum.String(v) }

var externals = enum.Set[External]{"reads-external", "mutates-external"}

func (v External) Values() []string { return externals.Strings() }
func (v External) Valid() bool      { return externals.Valid(v) }
func (v External) String() string   { return enum.String(v) }

var sandboxAccesses = enum.Set[SandboxAccess]{"ro", "rx", "rw", "rwx"}

func (v SandboxAccess) Values() []string { return sandboxAccesses.Strings() }
func (v SandboxAccess) Valid() bool      { return sandboxAccesses.Valid(v) }
func (v SandboxAccess) String() string   { return enum.String(v) }
