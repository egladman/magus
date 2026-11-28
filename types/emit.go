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

// EmitNoEncoding is a hole that names no encoding. No default is right in every
// position a hole can take, so an empty spec is refused rather than read as raw.
const EmitNoEncoding DiagnosticCode = "MGS1071"

// EmitUnknownEncoding is a hole whose encoding the output path's language does not
// declare, or an output path whose extension no language claims.
const EmitUnknownEncoding DiagnosticCode = "MGS1072"

// EmitWrongKind is a value whose kind does not fit its hole's encoding, such as a
// template in a str hole or a list under !s.
const EmitWrongKind DiagnosticCode = "MGS1073"

// EmitRefusedValue is a value its encoding refuses. A keyword, a pattern miss, a
// control character the encoding cannot write and an empty or multi-line raw are each one.
const EmitRefusedValue DiagnosticCode = "MGS1074"

// EmitConversion is a conversion other than !s. Buzz has no repr or ascii, so !r
// and !a cannot be respected and are refused rather than ignored.
const EmitConversion DiagnosticCode = "MGS1075"

// EmitBlockShape is a multi-line block whose hole shares its line with other text,
// where no indentation could be carried onto the lines after the first.
const EmitBlockShape DiagnosticCode = "MGS1076"

// EmitBadShape is a template built by hand whose strings count is not its
// interpolations count plus one.
const EmitBadShape DiagnosticCode = "MGS1077"

// EmitDuplicatePath is a plan naming one output path twice, where the last file
// would silently win.
const EmitDuplicatePath DiagnosticCode = "MGS1078"

// EmitNoFinalNewline is a non-empty output that does not end in a line feed.
const EmitNoFinalNewline DiagnosticCode = "MGS1079"
