package std

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

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
		{
			Name:    "parse_dotenv",
			Doc:     "Parse .env-format content into a name->value map. Supports KEY=VALUE, blank lines, # comments, a leading `export `, single/double quotes (double-quoted values honor \\n \\t \\\" \\\\ escapes), and inline comments after unquoted values. Pure: it does not touch the process environment.",
			Args:    []Arg{{Name: "content", Type: TypeString}},
			Returns: []Ret{{Type: TypeStringMap}},
			Impl:    EnvParseDotenv,
		},
		{
			Name:    "read_dotenv",
			Doc:     "Read a .env file and return its name->value map (parse_dotenv over the file contents). Errors if the file cannot be read.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: []Ret{{Type: TypeStringMap}},
			Impl:    EnvReadDotenv,
		},
		{
			Name:    "load_dotenv",
			Doc:     "Read a .env file and set each variable in the process environment, without overwriting names already set (the dotenv convention) or names the sandbox strips. A no-op in a recording/dry-run.",
			Args:    []Arg{{Name: "path", Type: TypeString}},
			Returns: nil,
			Impl:    EnvLoadDotenv,
		},
	},
}

// EnvGet returns the value of the named variable, or "" if unset or stripped by the sandbox policy.
func EnvGet(ctx context.Context, name string) (string, error) {
	if p := sandbox.FromContext(ctx); p != nil && !p.AllowEnv(name) {
		// Empty string, not an error: env.get is widely used as a "did the user
		// set X?" probe, and a hard error would break innocuous magusfiles in
		// the sandbox.
		return "", nil
	}
	return os.Getenv(name), nil
}

// EnvLookup returns (value, found) for the named variable, distinguishing "set
// but empty" ("", true) from "unset" ("", false) as os.LookupEnv does and
// os.Getenv does not. A sandbox-stripped variable reports ("", false) so a
// stripped secret is indistinguishable from an absent one and cannot be probed
// for (mirrors EnvGet's information-hiding).
func EnvLookup(ctx context.Context, name string) (string, bool, error) {
	if p := sandbox.FromContext(ctx); p != nil && !p.AllowEnv(name) {
		return "", false, nil
	}
	v, ok := os.LookupEnv(name)
	return v, ok, nil
}

// EnvSet sets name to value in the current process environment, unless the sandbox policy strips name.
func EnvSet(ctx context.Context, name, value string) error {
	if types.Tracing(ctx) {
		return nil
	}
	slog.Debug("env.set", "name", name)
	if p := sandbox.FromContext(ctx); p != nil && !p.AllowEnv(name) {
		// Refuse to re-introduce a stripped name; otherwise a spell could set
		// GITHUB_TOKEN back into magus's env so the next os.exec carries it.
		slog.Warn("env.set blocked by the sandbox", "name", name)
		return nil
	}
	return os.Setenv(name, value)
}

// EnvUnset removes name from the current process environment, unless the sandbox
// policy strips name (in which case it is already invisible and the call is a
// no-op, mirroring EnvSet's refusal to touch stripped names).
func EnvUnset(ctx context.Context, name string) error {
	if types.Tracing(ctx) {
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
// by the sandbox. A variable that is set but empty returns its empty value; def
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

// EnvList returns all environment variables as a name-value map, omitting any the sandbox policy strips.
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

// EnvParseDotenv parses .env-format content into a name->value map. It is pure
// (no process-environment access), so it is browser-safe.
func EnvParseDotenv(_ context.Context, content string) (map[string]string, error) {
	return parseDotenv(content), nil
}

// EnvReadDotenv reads a .env file and parses it.
func EnvReadDotenv(_ context.Context, path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("env.read_dotenv: %w", err)
	}
	return parseDotenv(string(data)), nil
}

// EnvLoadDotenv reads a .env file and sets each variable into the process
// environment. Existing names win (the dotenv convention), sandbox-stripped names
// are skipped (matching EnvSet), and a recording/dry-run is a no-op.
func EnvLoadDotenv(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("env.load_dotenv: %w", err)
	}
	if types.Tracing(ctx) {
		return nil
	}
	p := sandbox.FromContext(ctx)
	for k, v := range parseDotenv(string(data)) {
		if _, exists := os.LookupEnv(k); exists {
			continue
		}
		if p != nil && !p.AllowEnv(k) {
			slog.Warn("env.load_dotenv skipped a sandbox-stripped name", "name", k)
			continue
		}
		if err := os.Setenv(k, v); err != nil {
			return fmt.Errorf("env.load_dotenv: set %s: %w", k, err)
		}
	}
	return nil
}

// parseDotenv parses .env-format text: KEY=VALUE lines, with blank lines and
// #-comments ignored, an optional leading "export ", and single/double-quoted
// values. Double-quoted values honor a small escape set; single-quoted are
// literal; unquoted values drop an inline " #" comment and surrounding space.
func parseDotenv(content string) map[string]string {
	out := map[string]string{}
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			continue
		}
		out[key] = unquoteDotenv(strings.TrimSpace(line[eq+1:]))
	}
	return out
}

// unquoteDotenv resolves a single .env value's quoting.
func unquoteDotenv(v string) string {
	if len(v) >= 2 {
		if v[0] == '"' && v[len(v)-1] == '"' {
			return strings.NewReplacer(
				`\n`, "\n", `\t`, "\t", `\r`, "\r", `\"`, `"`, `\\`, `\`,
			).Replace(v[1 : len(v)-1])
		}
		if v[0] == '\'' && v[len(v)-1] == '\'' {
			return v[1 : len(v)-1]
		}
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = strings.TrimSpace(v[:i])
	}
	return v
}
