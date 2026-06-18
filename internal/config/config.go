// Package config holds the magus configuration schema and yaml-based loader.
// Config is loaded in priority order: defaults → magus.yaml → MAGUS_* env vars → CLI flags.
// Env vars use MAGUS_ prefix + yaml-tag path uppercased (e.g. cache.dir → MAGUS_CACHE_DIR).
package config

import (
	"log/slog"
	"strings"
	"time"

	"github.com/egladman/magus/types"
)

// Config is the top-level magus configuration.
type Config struct {
	Cache     Cache     `yaml:"cache"`
	CI        CI        `yaml:"ci"`
	Flake     Flake     `yaml:"flake"`
	Graph     Graph     `yaml:"graph"`
	Watch     Watch     `yaml:"watch"`
	Health    Health    `yaml:"health"`
	Telemetry Telemetry `yaml:"telemetry"`
	Daemon    Daemon    `yaml:"daemon"`
	VCS       VCS       `yaml:"vcs"`
	MCP       MCP       `yaml:"mcp"`
	Report    Report    `yaml:"report"`
	Log       Log       `yaml:"log"`
	Hints     Hints     `yaml:"hints"`

	// Concurrency caps concurrent builds; top-level and in-process fan-out share one limiter. Defaults to min(NumCPU, 8).
	Concurrency int `yaml:"concurrency" validate:"gte=0" cli:"short=j"`

	// HistoryPath is the path to the runtime-history JSON used by flake detection,
	// CI forecaster, graph timing, and bisect. Defaults to $XDG_STATE_HOME/magus/history/v1.json.
	HistoryPath string `yaml:"history_path"`

	// DryRun prints what would run without executing. Equivalent to MAGUS_DRY_RUN=1.
	DryRun bool `yaml:"dry_run" cli:"short=u"`

	// Strict turns correctness warnings into errors (e.g. unregistered deps → ErrUnregisteredDep). Equivalent to MAGUS_STRICT=1.
	Strict bool `yaml:"strict"`

	// AssumeInteractive allows interactive commands even when ISATTY returns false. Default false.
	AssumeInteractive bool `yaml:"assume_interactive"`

	// Sandbox confines subprocesses and spells to the workspace + allowlist using Linux landlock (≥5.13)
	// when available, with binding-level fallback. See SandboxConfig for allowlist and env knobs.
	Sandbox SandboxConfig `yaml:"sandbox"`
}

// SandboxConfig is the per-workspace sandbox policy.
type SandboxConfig struct {
	Enabled bool               `yaml:"enabled"` // master switch; equivalent to MAGUS_SANDBOX_ENABLED=1
	Allow   []SandboxAllowPath `yaml:"allow"`   // extra {path, mode} entries extending the filesystem allowlist
	Env     SandboxEnv         `yaml:"env"`     // env-var passthrough rules
}

// SandboxAllowPath is one extra filesystem allowlist entry. Mode is "ro" or "rw"; other values emit MGS2004.
type SandboxAllowPath struct {
	// Name is a free-form label for the entry. It is ignored by the sandbox; it
	// exists so `magus config set sandbox.allow.<name>.path=…` can address the
	// entry by name (the same convention used for other slice-of-struct config).
	Name string `yaml:"name,omitempty"`
	Path string `yaml:"path"`
	Mode string `yaml:"mode" validate:"omitempty,oneof=ro rw"`
}

// SandboxEnv controls per-child env passthrough when the sandbox is active.
type SandboxEnv struct {
	// Passthrough adds names/globs (e.g. "MISE_*") to the built-in env allowlist.
	Passthrough []string `yaml:"passthrough"`
}

// Log controls log output.
type Log struct {
	Format string `yaml:"format" validate:"omitempty,oneof=pretty plain text json"` // pretty|plain|text|json
	// Level is the minimum log level; "trace" also enables the startup timing table.
	Level string `yaml:"level" validate:"omitempty,oneof=trace debug info warn error"`
}

// Hints controls whether hint messages are emitted to the user.
type Hints struct {
	// Enabled controls whether hint messages (actionable nudges) are printed
	// to stderr. Defaults to true. Set hints.enabled: false in magus.yaml or
	// MAGUS_HINTS_ENABLED=false to suppress all hint output.
	// Pointer to distinguish "not set" from explicit false.
	Enabled *bool `yaml:"enabled"`
}

// LevelTrace is magus's most-verbose log level (one step below slog.LevelDebug).
const LevelTrace slog.Level = slog.LevelDebug - 4

// SlogLevel converts Level to slog.Level; unknown values return slog.LevelInfo.
func (l Log) SlogLevel() slog.Level {
	if strings.EqualFold(l.Level, "trace") {
		return LevelTrace
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(l.Level)); err != nil {
		return slog.LevelInfo
	}
	return lvl
}

