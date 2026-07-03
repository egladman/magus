//go:build !wasm

package std

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-utils bindings -module env -lang buzz -out ../host/gen/env.go

func init() { Register(Env) }

// Env is the "env" host module: process environment-variable access, filtered by the sandbox policy.
var Env = Module{
	Name: "env",
	Doc:  "Process environment variable access.",
	Methods: []Method{
		{
			Name:    "get",
			Doc:     "Return the value of name, or \"\" if unset. Use lookup to tell unset from set-but-empty.",
			Args:    []Arg{{Name: "name", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    EnvGet,
		},
		{
			Name:    "lookup",
			Doc:     "Return (value, found); found is false when name is unset or stripped by the sandbox.",
			Args:    []Arg{{Name: "name", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}, {Type: TypeBool}},
			Impl:    EnvLookup,
		},
		{
			Name:    "set",
			Doc:     "Set name to value in the current process environment.",
			Args:    []Arg{{Name: "name", Type: TypeString}, {Name: "value", Type: TypeString}},
			Returns: nil,
			Impl:    EnvSet,
		},
		{
			Name:    "list",
			Doc:     "Return all environment variables as a name→value map.",
			Args:    nil,
			Returns: []Ret{{Type: TypeStringMap}},
			Impl:    EnvList,
		},
		{
			Name:    "unset",
			Doc:     "Remove name from the current process environment.",
			Args:    []Arg{{Name: "name", Type: TypeString}},
			Returns: nil,
			Impl:    EnvUnset,
		},
		{
			Name:    "expand",
			Doc:     "Replace $VAR and ${VAR} references in s with their values (sandbox-stripped names expand to \"\").",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    EnvExpand,
		},
		{
			Name:    "home",
			Doc:     "Return the current user's home directory.",
			Args:    nil,
			Returns: []Ret{{Type: TypeString}},
			Impl:    EnvHome,
		},
		{
			Name:    "get_or",
			Doc:     "Return the value of name, or def when name is unset or stripped by the sandbox. Unlike get, an empty string is returned as-is — def only applies when the variable is absent.",
			Args:    []Arg{{Name: "name", Type: TypeString}, {Name: "def", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    EnvGetOr,
		},
		{
			Name:    "require",
			Doc:     "Return the value of name, or raise when it is unset or stripped by the sandbox. The fail-fast complement to get/lookup: a CI magusfile that needs GITHUB_TOKEN states the requirement once instead of threading a lookup-then-fatal check through every caller. A set-but-empty variable satisfies the requirement (its empty value is returned).",
			Args:    []Arg{{Name: "name", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    EnvRequire,
		},
	},
}

// EnvGet returns the value of the named variable, or "" if unset or stripped by the sandbox policy.
func EnvGet(ctx context.Context, name string) (string, error) {
	if p := sandbox.FromContext(ctx); p != nil && !p.AllowEnv(name) {
		// Return empty string rather than error: env.get is widely used
		// as a "did the user set X?" probe. A hard error would break
		// otherwise-innocuous magusfiles in the sandbox.
		return "", nil
	}
	return os.Getenv(name), nil
}

// EnvLookup returns (value, found) for the named variable. Unlike EnvGet it
// distinguishes "set but empty" ("", true) from "unset" ("", false) — the
// distinction Go's os.LookupEnv exposes and os.Getenv collapses. A variable the
// sandbox policy strips reports ("", false): a stripped secret is
// indistinguishable from an absent one, so a spell cannot probe for its
// existence (mirrors EnvGet's information-hiding).
func EnvLookup(ctx context.Context, name string) (string, bool, error) {
	if p := sandbox.FromContext(ctx); p != nil && !p.AllowEnv(name) {
		return "", false, nil
	}
	v, ok := os.LookupEnv(name)
	return v, ok, nil
}

// EnvSet sets name to value in the current process environment, unless the sandbox policy strips name.
func EnvSet(ctx context.Context, name, value string) error {
	if types.Recording(ctx) {
		return nil
	}
	slog.Debug("env.set", "name", name)
	if p := sandbox.FromContext(ctx); p != nil && !p.AllowEnv(name) {
		// Refuse re-introduction of names the policy strips. Without this
		// a spell could set GITHUB_TOKEN back into magus's env so the
		// next os.exec carries it.
		slog.Warn("env.set blocked by the sandbox", "name", name)
		return nil
	}
	return os.Setenv(name, value)
}

// EnvUnset removes name from the current process environment, unless the sandbox
// policy strips name (in which case it is already invisible and the call is a
// no-op, mirroring EnvSet's refusal to touch stripped names).
func EnvUnset(ctx context.Context, name string) error {
	if types.Recording(ctx) {
		return nil
	}
	if p := sandbox.FromContext(ctx); p != nil && !p.AllowEnv(name) {
		slog.Warn("env.unset blocked by the sandbox", "name", name)
		return nil
	}
	return os.Unsetenv(name)
}

// EnvExpand replaces $VAR and ${VAR} references in s using the same
// sandbox-aware lookup as EnvGet: a name the policy strips (or that is unset)
// expands to "", so a spell cannot recover a hidden secret by interpolating it
// into a string.
func EnvExpand(ctx context.Context, s string) (string, error) {
	p := sandbox.FromContext(ctx)
	return os.Expand(s, func(name string) string {
		if p != nil && !p.AllowEnv(name) {
			return ""
		}
		return os.Getenv(name)
	}), nil
}

// EnvHome returns the current user's home directory.
func EnvHome(_ context.Context) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return home, nil
}

// EnvGetOr returns the value of name, or def if the variable is unset or stripped
// by the sandbox. A variable that is set but empty returns its empty value — def
// is only the fallback for absence.
func EnvGetOr(ctx context.Context, name, def string) (string, error) {
	v, ok, err := EnvLookup(ctx, name)
	if err != nil {
		return def, err
	}
	if !ok {
		return def, nil
	}
	return v, nil
}

// EnvRequire returns the value of name, or an error when it is unset or stripped
// by the sandbox. It shares EnvLookup's sandbox-aware "found" semantics: a
// stripped variable is reported as absent (and so raises), and a set-but-empty
// variable is present (its empty value is returned).
func EnvRequire(ctx context.Context, name string) (string, error) {
	v, ok, err := EnvLookup(ctx, name)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("env.require: %q is not set", name)
	}
	return v, nil
}

// EnvList returns all environment variables as a name→value map, omitting any the sandbox policy strips.
func EnvList(ctx context.Context) (map[string]string, error) {
	raw := os.Environ()
	p := sandbox.FromContext(ctx)
	m := make(map[string]string, len(raw))
	for _, kv := range raw {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				name := kv[:i]
				if p != nil && !p.AllowEnv(name) {
					break
				}
				m[name] = kv[i+1:]
				break
			}
		}
	}
	return m, nil
}
