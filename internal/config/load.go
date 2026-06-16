package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

// Filename is the canonical config file name magus searches for.
const Filename = "magus.yaml"

// DefaultHistoryPath returns $XDG_STATE_HOME/magus/history/v1.json (or ~/.local/state equivalent).
// The path is outside .magus/ so it is never swept into a build-cache GHA step.
func DefaultHistoryPath() string {
	if s := os.Getenv("XDG_STATE_HOME"); s != "" {
		return filepath.Join(s, "magus", "history", "v1.json")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "magus", "history", "v1.json")
	}
	// Last resort: relative path (unusual but keeps the binary functional).
	return filepath.Join(".magus", "history", "v1.json")
}

// UserConfigDir returns the base directory for magus's user-global config,
// honoring XDG_CONFIG_HOME on every platform. os.UserConfigDir consults
// XDG_CONFIG_HOME only on Linux (macOS returns ~/Library/Application Support),
// but magus documents its config home as $XDG_CONFIG_HOME/magus everywhere, so
// check that first and fall back to the OS default when it is unset.
func UserConfigDir() (string, error) {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		return x, nil
	}
	return os.UserConfigDir()
}

// EnvPrefix is the lowercase env-var prefix; Cache.Dir → MAGUS_CACHE_DIR.
const EnvPrefix = "magus"

// Load merges defaults → user-global → workspace → cwd → MAGUS_* env vars.
// If explicitPath is non-empty only that file is loaded (missing = hard error).
func Load(explicitPath string) (Config, error) {
	return LoadWithRoot(explicitPath, "")
}

// LoadWithRoot is like [Load] but accepts a pre-discovered workspace root.
// When knownRoot is non-empty it is used directly for tier 3 without walking
// up the directory tree, saving redundant stat calls on cold starts where the
// caller has already located the root.
func LoadWithRoot(explicitPath, knownRoot string) (Config, error) {
	cfg := Defaults()

	if explicitPath != "" {
		loaded, err := loadFileInto(cfg, explicitPath)
		if err != nil {
			return Config{}, err
		}
		cfg = loaded
		if err := Validate(cfg); err != nil {
			return Config{}, err
		}
		warnIfConcurrencyHigh(cfg.Concurrency, runtime.NumCPU())
		return cfg, nil
	}

	// Tier 2: user-global ($XDG_CONFIG_HOME/magus/)
	if udc, err := UserConfigDir(); err == nil {
		loaded, err := loadDirInto(cfg, filepath.Join(udc, "magus"))
		if err != nil {
			return Config{}, err
		}
		cfg = loaded
	}

	// Tier 3: workspace root — use caller-supplied root if available, else walk up.
	wsRoot := knownRoot
	if wsRoot == "" {
		wsRoot = findWorkspaceRoot()
	}
	if wsRoot != "" {
		loaded, err := loadDirInto(cfg, wsRoot)
		if err != nil {
			return Config{}, err
		}
		cfg = loaded
	}

	// Tier 4: cwd project-local (skip when cwd equals workspace root)
	cwd, err := os.Getwd()
	if err == nil && cwd != wsRoot {
		loaded, err := loadDirInto(cfg, cwd)
		if err != nil {
			return Config{}, err
		}
		cfg = loaded
	}

	if err := Validate(cfg); err != nil {
		return Config{}, err
	}
	warnIfConcurrencyHigh(cfg.Concurrency, runtime.NumCPU())
	return cfg, nil
}

// warnIfConcurrencyHigh emits a single warning when the configured concurrency
// is more than 2× the local CPU count. This is informational only; magus
// honours the explicit value because the planning host's NumCPU may not
// match the executor's (e.g. a CI shard running on a different runner).
func warnIfConcurrencyHigh(concurrency, numCPU int) {
	if concurrency <= 0 || numCPU <= 0 {
		return
	}
	if concurrency > 2*numCPU {
		slog.Warn(
			"config.concurrency_high",
			slog.Int("concurrency", concurrency),
			slog.Int("num_cpu", numCPU),
			slog.String("msg", fmt.Sprintf("concurrency=%d exceeds 2×NumCPU=%d; expect contention", concurrency, 2*numCPU)),
		)
	}
}