// Cache controls the content-addressed build cache.
type Cache struct {
	Dir       string      `yaml:"dir"`                      // override default cache location (.magus/ in workspace root)
	Immutable bool        `yaml:"immutable"`                // true = read-only replay; default (false) writes new artifacts
	SizeMB    int         `yaml:"size_mb" validate:"gte=0"` // disk cap in MB (binary); 0 = unlimited
	Remote    CacheRemote `yaml:"remote"`                   // settings specific to a shared remote cache backend
}

// CacheRemote holds settings that apply only to a remote cache backend (wired via
// magus.cache.remote in the magusfile). The backend binding is code, so it stays
// in the magusfile; everything here is declarative policy.
type CacheRemote struct {
	TrustedKeys []string `yaml:"trusted_keys"` // base64 Ed25519 public keys a remote artifact must be signed by; required when a backend is wired
	// Insecure disables remote-cache signature verification: unsigned artifacts are
	// imported and produced with no trust set. A shared cache without signing is a
	// supply-chain hazard — use only for trusted single-repo CI, or to validate a
	// backend before minting keys. When true, trusted_keys is not required.
	Insecure bool `yaml:"insecure"`
}

// CI controls CI fan-out behaviour.
type CI struct {
	MaxShards        int `yaml:"max_shards" validate:"shard_count"`   // max parallel shards; -1 = unlimited
	RunnerPoolBudget int `yaml:"runner_pool_budget" validate:"gte=0"` // GHA matrix-level concurrency cap; 0 = no cap
}

// Flake controls flakiness detection and auto-retry for test runs.
type Flake struct {
	Enabled          bool    `yaml:"enabled"`
	BootstrapSamples int     `yaml:"bootstrap_samples" validate:"gte=0"` // outcomes below which all failures retry once
	MinSamples       int     `yaml:"min_samples" validate:"gte=0"`       // minimum outcomes before Wilson-score gates retry
	Threshold        float64 `yaml:"threshold" validate:"gte=0,lte=1"`   // Wilson lower-bound above which a project+target is flaky
	AnnotateGHA      bool    `yaml:"annotate_gha"`                       // emit ::warning annotations and GITHUB_STEP_SUMMARY table
}

// Watch controls magus watch defaults.
type Watch struct {
	// Ignore adds patterns (glob or {type,pattern}) beyond workspace builtins and --ignore flags.
	Ignore []types.IgnorePattern `yaml:"ignore" validate:"dive"`
}

// MCP controls the Model Context Protocol server (requires -tags mcp).
type MCP struct {
	Enabled *bool  `yaml:"enabled"`                                  // pointer distinguishes unset from explicit false
	Address string `yaml:"address" validate:"omitempty,mcp_address"` // host:port; default 127.0.0.1:7391
}

// Daemon controls the proc server's listen address and multi-workspace behaviour.
type Daemon struct {
	// Address is the unix:// socket the parent listens on; empty auto-generates one.
	Address string `yaml:"address" validate:"omitempty,magus_endpoint"`
	// IdleTTL controls workspace eviction in the multi-workspace daemon; 0 = default 6h.
	IdleTTL time.Duration `yaml:"idle_ttl"`
	// Socket is the runtime socket path set by the daemon for forwarded children; unix:// URL or bare path.
	Socket string `yaml:"socket"`
	// Workspaces is the explicit list of workspace roots to serve; non-empty enables eager union of sandbox
	// policies and rejects out-of-list workspaces (MGS2010).
	Workspaces []string `yaml:"workspaces"`
}

// VCS controls VCS-driven affected detection.
type VCS struct {
	Enabled *bool  `yaml:"enabled"` // false = fall back to all projects; pointer distinguishes unset
	Name    string `yaml:"name"`    // pin VCS by name (git/hg/jj); empty = autodetect
	// BaseRef sets the default base ref. Per-VCS overrides use MAGUS_VCS_<NAME>_BASE_REF (dynamic; not a Config field).
	BaseRef string `yaml:"base_ref"`
}

// Graph sets defaults for the graph subcommand.
type Graph struct {
	Direction string `yaml:"direction" validate:"omitempty,oneof=downstream upstream"` // "downstream" or "upstream"
	Spell     string `yaml:"spell"`                                                    // filter to a single spell
	Depth     int    `yaml:"depth" validate:"gte=0"`                                   // 0 = unlimited
	Roots     string `yaml:"roots"`                                                    // comma-separated starting nodes
}

// Health controls dependency-health checks run by magus doctor.
type Health struct {
	Exempt []string `yaml:"exempt"` // project paths exempt from blast-radius warnings (exact match)
}

