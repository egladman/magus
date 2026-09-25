package filesystem

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ModeRule maps a sandbox.allow mode to the grants it spells: r for read, w for
// write, x for exec. The empty mode is ro. Anything else is an error, so a typo such
// as RW cannot quietly grant less, or more, than was meant.
func ModeRule(mode string) (Rule, error) {
	switch mode {
	case "", "ro":
		return Rule{Read: true}, nil
	case "rw":
		return Rule{Read: true, Write: true}, nil
	case "rx":
		return Rule{Read: true, Exec: true}, nil
	case "rwx":
		return Rule{Read: true, Write: true, Exec: true}, nil
	}
	return Rule{}, fmt.Errorf("sandbox: unknown mode %q (want ro, rw, rx or rwx)", mode)
}

// ErrUnsetVariable is wrapped by ExpandUserRule when rawPath names a variable that
// is unset or empty.
var ErrUnsetVariable = errors.New("variable is unset or empty")

// ExpandUserRule resolves a magus.yaml sandbox.allow entry to a Rule granting what
// mode spells (see ModeRule). rawPath may start with ~ for home and may reference
// $VAR or ${VAR}, looked up with lookupEnv.
//
// A variable that is unset or empty is an error rather than an empty string:
// "$UNSET/" would otherwise grant "/". A relative result is an error too, since it
// would be resolved against whatever directory magus happens to run in.
func ExpandUserRule(rawPath, mode, home string, lookupEnv func(string) (string, bool)) (Rule, error) {
	rule, err := ModeRule(mode)
	if err != nil {
		return Rule{}, err
	}
	var unset []string
	expanded := os.Expand(rawPath, func(name string) string {
		v, ok := lookupEnv(name)
		if !ok || v == "" {
			unset = append(unset, name)
		}
		return v
	})
	if len(unset) > 0 {
		return Rule{}, fmt.Errorf("sandbox: %q: %w: %s", rawPath, ErrUnsetVariable, strings.Join(unset, ", "))
	}
	if expanded == "~" || strings.HasPrefix(expanded, "~/") {
		if home == "" {
			return Rule{}, fmt.Errorf("sandbox: %q: ~ needs a home directory and none is known", rawPath)
		}
		expanded = filepath.Join(home, expanded[1:])
	}
	if !filepath.IsAbs(expanded) {
		return Rule{}, fmt.Errorf("sandbox: %q: path must be absolute", rawPath)
	}
	rule.Path = ResolveRulePath(expanded)
	return rule, nil
}
