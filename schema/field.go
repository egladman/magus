package schema

import "github.com/egladman/magus/schema/gen"

// Type and constant aliases so callers using the schema package do not need
// to import schema/gen directly.

// Kind aliases gen.Kind: the scalar kind of a config field.
type Kind = gen.Kind

// FlagNames aliases gen.FlagNames: the long/short CLI flag names for a field.
type FlagNames = gen.FlagNames

// Field aliases gen.Field: one scalar config field exposed as a flag and/or env var.
type Field = gen.Field

// The Kind constants mirror gen's scalar config-field kinds.
const (
	KindString      Kind = gen.KindString
	KindInt         Kind = gen.KindInt
	KindBool        Kind = gen.KindBool
	KindFloat64     Kind = gen.KindFloat64
	KindBoolPtr     Kind = gen.KindBoolPtr
	KindDuration    Kind = gen.KindDuration
	KindStringSlice Kind = gen.KindStringSlice
)