// Telemetry holds OpenTelemetry exporter settings. OFF by default; no magus-operated backend exists.
// When Enabled, magus connects to the OTLP collector you configure and sends data there only.
type Telemetry struct {
	Enabled     bool              `yaml:"enabled"`
	Endpoint    string            `yaml:"endpoint"`                                      // host:port; required when Enabled
	Protocol    string            `yaml:"protocol" validate:"omitempty,oneof=grpc http"` // "grpc" or "http"
	Insecure    bool              `yaml:"insecure"`                                      // disable TLS
	ServiceName string            `yaml:"service_name"`                                  // resource attribute service.name
	SampleRatio float64           `yaml:"sample_ratio" validate:"gte=0,lte=1"`           // head-based sampling ratio; 1.0 = all
	Headers     map[string]string `yaml:"headers"`                                       // static OTLP request headers
}

// EnvVarDoc documents one MAGUS_* environment variable.
type EnvVarDoc struct {
	EnvVar  string // full name, e.g. "MAGUS_CACHE_DIR"
	YAMLKey string // equivalent magus.yaml path, e.g. "cache.dir"
	Default string // human-readable default; empty = unset
	Desc    string // one-line description
}

// Report controls JSONL event emission for magus run.
type Report struct {
	// Filter restricts event types via +type/-type/bare terms; any "+" sets default-deny.
	Filter []string `yaml:"filter"`
}

func boolPtr(v bool) *bool { return &v }

