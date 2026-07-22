// Package fieldtype holds the hand-written config-field types (Kind, Field,
// FlagNames) that the generated inventory in schema/gen populates. It is a
// dependency-free leaf: schema/gen imports it for the types, keeping the generated
// data pure (no import of internal/config), and the schema package re-exports these
// as aliases so callers need not import this package directly.
package fieldtype

import (
	"fmt"
	"strings"
)

// Kind identifies the Go type of a config field.
type Kind uint8

// The Kind constants enumerate the supported config-field Go types.
const (
	KindString      Kind = iota // string
	KindInt                     // int
	KindBool                    // bool
	KindFloat64                 // float64
	KindBoolPtr                 // *bool (env-only; three-state nil/true/false)
	KindDuration                // time.Duration
	KindStringSlice             // []string (env-only; comma-separated)
)

// String returns the name of the kind constant (e.g. "KindBool"), matching the
// source identifier so log output is human-readable rather than a raw integer.
func (k Kind) String() string {
	switch k {
	case KindString:
		return "KindString"
	case KindInt:
		return "KindInt"
	case KindBool:
		return "KindBool"
	case KindFloat64:
		return "KindFloat64"
	case KindBoolPtr:
		return "KindBoolPtr"
	case KindDuration:
		return "KindDuration"
	case KindStringSlice:
		return "KindStringSlice"
	default:
		return fmt.Sprintf("Kind(%d)", uint8(k))
	}
}

// FlagNames carries the long and (optional) short forms of a CLI flag.
// Names are bare — callers prepend "--" / "-" for display.
type FlagNames struct {
	Long  string // e.g. "cache-dir"; empty means env-only
	Short string // e.g. "c"; empty when no short was declared
}

// Field documents one scalar config field exposed as a CLI flag and/or
// MAGUS_* environment variable.
type Field struct {
	GoPath   string    // "Cache.Dir"
	YamlPath string    // "cache.dir"
	EnvVar   string    // "MAGUS_CACHE_DIR"
	Flag     FlagNames // long + optional short; Long empty means env-only
	Kind     Kind
	Usage    string // one-line description for flag.Usage
}

// String returns a single-line summary suitable for log output and %v
// formatting:
//
//	MAGUS_CACHE_DIR (--cache-dir, cache.dir)
//	MAGUS_OUTPUT (-o, --output, output)
//	MAGUS_HINTS_ENABLED (env-only, hints.enabled)
//
// For the labelled multi-line block, use [Field.Describe].
func (f Field) String() string {
	flags := flagsLabel(f.Flag)
	return fmt.Sprintf("%s (%s, %s)", f.EnvVar, flags, f.YamlPath)
}

// Describe renders a Field as a labelled three-line block:
//
//	Env: MAGUS_CACHE_DIR
//	Flags: --cache-dir
//	Config: cache.dir
//
// The short form is omitted when none was declared. Fields with no CLI flag
// (KindBoolPtr) show "(env-only)" on the Flags line.
func (f Field) Describe() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Env: %s\n", f.EnvVar)
	if f.Flag.Long == "" {
		sb.WriteString("Flags: (env-only)\n")
	} else {
		flags := "--" + f.Flag.Long
		if f.Flag.Short != "" {
			flags = "-" + f.Flag.Short + ", " + flags
		}
		fmt.Fprintf(&sb, "Flags: %s\n", flags)
	}
	fmt.Fprintf(&sb, "Config: %s", f.YamlPath)
	return sb.String()
}

func flagsLabel(f FlagNames) string {
	if f.Long == "" {
		return "env-only"
	}
	if f.Short != "" {
		return "-" + f.Short + ", --" + f.Long
	}
	return "--" + f.Long
}
