package configuration

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/egladman/magus/libs/apiversion"
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

// Parse converts value, as a person types it, to the value a config file
// holds for s: a list splits on commas, and a map reads as a YAML object.
// A value its Kind refuses is an error.
func (s Setting) Parse(value string) (any, error) {
	switch s.Kind {
	case command.String, command.Text:
		return value, nil
	case command.Int:
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("invalid integer %q", value)
		}
		return n, nil
	case command.Float:
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q", value)
		}
		return f, nil
	case command.Duration:
		d, err := time.ParseDuration(value)
		if err != nil {
			return nil, fmt.Errorf("invalid duration %q (e.g. \"6h\", \"30m\")", value)
		}
		return d, nil
	case command.Bool:
		return parseBool(value)
	case command.OptionalBool:
		b, err := parseBool(value)
		if err != nil {
			return nil, err
		}
		return &b, nil
	case command.StringList:
		var parts []string
		for _, p := range strings.Split(value, ",") {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
		return parts, nil
	case command.StringMap:
		m := map[string]string{}
		if err := yaml.Unmarshal([]byte(value), &m); err != nil {
			return nil, fmt.Errorf("invalid map %q (YAML object, e.g. \"{X: Y}\"): %w", value, err)
		}
		return m, nil
	}
	return nil, fmt.Errorf("%s: unsupported kind %q", s.YamlPath, s.Kind)
}

// parseBool accepts true/1/yes and false/0/no in any case.
func parseBool(value string) (bool, error) {
	switch strings.ToLower(value) {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	}
	return false, fmt.Errorf("invalid boolean %q (use true/false/1/0)", value)
}

// NameSegment stands for a list entry's name key in a YamlPath, as in
// "sandbox.allow.<name>.mode".
const NameSegment = "<name>"

// ConfigurationSpec is every setting a program reads: the one inventory
// its flags, variables, config keys and docs come from.
type ConfigurationSpec struct { //nolint:stutter // the Kubernetes JobSpec shape: a Kind's spec is <Kind>Spec
	Settings []Setting
}

// Kind names the record apiversion.New stamps around a ConfigurationSpec.
func (ConfigurationSpec) Kind() apiversion.Kind { return "Configuration" }

// APIVersion names the format group clikit owns for a settings inventory.
func (ConfigurationSpec) APIVersion() apiversion.APIVersion {
	return "clikit.configuration/v1alpha1"
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
