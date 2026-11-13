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

// Setting is one value a program reads from its config file, an environment
// variable or a flag. Each source it lacks is empty.
type Setting struct {
	GoPath   string    // "Cache.Dir"; empty when no struct field holds it
	YamlPath string    // "cache.dir"; empty when no config key sets it
	EnvVar   string    // "MAGUS_CACHE_DIR"; empty when no variable sets it
	Flag     FlagNames // long + optional short; Long empty means no flag
	Kind     command.Kind
	Usage    string // one-line description for flag.Usage
	Doc      string // the field's first doc paragraph
	Default  string // the value unset reads as; empty when zero

	// Floor lets the variable raise a Text value and never lower it, by
	// the type's WeakerThan: a value a parent hands its children.
	Floor bool

	// Internal marks a variable the program sets for the processes it
	// spawns. Docs list it as set by the program, never to set by hand.
	Internal bool
}

// NameSegment stands for a list entry's name key in a YamlPath, as in
// "sandbox.allow.<name>.mode".
const NameSegment = "<name>"

// ConfigurationSpec is every setting a program reads: the one inventory
// its flags, variables, config keys and docs come from.
type ConfigurationSpec struct {
	Settings []Setting
}

// Variable returns the setting the environment variable name sets.
func (c ConfigurationSpec) Variable(name string) (Setting, bool) {
	for _, s := range c.Settings {
		if s.EnvVar == name && name != "" {
			return s, true
		}
	}
	return Setting{}, false
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