// EnvVarDocs returns documentation for every MAGUS_* environment variable in declaration order.
func EnvVarDocs() []EnvVarDoc {
	return []EnvVarDoc{
		{"MAGUS_CACHE_DIR", "cache.dir", "", "Override the default cache location (.magus/ in the workspace root)"},
		{"MAGUS_CACHE_IMMUTABLE", "cache.immutable", "false", "When true (or 1), open the cache in read-only mode: replay hits but never write new entries"},
		{"MAGUS_CACHE_SIZE_MB", "cache.size_mb", "0", "Cache disk usage cap in MB (binary, 1<<20); 0 means unlimited"},
		{"MAGUS_CACHE_REMOTE_INSECURE", "cache.remote.insecure", "false", "Disable remote-cache signature verification (accept/produce unsigned artifacts); for trusted single-repo CI only"},
		{"MAGUS_LOG_FORMAT", "log.format", "pretty", "Output format: pretty, plain, text, or json"},
		{"MAGUS_LOG_LEVEL", "log.level", "info", "Minimum log level: trace, debug, info, warn, error (trace also prints the startup timing table)"},
		{"MAGUS_CONCURRENCY", "concurrency", "min(NumCPU,8)", "Maximum number of concurrently running per-project build steps"},
		{"MAGUS_HISTORY_PATH", "history_path", "$XDG_STATE_HOME/magus/history/v1.json", "Path to the runtime-history JSON shared by flake detection, the CI forecaster, graph timing, and bisect"},
		{"MAGUS_DRY_RUN", "dry_run", "false", "When 1 or true, print what would run without executing anything"},
		{"MAGUS_STRICT", "strict", "false", "When 1 or true, workspace correctness warnings (e.g. unregistered dependencies) become errors that fail the run"},
		{"MAGUS_VCS_ENABLED", "vcs.enabled", "true", "Master switch for VCS-driven affected detection; false makes affected fall back to all projects"},
		{"MAGUS_VCS_NAME", "vcs.name", "", "Pin the active VCS by name (git, hg, jj); empty autodetects from .git/.hg/.jj"},
		{"MAGUS_VCS_BASE_REF", "vcs.base_ref", "", "Default base ref for the active VCS adapter, e.g. origin/main for git"},
		{"MAGUS_VCS_<NAME>_BASE_REF", "", "", "Per-VCS base-ref override, e.g. MAGUS_VCS_GIT_BASE_REF; dynamic pattern, read directly by package vcs"},
		{"MAGUS_DAEMON_SOCKET", "daemon.socket", "", "Runtime proc-server socket set by the daemon for forwarded child processes; unix:// URL or bare path"},
		{"MAGUS_CI_MAX_SHARDS", "ci.max_shards", "8", "Maximum number of parallel CI shards; -1 means unlimited"},
		{"MAGUS_CI_RUNNER_POOL_BUDGET", "ci.runner_pool_budget", "0", "Cross-shard concurrency cap at the GHA matrix level; 0 means unlimited"},
		{"MAGUS_SHARD", "", "", "CI matrix shard ID (e.g. \"0\"); equivalent to magus run --shard; set by .github/actions/magus"},
		{"MAGUS_N_SHARDS", "", "", "Total shard count for this matrix run; equivalent to magus run --n-shards; set by .github/actions/magus"},
		{"MAGUS_GRAPH_DIRECTION", "graph.direction", "downstream", "Default graph direction: downstream or upstream"},
		{"MAGUS_GRAPH_SPELL", "graph.spell", "", "Filter graph output to a single spell"},
		{"MAGUS_GRAPH_DEPTH", "graph.depth", "0", "Cap displayed graph depth (0 = unlimited)"},
		{"MAGUS_GRAPH_ROOTS", "graph.roots", "", "Comma-separated starting nodes for graph traversal"},
		{"MAGUS_TELEMETRY_ENABLED", "telemetry.enabled", "false", "Turn OTLP export on; magus connects to telemetry.endpoint when true"},
		{"MAGUS_TELEMETRY_ENDPOINT", "telemetry.endpoint", "", "OTLP collector address as host:port (no scheme); required when telemetry is enabled"},
		{"MAGUS_TELEMETRY_PROTOCOL", "telemetry.protocol", "grpc", "OTLP wire protocol: grpc or http"},
		{"MAGUS_TELEMETRY_INSECURE", "telemetry.insecure", "false", "Disable TLS for the OTLP exporter (plaintext local-collector setups)"},
		{"MAGUS_TELEMETRY_SERVICE_NAME", "telemetry.service_name", "magus", "Value of the resource attribute service.name on emitted spans/metrics"},
		{"MAGUS_TELEMETRY_SAMPLE_RATIO", "telemetry.sample_ratio", "1.0", "Head-based trace sampling ratio in [0,1]"},
		{"MAGUS_DAEMON_ADDRESS", "daemon.address", "", "Adopt-server socket as a unix:// URL; empty auto-generates a per-process socket"},
		{"MAGUS_DAEMON_IDLE_TTL", "daemon.idle_ttl", "6h", "Idle workspace eviction TTL for the multi-workspace daemon; e.g. \"6h\", \"30m\""},
		{"MAGUS_DAEMON_WORKSPACES", "daemon.workspaces", "", "Colon-separated list of workspace roots the daemon will serve; non-empty list triggers eager union of sandbox policies and rejection of out-of-list workspaces (MGS2010)"},
		{"MAGUS_ASSUME_INTERACTIVE", "assume_interactive", "false", "When 1 or true, assume an interactive terminal even if detection says otherwise"},
		{"MAGUS_MCP_ENABLED", "mcp.enabled", "true", "When 0 or false, refuse to start the MCP server even when the binary was built with -tags mcp"},
		{"MAGUS_MCP_ADDRESS", "mcp.address", "127.0.0.1:7391", "host:port for the MCP Streamable HTTP server started alongside the daemon"},
		{"MAGUS_HINTS_ENABLED", "hints.enabled", "true", "When false, suppress all hint messages printed to stderr"},
		{"MAGUS_FLAKE_ENABLED", "flake.enabled", "true", "Master switch for flakiness detection and auto-retry; false disables all retry logic"},
		{"MAGUS_FLAKE_BOOTSTRAP_SAMPLES", "flake.bootstrap_samples", "20", "Number of outcomes below which all failures are retried once (bootstrap phase)"},
		{"MAGUS_FLAKE_MIN_SAMPLES", "flake.min_samples", "20", "Minimum outcomes required before Wilson-score flake rate gates retry decisions"},
		{"MAGUS_FLAKE_THRESHOLD", "flake.threshold", "0.05", "Wilson lower-bound flake rate above which a project+target is considered flaky"},
		{"MAGUS_FLAKE_ANNOTATE_GHA", "flake.annotate_gha", "true", "When true, emit ::warning annotations and flake summary to $GITHUB_STEP_SUMMARY"},
		{"MAGUS_REPORT_FILTER", "report.filter", "", "Comma-separated +type/-type terms restricting JSONL event emission (e.g. -graph.build,-graph.query)"},
		{"MAGUS_SANDBOX_ENABLED", "sandbox.enabled", "false", "When 1 or true, confine every subprocess and in-process spell to the workspace + a curated allowlist, scrub the child-process env to a minimum allowlist, and refuse paths outside it. See magus.yaml sandbox.allow and sandbox.env.passthrough for extension"},
	}
}

// Defaults returns a Config populated with the magus built-in defaults.
func Defaults() Config {
	return Config{
		CI:          CI{MaxShards: 8},
		HistoryPath: DefaultHistoryPath(),
		Flake: Flake{
			Enabled:          true,
			BootstrapSamples: 20,
			MinSamples:       20,
			Threshold:        0.05,
			AnnotateGHA:      true,
		},
		Health: Health{},
		Hints:  Hints{Enabled: boolPtr(true)},
		Telemetry: Telemetry{
			Protocol:    "grpc",
			ServiceName: "magus",
			SampleRatio: 1.0,
		},
	}
}