// loadDirInto looks for magus.yaml or .magus.yaml in dir and, if found,
// merges it on top of cfg. Returns cfg unchanged when neither file exists.
// Returns an error when both files exist in the same directory.
func loadDirInto(cfg Config, dir string) (Config, error) {
	plain := filepath.Join(dir, "magus.yaml")
	dotted := filepath.Join(dir, ".magus.yaml")

	_, plainErr := os.Stat(plain)
	_, dottedErr := os.Stat(dotted)

	plainExists := plainErr == nil
	dottedExists := dottedErr == nil

	if plainExists && dottedExists {
		return Config{}, fmt.Errorf("config: %s contains both magus.yaml and .magus.yaml — pick one", dir)
	}
	if !plainExists && !dottedExists {
		return cfg, nil
	}

	path := plain
	if dottedExists {
		path = dotted
	}
	return loadFileInto(cfg, path)
}

// loadFileInto parses the YAML at path and merges its values on top of cfg.
// Unknown YAML keys are silently accepted (non-strict mode) but a WARN is
// emitted via slog so users can spot typos or stale config keys.
func loadFileInto(cfg Config, path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	// Probe for unknown keys: use a strict decoder and discard the error
	// (we still accept the file), but log a warning so users notice typos.
	var probe Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if decErr := dec.Decode(&probe); decErr != nil {
		slog.Warn("config: unknown or unexpected keys in config file (run 'magus config validate' for details)",
			"path", path, "detail", decErr.Error())
	}
	var overlay Config
	if err := yaml.Unmarshal(data, &overlay); err != nil {
		return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return mergeConfig(cfg, overlay), nil
}

// mergeConfig returns dst with every non-zero field from src applied on top,
// recursing into nested structs so a partial overlay (e.g. only daemon.idle_ttl)
// merges field-by-field over the defaults. The rule is "non-zero wins": a field
// left at its zero value means "inherit", never "force zero", so an absent YAML
// key (which decodes to a zero value) cannot clobber an upstream tier. Pointer
// fields (tri-state *bool) override only when non-nil; slices and maps when
// non-empty. Driving this by reflection keeps it exhaustive — it can never drift
// from the Config struct, which the previous hand-written merge repeatedly did
// (it silently dropped daemon.idle_ttl, vcs.*, mcp.*, health.*, strict, …).
func mergeConfig(dst, src Config) Config {
	mergeStruct(reflect.ValueOf(&dst).Elem(), reflect.ValueOf(src))
	return dst
}

// mergeStruct applies mergeConfig's non-zero-wins rule field-by-field, recursing
// into nested structs. dst must be addressable; unsettable fields are skipped.
func mergeStruct(dst, src reflect.Value) {
	for i := 0; i < src.NumField(); i++ {
		df, sf := dst.Field(i), src.Field(i)
		if !df.CanSet() {
			continue
		}
		if sf.Kind() == reflect.Struct {
			mergeStruct(df, sf)
			continue
		}
		if !sf.IsZero() {
			df.Set(sf)
		}
	}
}

// parseBoolEnv parses a boolean environment variable value using a
// case-insensitive comparison. "true", "1", "yes" → true; "false", "0", "no"
// → false. Any unrecognised value returns fallback unchanged.
//
//nolint:unused // canonical reference implementation that cmd/magus-config-gen codegen mirrors into generated config loaders.
func parseBoolEnv(v string, fallback bool) bool {
	switch strings.ToLower(v) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	}
	return fallback
}

// LoadFile parses the config file at path on top of Defaults() and returns
// the merged Config. When strict is true, unknown YAML keys are rejected and
// Validate is run on the result; errors from either step are returned as
// structured errors (*ValidationError for validation failures, plain errors
// for YAML syntax and unknown-field errors). When strict is false the
// behaviour mirrors the internal loadFileInto: unknown keys are silently
// ignored and Validate is not run.
func LoadFile(path string, strict bool) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	var overlay Config
	if strict {
		dec := yaml.NewDecoder(bytes.NewReader(data))
		dec.KnownFields(true)
		if err := dec.Decode(&overlay); err != nil {
			return Config{}, err
		}
	} else {
		if err := yaml.Unmarshal(data, &overlay); err != nil {
			return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
		}
	}
	merged := mergeConfig(Defaults(), overlay)
	if strict {
		if err := Validate(merged); err != nil {
			return Config{}, err
		}
	}
	return merged, nil
}

// findWorkspaceRoot walks up from cwd until it finds a directory containing
// go.mod, which magus treats as the workspace root. Returns "" on failure.
func findWorkspaceRoot() string {
	cur, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// ExtractFlag pre-scans args for -config/--config so the config file can
// be loaded before each subcommand registers its real flag set.
func ExtractFlag(args []string) string {
	for i, a := range args {
		switch {
		case a == "-config" || a == "--config":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "-config="):
			return strings.TrimPrefix(a, "-config=")
		case strings.HasPrefix(a, "--config="):
			return strings.TrimPrefix(a, "--config=")
		}
	}
	return ""
}
