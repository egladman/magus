package configuration

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/libs/clikit/command"
)

// FlagNames carries the long and optional short forms of a CLI flag.
// Names are bare; callers prepend "--" / "-" for display.
type FlagNames struct {
	Long  string // e.g. "cache-dir"; empty means env-only
	Short string // e.g. "c"; empty when no short was declared
}

// Setting is one scalar config field exposed as a CLI flag and an
// environment variable.
type Setting struct {
	GoPath   string    // "Cache.Dir"
	YamlPath string    // "cache.dir"
	EnvVar   string    // "MAGUS_CACHE_DIR"
	Flag     FlagNames // long + optional short; Long empty means env-only
	Kind     command.Kind
	Usage    string // one-line description for flag.Usage
	Doc      string // the field's first doc paragraph
}

// String returns a single-line summary suitable for log output and %v
// formatting:
//
//	MAGUS_CACHE_DIR (--cache-dir, cache.dir)
//	MAGUS_OUTPUT (-o, --output, output)
//	MAGUS_HINTS_ENABLED (env-only, hints.enabled)
//
// For the labeled multi-line block, use [Setting.Describe].
func (s Setting) String() string {
	return fmt.Sprintf("%s (%s, %s)", s.EnvVar, flagsLabel(s.Flag), s.YamlPath)
}

// Describe renders a Setting as a labeled three-line block:
//
//	Env: MAGUS_CACHE_DIR
//	Flags: --cache-dir
//	Config: cache.dir
//
// The short form is omitted when none was declared. A setting with no CLI
// flag shows "(env-only)" on the Flags line.
func (s Setting) Describe() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Env: %s\n", s.EnvVar)
	if s.Flag.Long == "" {
		sb.WriteString("Flags: (env-only)\n")
	} else {
		flags := "--" + s.Flag.Long
		if s.Flag.Short != "" {
			flags = fmt.Sprintf("-%s, %s", s.Flag.Short, flags)
		}
		fmt.Fprintf(&sb, "Flags: %s\n", flags)
	}
	fmt.Fprintf(&sb, "Config: %s", s.YamlPath)
	return sb.String()
}

func flagsLabel(f FlagNames) string {
	if f.Long == "" {
		return "env-only"
	}
	if f.Short != "" {
		return fmt.Sprintf("-%s, --%s", f.Short, f.Long)
	}
	return "--" + f.Long
}
