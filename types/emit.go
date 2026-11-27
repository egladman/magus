package types

// Interpolation is one hole of a Template: PEP 750's Interpolation. Conversion is
// "a", "r", "s" or "" for none. FormatSpec is the encoding the hole names.
type Interpolation struct {
	Value      any
	Expression string
	Conversion string
	FormatSpec string
}

// Template is static strings and holes: PEP 750's Template. Strings holds one more
// item than Interpolations. Source is "" when built in Buzz.
type Template struct {
	Strings        []string
	Interpolations []Interpolation
	Source         string
}

// EmitFile is one file to write: where, and what.
type EmitFile struct {
	Path     string
	Template Template
}

// EmitBadHeader is a first line that is not the delimiter shown as a hole.
const EmitBadHeader DiagnosticCode = "MGS1064"

// EmitBadBraces is a brace run that opens or closes no hole, or a hole typed with the
// wrong delimiter count.
const EmitBadBraces DiagnosticCode = "MGS1065"

// EmitBadHole is a hole whose inside is not a name, a conversion and a spec.
const EmitBadHole DiagnosticCode = "MGS1066"

// EmitBadBytes is a byte-order mark, carriage return, NUL or invalid UTF-8.
const EmitBadBytes DiagnosticCode = "MGS1067"

// EmitMissingValue is a hole whose name the values lack.
const EmitMissingValue DiagnosticCode = "MGS1068"

// EmitUnusedValue is a value no hole reads.
const EmitUnusedValue DiagnosticCode = "MGS1069"

// EmitNullValue refuses a null in values: a hole bound to nothing would render nothing.
const EmitNullValue DiagnosticCode = "MGS1070"
