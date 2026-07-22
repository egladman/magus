package schema

import "github.com/egladman/magus/schema/fieldtype"

// Type and constant aliases so callers using the schema package do not need
// to import schema/fieldtype directly.

// Kind aliases fieldtype.Kind: the scalar kind of a config field.
type Kind = fieldtype.Kind

// FlagNames aliases fieldtype.FlagNames: the long/short CLI flag names for a field.
type FlagNames = fieldtype.FlagNames

// Field aliases fieldtype.Field: one scalar config field exposed as a flag and/or env var.
type Field = fieldtype.Field

// The Kind constants mirror fieldtype's scalar config-field kinds.
const (
	KindString      Kind = fieldtype.KindString
	KindInt         Kind = fieldtype.KindInt
	KindBool        Kind = fieldtype.KindBool
	KindFloat64     Kind = fieldtype.KindFloat64
	KindBoolPtr     Kind = fieldtype.KindBoolPtr
	KindDuration    Kind = fieldtype.KindDuration
	KindStringSlice Kind = fieldtype.KindStringSlice
)
