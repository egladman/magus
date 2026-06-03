package std

import (
	"context"
	"log/slog"
	"os"

	"github.com/egladman/magus/internal/sandbox"
)

//go:generate go run ../../cmd/magus-bindings-gen -module env -lang buzz -out gen/buzz/env.go

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
