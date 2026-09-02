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
	Cache      Cache      `json:"cache" yaml:"cache"`
	CI         CI         `json:"ci" yaml:"ci"`
	Volatility Volatility `json:"volatility" yaml:"volatility"`
	Watch      Watch      `json:"watch" yaml:"watch"`
	Telemetry  Telemetry  `json:"telemetry" yaml:"telemetry"`
	Daemon     Daemon     `json:"daemon" yaml:"daemon"`
	VCS        VCS        `json:"vcs" yaml:"vcs"`
	MCP        MCP        `json:"mcp" yaml:"mcp"`
	Console    Console    `json:"console" yaml:"console"`
	Report     Report     `json:"report" yaml:"report"`
	Log        Log        `json:"log" yaml:"log"`
	Hints      Hints      `json:"hints" yaml:"hints"`
	Knowledge  Knowledge  `json:"knowledge" yaml:"knowledge"`
	Secret     Secret     `json:"secret" yaml:"secret"`
	Diff       Diff       `json:"diff" yaml:"diff"`

	// Concurrency caps concurrent builds; top-level and in-process fan-out share one limiter. Defaults to min(NumCPU, 8).
	Concurrency int `json:"concurrency" yaml:"concurrency" validate:"gte=0" cli:"short=j"`

	// MaxFailures bounds how many projects may fail before a run stops starting
	// more. Zero, the default, is unlimited: a batch runs everything it can and
	// reports every failure at once.
	//
	// A budget rather than a boolean, so one setting covers what other tools split
	// across a pair of flags: 1 is fail-fast, 3 tolerates three before giving up.
	//
	// Keeping going is the default because a gate over many projects is asked "what
	// is broken", and stopping at the first failure answers "something is" - one
	// failure per invocation, each fix costing another full run to find the next.
	//
	// Projects that depended on a failure still stop. That is not this setting's
	// doing and cannot be turned off; their work is genuinely invalid.
	MaxFailures int `json:"max_failures" yaml:"max_failures" validate:"gte=0"`

	// TargetTimeout bounds how long any single target may run before magus
	// cancels it. Zero, the default, means no limit.
	//
	// A runaway guard, not a performance budget: a magusfile is code, so a
	// non-terminating loop is writable by accident and nothing else reclaims a
	// CI runner that hit one.
	//
	// It bounds the WHOLE target, subprocesses included. Set it ABOVE your
	// slowest legitimate target, not near it - off by default because a wrong
	// value here fails builds that were fine.
	TargetTimeout time.Duration `json:"target_timeout" yaml:"target_timeout"`

	// HistoryPath is the path to the runtime-history JSON used by volatility detection,
	// CI forecaster, graph timing, and bisect. Defaults to $XDG_STATE_HOME/magus/history/v1.json.
	HistoryPath string `json:"history_path" yaml:"history_path"`

	// DryRun prints what would run without executing. Equivalent to MAGUS_DRY_RUN=1.
	DryRun bool `json:"dry_run" yaml:"dry_run" cli:"short=u"`

	// DefaultCharms are execution charms applied to every `magus run` / `magus x` by
	// default, e.g. ["rw"] to make targets write locally without typing :rw. Per-run
	// :charms stack on top. The ci anchor still strips "rw" (RunCI), so a local
	// `magus run ci` stays read-only; --no-default-charms ignores these for one run.
	// `magus affected` does not apply them, so CI stays read-only unless explicit.
	DefaultCharms []string `json:"default_charms" yaml:"default_charms"`

	// Sandbox confines subprocesses and spells to the workspace + allowlist using Linux landlock (≥5.13)
	// when available, with binding-level fallback. See SandboxConfig for allowlist and env knobs.
	Sandbox SandboxConfig `json:"sandbox" yaml:"sandbox"`

	// Spells configures workspace spell resolution (the import walk and its wards).
	Spells SpellsConfig `json:"spells" yaml:"spells"`

	// RequiredVersion is the oldest magus that can run this workspace, as a semver
	// constraint (">= 0.4.0", "^0.4"). Empty means no floor.
	//
	// The binary that hits the problem is the OLD one, and it cannot be told which
	// release added the key it is choking on - it has never heard of that release. A
	// declared minimum is the only thing it can evaluate against a future it does not
	// know, which is why Terraform's required_version and Go's `go` directive take
	// this shape. Checked before any magusfile is evaluated; see MGS1021.
	//
	// cli:"-" - magus.yaml ONLY, deliberately. Config layering exists to let a caller
	// override the workspace, and a floor is the one field where that is backwards:
	// it protects the caller from a binary too old to read the workspace, so an
	// override switches off a check whose job is to stop you. An env var would be
	// worse than a flag - one MAGUS_REQUIRED_VERSION exported in a CI environment
	// would silently disable the floor for every workspace that session touches.
	RequiredVersion string `json:"required_version" yaml:"required_version" cli:"-"`
}

// Diff configures `magus diff`.
type Diff struct {
	// Tui opens the interactive viewer when the terminal can draw it. nil = default true.
	//
	// Default ON because the viewer is the same report plus navigation - it renders the same
	// annotation lines, from the same diffFileFacts the text mode uses - and a reader who has
	// to know a flag exists before they can step through a changeset mostly never does.
	//
	// Turning it off is a preference, not a workaround: `magus diff --no-tui` for one run, this
	// for every run. Neither is needed to make the command scriptable - the viewer already
	// stands aside on its own for anything that is not a person at a terminal.
	Tui *bool `json:"tui" yaml:"tui"`
}

// TuiEnabled reports whether `magus diff` may open the viewer.
func (d Diff) TuiEnabled() bool { return d.Tui == nil || *d.Tui }

// SpellsConfig holds workspace-level spell settings.
type SpellsConfig struct {
	// AllowShadow acknowledges intentional spell shadows. Spell imports resolve
	// root-wins (a spells/<name> higher in the tree is canonical), so a same-named
	// spell in a nested project is normally a dead footgun and blocks the run. Listing
	// its import path here with a reason permits the shadow deliberately. `magus
	// doctor` flags an entry whose shadow no longer exists, so stale reasons are pruned.
	AllowShadow []ShadowAck `json:"allow_shadow" yaml:"allow_shadow"`
}

// ShadowAck acknowledges one intentional spell shadow. Name is the import path the
// shadow is keyed by (e.g. "spells/hello"); Reason is required so the intent is
// auditable, matching the acknowledged-suppression pattern used elsewhere.
type ShadowAck struct {
	Name   string `json:"name" yaml:"name"`
	Reason string `json:"reason" yaml:"reason" validate:"required"`
}

// SandboxConfig is the per-workspace sandbox policy.
type SandboxConfig struct {
	Enabled bool               `json:"enabled" yaml:"enabled"` // master switch; equivalent to MAGUS_SANDBOX_ENABLED=1
	Allow   []SandboxAllowPath `json:"allow" yaml:"allow"`     // extra {path, mode} entries extending the filesystem allowlist
	Env     SandboxEnv         `json:"env" yaml:"env"`         // env-var passthrough rules
}

// SandboxAllowPath is one extra filesystem allowlist entry. Mode is "ro" or "rw"; other values emit MGS2004.
type SandboxAllowPath struct {
	// Name is a free-form label for the entry. It is ignored by the sandbox; it
	// exists so `magus config set sandbox.allow.<name>.path=…` can address the
	// entry by name (the same convention used for other slice-of-struct config).
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
	Path string `json:"path" yaml:"path"`
	Mode string `json:"mode" yaml:"mode" validate:"omitempty,oneof=ro rw"`
}

// SandboxEnv controls per-child env passthrough when the sandbox is active.
type SandboxEnv struct {
	// Passthrough adds names/globs (e.g. "MISE_*") to the built-in env allowlist.
	Passthrough []string `json:"passthrough" yaml:"passthrough"`
}

// Log controls log output.
type Log struct {
	Format string `json:"format" yaml:"format" validate:"omitempty,oneof=pretty plain text json"` // pretty|plain|text|json
	// Level is the minimum log level; "trace" also enables the startup timing table.
	Level string `json:"level" yaml:"level" validate:"omitempty,oneof=trace debug info warn error"`
	// Silent suppresses progress like --quiet, and additionally bounds the failing-project
	// dump (tail + path to the full log) and bubbles up only lines a target marks as a
	// notice ("magus:notice:"). Normally set via -s/--silent; MAGUS_LOG_SILENT=1 is the env equivalent.
	// Pointer to distinguish "not set" from explicit false.
	Silent *bool `json:"silent" yaml:"silent"`
	// Stream shows every target's subprocess output live and interleaved, instead of
	// withholding a passing target's output until it fails. This is deliberately NOT
	// derived from Level: what gets logged and whether a target's own output is
	// withheld are separate questions, and conflating them is why -v used to ambush
	// people with a wall of concurrent output when they wanted a little more detail.
	// Normally set via -vv (and implied by -vvv); MAGUS_LOG_STREAM=1 is the env equivalent.
	// Pointer to distinguish "not set" from explicit false.
	Stream *bool `json:"stream" yaml:"stream"`
}

// IsSilent reports whether silent output mode is enabled.
func (l Log) IsSilent() bool { return l.Silent != nil && *l.Silent }

// IsStream reports whether target output is streamed live rather than withheld
// until failure.
func (l Log) IsStream() bool { return l.Stream != nil && *l.Stream }

// Hints controls whether hint messages are emitted to the user.
type Hints struct {
	// Enabled controls whether hint messages (actionable nudges) are printed
	// to stderr. Defaults to true. Set hints.enabled: false in magus.yaml or
	// MAGUS_HINTS_ENABLED=false to suppress all hint output.
	// Pointer to distinguish "not set" from explicit false.
	Enabled *bool `json:"enabled" yaml:"enabled"`
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
	Dir string `json:"dir" yaml:"dir"` // override default cache location (.magus/ in workspace root)
	// Write gates producing entries. It replaces a cache.immutable flag that named the
	// absence of the behavior: answering "can this run write?" meant parsing a double
	// negative, and the CI snippet that used it read inverted from its own intent.
	Write   CacheWrite   `json:"write" yaml:"write"`
	Include CacheInclude `json:"include" yaml:"include"`
	SizeMB  int          `json:"size_mb" yaml:"size_mb" validate:"gte=0"` // disk cap in MB (binary); 0 = unlimited
	Remote  CacheRemote  `json:"remote" yaml:"remote"`                    // settings specific to a shared remote cache backend
}

// CacheWrite gates writing entries: the local snapshot and the remote push alike.
//
// One flag covers both because they are one decision today - a run either produces
// entries or it does not. Restoring is deliberately NOT gated: a read-only run still
// populates its local cache from a remote hit, so a pull request replays the shared
// cache at full speed while publishing nothing to it.
type CacheWrite struct {
	Enabled *bool `json:"enabled" yaml:"enabled"` // nil = default true
}

// CacheInclude selects which facts about the host enter every cache key.
//
// OS and architecture are separate because they move independently: a container image
// built on linux/amd64 differs from linux/arm64 by ARCH alone, while a shell test suite
// differs between macOS and linux by OS alone. One combined switch would make a target
// that cares about one pay for both.
type CacheInclude struct {
	OS   CacheIncludeFlag `json:"os" yaml:"os"`
	Arch CacheIncludeFlag `json:"arch" yaml:"arch"`
}

// CacheIncludeFlag is one host fact's switch. Both default to DISABLED, and neither
// holds correctness: Manifest.Platform refuses a cross-platform replay whatever these
// say, so on one machine they are constants that discriminate nothing. Keeping them out
// is what lets an output ref name the same run on every machine. Turn one on for a cache
// shared across platforms, where one key per platform beats every platform colliding on
// one key and taking the guard's miss.
type CacheIncludeFlag struct {
	Enabled *bool `json:"enabled" yaml:"enabled"` // nil = default false
}

// WriteEnabled reports whether this run may produce cache entries.
func (c Cache) WriteEnabled() bool { return c.Write.Enabled == nil || *c.Write.Enabled }

// IncludeOS reports whether the host OS keys every entry.
func (c Cache) IncludeOS() bool { return c.Include.OS.Enabled != nil && *c.Include.OS.Enabled }

// IncludeArch reports whether the host architecture keys every entry.
func (c Cache) IncludeArch() bool { return c.Include.Arch.Enabled != nil && *c.Include.Arch.Enabled }

// CacheRemote holds settings that apply only to a remote cache backend (wired via
// magus.cache.remote in the magusfile). The backend binding is code, so it stays
// in the magusfile; everything here is declarative policy.
type CacheRemote struct {
	TrustedKeys []string `json:"trusted_keys" yaml:"trusted_keys"` // base64 Ed25519 public keys a remote artifact must be signed by; required when a backend is wired
	// Insecure disables remote-cache signature verification: unsigned artifacts are
	// imported and produced with no trust set. A shared cache without signing is a
	// supply-chain hazard — use only for trusted single-repo CI, or to validate a
	// backend before minting keys. When true, trusted_keys stops being required and
	// InsecureReason starts.
	Insecure bool `json:"insecure" yaml:"insecure"`
	// InsecureReason is the prose behind Insecure, required whenever it is true. Same
	// rule SkipCacheReason and DriftReason carry: switching off the check that says
	// whether an artifact came from who it claims is a claim about this cache. This
	// file is committed, so the next machine to trust the cache reads the answer here.
	InsecureReason string `json:"insecure_reason,omitempty" yaml:"insecure_reason,omitempty"`
}

// CI controls CI fan-out behavior.
type CI struct {
	MaxShards        int `json:"max_shards" yaml:"max_shards" validate:"shard_count"`           // max parallel shards; -1 = unlimited
	RunnerPoolBudget int `json:"runner_pool_budget" yaml:"runner_pool_budget" validate:"gte=0"` // GHA matrix-level concurrency cap; 0 = no cap
	// RecordRuns keeps the per-branch run log (forecast.Run) in the history file:
	// which commit a branch passed or failed a target at, and when. On by default,
	// because `--base last-passed` reads it and a CI run that cannot find its base
	// gates less than it appears to.
	//
	// It is the break-glass switch for the one exception in history.go's cache-safety
	// notice. That log is the only part of the history carrying a commit id or a
	// branch name, so a workspace whose policy forbids either leaving the repository -
	// however scoped the CI cache is - turns this off and gives up only the last-passed
	// base, keeping every timing and volatility field. Off, magus records nothing and
	// `--base last-passed` says so loudly rather than resolving to something arbitrary.
	RecordRuns bool `json:"record_runs" yaml:"record_runs"`
}

// Volatility controls volatility detection and auto-retry for test runs.
type Volatility struct {
	Enabled          bool    `json:"enabled" yaml:"enabled"`
	BootstrapSamples int     `json:"bootstrap_samples" yaml:"bootstrap_samples" validate:"gte=0"` // outcomes below which all failures retry once
	MinSamples       int     `json:"min_samples" yaml:"min_samples" validate:"gte=0"`             // minimum outcomes before Wilson-score gates retry
	Threshold        float64 `json:"threshold" yaml:"threshold" validate:"gte=0,lte=1"`           // Wilson lower-bound above which a project+target is volatile
	AnnotateGHA      bool    `json:"annotate_gha" yaml:"annotate_gha"`                            // emit ::warning annotations and GITHUB_STEP_SUMMARY table
}

// Watch controls magus watch defaults.
type Watch struct {
	// Ignore adds patterns (glob or {type,pattern}) beyond workspace builtins and --ignore flags.
	Ignore []types.IgnorePattern `json:"ignore" yaml:"ignore" validate:"dive"`
}

// Secret bounds how long magus waits on a secret provider.
//
// Two budgets rather than one, because they answer different questions. Interactive is
// "how long will you hold the build open for a person to complete an unlock" - long
// enough to find your phone. Unattended is "how long before concluding nobody is coming",
// and it is short on purpose: with no terminal a provider that would prompt cannot, so
// waiting past the point where a cached session would have answered only delays a failure
// that is already certain. Ten seconds of a CI job beats forty-five minutes of one.
type Secret struct {
	// Interactive bounds a provider read when stdin is a terminal. Default 60s.
	Interactive time.Duration `json:"interactive_timeout" yaml:"interactive_timeout"`
	// Unattended bounds a provider read with no terminal to prompt on. Default 10s.
	Unattended time.Duration `json:"unattended_timeout" yaml:"unattended_timeout"`
}

// MCP controls the Model Context Protocol server.
type MCP struct {
	Enabled *bool  `json:"enabled" yaml:"enabled"`                                  // pointer distinguishes unset from explicit false
	Address string `json:"address" yaml:"address" validate:"omitempty,mcp_address"` // host:port; default 127.0.0.1:7391
}

// Console controls the console service. The console mounts read-only GET endpoints on the MCP
// HTTP server (/api/v1/graph, /api/v1/events) plus the typed StatusService, so a browser running
// the hosted Graph Explorer can read the current workspace, plus the bearer-gated magus.job.v1alpha1 JobService
// for triggering maintenance jobs (the daemon's one mutating surface). Loopback only; bearer auth.
type Console struct {
	Enabled *bool `json:"enabled" yaml:"enabled"` // pointer distinguishes unset from explicit false; default true when MCP is up
}

// Daemon controls the proc server's listen address and multi-workspace behavior.
type Daemon struct {
	// Enabled uses a shared, persistent daemon; false runs each invocation self-contained. Default true.
	// The shared daemon is the one from `magus server start`; with Enabled false an
	// invocation never discovers or adopts it and hosts its own per-process pool.
	// Recursive magus calls still forward over a per-process socket to share the
	// concurrency budget - only the SHARED daemon is opted out of.
	Enabled bool `json:"enabled" yaml:"enabled"`
	// Address is the unix:// socket the parent listens on; empty auto-generates one.
	Address string `json:"address" yaml:"address" validate:"omitempty,magus_endpoint"`
	// IdleTTL controls workspace eviction in the multi-workspace daemon; 0 = default 6h.
	IdleTTL time.Duration `json:"idle_ttl" yaml:"idle_ttl"`
	// Workspaces is the explicit list of workspace roots to serve; non-empty enables eager union of sandbox
	// policies and rejects out-of-list workspaces (MGS2010).
	Workspaces []string `json:"workspaces" yaml:"workspaces"`
	// Maintenance configures the daemon's built-in background maintenance scheduler.
	Maintenance Maintenance `json:"maintenance" yaml:"maintenance"`
}

// Maintenance sets how often the daemon runs each low-key background maintenance job on its own.
// Each field is the MINIMUM interval since that job last ran before the daemon runs it again;
// the run is idle-gated (only when the pool is quiet) and submitted through the same coalescing
// path as a manual run, so the two never double-run. "Last run" is read from the activity trail,
// so a manual trigger through any path (CLI or RPC) resets the countdown, and the schedule
// survives a daemon restart. A zero (or negative) interval disables that job's scheduling.
// clear-cache is intentionally absent: wiping the cache is user-triggered only, never scheduled.
type Maintenance struct {
	// RotateActivities is how often the daemon trims the activity trail.
	//
	// It is that trail's only bound, so this job owns rotation outright, and it runs hourly
	// because a rotate on an already-small trail costs one stat. A write-triggered rotate
	// running off a producer's own append counter cannot share the job: a counter only bounds
	// the producer that owns it, and an agent hook is a short-lived process with nowhere to
	// keep one, so a hook-fed trail would be bounded by nothing at all.
	RotateActivities time.Duration `json:"rotate_activities" yaml:"rotate_activities"` // trim the activity trail; default 1h (its only bound)
	RotateLogs       time.Duration `json:"rotate_logs" yaml:"rotate_logs"`             // trim the run-log journals; default 7d (their only bound, so weekly)
	SyncGraph        time.Duration `json:"sync_graph" yaml:"sync_graph"`               // reconcile the graph; default 6h (a safety net behind the VCS hook)
	// CheckReview notices a merge or a new remark on a review this tree took part in. The only
	// scheduled job that leaves the machine, so its default is the longest here: a pull request
	// merges once, and a remark waiting fifteen minutes costs nobody anything.
	CheckReview time.Duration `json:"check_review" yaml:"check_review"`
}

// VCS controls VCS-driven affected detection.
type VCS struct {
	Enabled *bool  `json:"enabled" yaml:"enabled"` // false = fall back to all projects; pointer distinguishes unset
	Name    string `json:"name" yaml:"name"`       // pin VCS by name (git/hg/sl/jj); empty = autodetect
	// BaseRef sets the default base ref. Per-VCS overrides use MAGUS_VCS_<NAME>_BASE_REF (dynamic; not a Config field).
	BaseRef string `json:"base_ref" yaml:"base_ref"`
}

// Knowledge configures the cross-workspace knowledge graph.
type Knowledge struct {
	// Workspaces are additional workspace roots to union into a `--global`
	// knowledge-graph query (query/explain/path and graph export/stats). Each is
	// loaded and its node IDs are namespaced by the workspace ("<name>//<id>") so
	// IDs from different repos cannot collide. Empty means --global covers only the
	// current workspace.
	Workspaces []string `json:"workspaces" yaml:"workspaces"`
	// MaxSizeMB is a soft cap on the knowledge shard store (<cache>/knowledge). When
	// exceeded after a build, least-recently-used shard files are evicted; an evicted
	// shard is restored from the remote cache or rebuilt on the next query. 0
	// (default) is unlimited - the store self-reconciles deleted projects, so a cap
	// mainly bounds transient bloat.
	MaxSizeMB int `json:"max_size_mb" yaml:"max_size_mb" validate:"gte=0"`
	// Symbols overrides symbol ingestion for specific projects. Ingestion is normally
	// AUTOMATIC: every project bound to a symbol-capable spell (go, ts, py, rust - any
	// spell exposing the reserved `scip` op) is ingested from its cached index with no
	// config here. Each entry below instead points a named project at a
	// workspace-relative .scip path your own build emits, for a project whose index does
	// not come from a magus `scip` op. A declared (or derived) index that does not exist
	// yet (the scip target has not run) is simply skipped, so the shard appears once the
	// index is built.
	Symbols []SymbolIndex `json:"symbols" yaml:"symbols"`
	// VCS enables folding git history metadata (last-commit SHA and time, commit
	// count) onto file nodes as a @vcs shard. Opt-in and best-effort: disabled by
	// default, and a non-git workspace simply yields no shard. The history scan is
	// bounded and cached against HEAD, so it runs at build time on a commit change,
	// never per query.
	VCS KnowledgeVCSConfig `json:"vcs" yaml:"vcs"`
	// SymbolIndexing configures the daemon's background auto-indexing: it runs each
	// symbol-capable project's `scip` op for you when its sources change, so symbols
	// stay fresh with no manual `magus run ::scip`. ON by default in the daemon (a
	// one-shot CLI never auto-indexes); throttled and idle-gated so it never delays
	// your own work. Set disabled to opt out.
	SymbolIndexing SymbolIndexingConfig `json:"symbol_indexing" yaml:"symbol_indexing"`
	// Notes declares where this workspace keeps its human-authored notes (see
	// NotesConfig). Empty (the default) means no notes store at all and every part of the
	// feature is inert.
	Notes NotesConfig `json:"notes" yaml:"notes"`
}

// NotesConfig declares where human-authored notes live. There are two locations because
// there are two audiences, and the pair is one half of a 2x2 the whole knowledge surface
// turns on:
//
//	                    the team sees it        only you see it
//	a person wrote it   notes.shared            notes.private
//	an agent wrote it   (deliberately nothing)  magus memory
//
// The empty cell is the design, not a gap: an agent's derived claims are never pushed at
// the team, which is the same rule the guard enforces by refusing agent writes to either
// notes location.
//
// A note is prose a PERSON wrote about the code, anchored to graph entities but derived
// from none of them - the one node class magus cannot regenerate, because its only
// provenance is the author. Both locations are DECLARED rather than assumed: magus judges
// writes to them, and a rule fired on a guessed location would act on a workspace that
// never opted in.
type NotesConfig struct {
	// Shared is the workspace-relative directory holding notes the TEAM has: committed,
	// so git attributes each one to whoever wrote it and review sees every change.
	// Empty disables it entirely - no shard, no verification, no guard rule.
	//
	// It must stay inside the workspace. That is not a restriction so much as what the
	// word means here: a note outside the checkout is not shared with anyone.
	Shared string `json:"shared" yaml:"shared"`
	// Private is a SECOND notes location, yours rather than the team's, and it may sit
	// anywhere on disk - a vault, a scratch directory, somewhere outside any repository.
	//
	// It is a separate key rather than a looser Path because the two carry different
	// TRUST, and one key would have silently collapsed the difference. A note under Path
	// is committed, so git attributes it, review sees it, and the shard holding it is safe
	// to share. A personal note has none of that: no attribution is possible outside the
	// repo, nobody reviewed it, and it is on one machine. Presenting the two identically
	// would leave a reader unable to tell "the team agreed this" from "someone wrote this
	// in their vault".
	//
	// Consequences that follow from that and are enforced rather than documented: its
	// shard is never exported to the remote cache (the same rule @memory lives under, for
	// the same reason - private content must not leak into a shared cache), and its nodes
	// carry a scope attr so the distinction is visible wherever they surface.
	//
	// Agents still may not write here. That is the point: it is the vault case.
	//
	// compat(until: `magus notes promote` has replaced this in practice - no workspace here
	// or in the wild sets knowledge.notes.private, and `magus memory` carries the drafting
	// tier instead): SUPERSEDED, still read, no longer the recommended shape.
	//
	// Line the three stores up by property rather than by who types into them and this one
	// has no column of its own: private notes and `magus memory` are both uncommitted,
	// unattributed, unreviewed and unrecoverable. The only thing separating them was who
	// wrote the file, which is a field rather than a store - and one the guard cannot
	// actually enforce, since a person pasting an agent's prose into $EDITOR passes it
	// cleanly. What private bought that memory did not was ANCHORS; `notes promote` closes
	// that by deriving a note's anchors from a record's node refs, so the drafting tier can
	// now graduate into the committed one without a third store in between.
	//
	// Observe that it is safe to drop by checking that nothing sets it: it was never set in
	// this repository, and a store nobody points at holds nothing to lose. Kept readable
	// until then because the path may name someone's vault, and deleting the key would
	// orphan real prose to save a struct field.
	Private string `json:"private" yaml:"private"`
}

// SymbolIndexingConfig tunes daemon background symbol auto-indexing (see
// Knowledge.SymbolIndexing). Zero value = enabled with built-in timings.
type SymbolIndexingConfig struct {
	// Disabled opts out of background auto-indexing. Auto-indexing is on by default
	// in the daemon, so this is the switch to turn it off (e.g. the indexers are not
	// installed and you index in CI instead).
	Disabled bool `json:"disabled" yaml:"disabled"`
	// QuietSeconds is how long a project's sources must be quiet after the last change
	// before it is re-indexed, so a burst of edits coalesces into one run. 0 uses a
	// built-in default.
	QuietSeconds int `json:"quiet_seconds" yaml:"quiet_seconds" validate:"gte=0"`
	// MinIntervalSeconds is the minimum time between re-index runs for one project, a
	// ceiling on how often the indexer fires however often files change. 0 uses a
	// built-in default.
	MinIntervalSeconds int `json:"min_interval_seconds" yaml:"min_interval_seconds" validate:"gte=0"`
}

// KnowledgeVCSConfig configures git-history ingestion into the @vcs shard (see Knowledge.VCS).
type KnowledgeVCSConfig struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
	// MaxCommits bounds the history walk to the most recent N commits. 0 uses a
	// built-in default; a small value keeps the scan fast on a large repo at the cost
	// of undercounting commits for long-lived files.
	MaxCommits int `json:"max_commits" yaml:"max_commits" validate:"gte=0"`
	// Authorship includes the `author` nodes and `authored` edges (who touched which
	// files) in the graph. Defaults to ON (nil = on): authorship is context that helps
	// an agent, and the edges are already bounded by MaxCommits. Set false to keep only
	// the per-file vcs_* attrs and omit the author layer.
	Authorship *bool `json:"authorship" yaml:"authorship"`
}

// SymbolIndex declares one project's SCIP index for symbol ingestion.
type SymbolIndex struct {
	Project string `json:"project" yaml:"project"` // workspace-relative project path the symbols belong to
	Index   string `json:"index" yaml:"index"`     // workspace-relative path to the .scip index file
}

// Telemetry holds OpenTelemetry exporter settings. OFF by default; no magus-operated backend exists.
// When Enabled, magus connects to the OTLP collector you configure and sends data there only.
type Telemetry struct {
	Enabled     bool              `json:"enabled" yaml:"enabled"`
	Endpoint    string            `json:"endpoint" yaml:"endpoint"`                                      // host:port; required when Enabled
	Protocol    string            `json:"protocol" yaml:"protocol" validate:"omitempty,oneof=grpc http"` // "grpc" or "http"
	Insecure    bool              `json:"insecure" yaml:"insecure"`                                      // disable TLS
	ServiceName string            `json:"service_name" yaml:"service_name"`                              // resource attribute service.name
	SampleRatio float64           `json:"sample_ratio" yaml:"sample_ratio" validate:"gte=0,lte=1"`       // head-based sampling ratio; 1.0 = all
	Headers     map[string]string `json:"headers" yaml:"headers"`                                        // static OTLP request headers
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
	Filter []string `json:"filter" yaml:"filter"`
}

func boolPtr(v bool) *bool { return &v }

// EnvVarDocs returns documentation for every MAGUS_* environment variable in declaration order.
func EnvVarDocs() []EnvVarDoc {
	return []EnvVarDoc{
		{"MAGUS_CACHE_DIR", "cache.dir", "", "Override the default cache location (.magus/ in the workspace root)"},
		{"MAGUS_CACHE_WRITE_ENABLED", "cache.write.enabled", "true", "When false (or 0), replay cache hits but never write new entries, locally or to a remote"},
		{"MAGUS_CACHE_INCLUDE_OS_ENABLED", "cache.include.os.enabled", "false", "When true, the host OS keys every cache entry; off by default because a manifest guard already refuses a cross-platform replay"},
		{"MAGUS_CACHE_INCLUDE_ARCH_ENABLED", "cache.include.arch.enabled", "false", "When true, the host architecture keys every cache entry; off by default because a manifest guard already refuses a cross-platform replay"},
		{"MAGUS_CACHE_SIZE_MB", "cache.size_mb", "0", "Cache disk usage cap in MB (binary, 1<<20); 0 means unlimited"},
		{"MAGUS_CACHE_REMOTE_INSECURE", "cache.remote.insecure", "false", "Disable remote-cache signature verification (accept/produce unsigned artifacts); for trusted single-repo CI only. Requires cache.remote.insecure_reason"},
		{"MAGUS_CACHE_REMOTE_INSECURE_REASON", "cache.remote.insecure_reason", "", "Why this cache runs unverified; required whenever cache.remote.insecure is true"},
		{"MAGUS_LOG_FORMAT", "log.format", "pretty", "Output format: pretty, plain, text, or json"},
		{"MAGUS_LOG_LEVEL", "log.level", "info", "Minimum log level: trace, debug, info, warn, error (trace also prints the startup timing table)"},
		{"MAGUS_CONCURRENCY", "concurrency", "min(NumCPU,8)", "Maximum number of concurrently running per-project build steps"},
		{"MAGUS_HISTORY_PATH", "history_path", "$XDG_STATE_HOME/magus/history/v1.json", "Path to the runtime-history JSON shared by volatility detection, the CI forecaster, graph timing, and bisect"},
		{"MAGUS_DRY_RUN", "dry_run", "false", "When 1 or true, print what would run without executing anything"},
		{"MAGUS_DEFAULT_CHARMS", "default_charms", "", "Comma-separated charms applied to every magus run/x by default (e.g. rw); the ci anchor still strips rw, and --no-default-charms ignores them for one run"},
		{"MAGUS_VCS_ENABLED", "vcs.enabled", "true", "Master switch for VCS-driven affected detection; false makes affected fall back to all projects"},
		{"MAGUS_VCS_NAME", "vcs.name", "", "Pin the active VCS by name (git, hg, sl, jj); empty autodetects from .git/.hg/.sl/.jj"},
		{"MAGUS_VCS_BASE_REF", "vcs.base_ref", "", "Default base ref for the active VCS adapter, e.g. origin/main for git"},
		{"MAGUS_VCS_<NAME>_BASE_REF", "", "", "Per-VCS base-ref override, e.g. MAGUS_VCS_GIT_BASE_REF; dynamic pattern, read directly by package vcs"},
		{"MAGUS_DAEMON_SOCKET", "", "", "Env-only, no magus.yaml equivalent: runtime proc-server socket set by the daemon for forwarded child processes; unix:// URL or bare path, read directly by the process that adopts it"},
		{"MAGUS_CI_MAX_SHARDS", "ci.max_shards", "8", "Maximum number of parallel CI shards; -1 means unlimited"},
		{"MAGUS_CI_RUNNER_POOL_BUDGET", "ci.runner_pool_budget", "0", "Cross-shard concurrency cap at the GHA matrix level; 0 means unlimited"},
		{"MAGUS_SHARD", "", "", "CI matrix shard ID (e.g. \"0\"); equivalent to magus run --shard; set by .github/actions/magus"},
		{"MAGUS_N_SHARDS", "", "", "Total shard count for this matrix run; equivalent to magus run --n-shards; set by .github/actions/magus"},
		{"MAGUS_TELEMETRY_ENABLED", "telemetry.enabled", "false", "Turn OTLP export on; magus connects to telemetry.endpoint when true"},
		{"MAGUS_TELEMETRY_ENDPOINT", "telemetry.endpoint", "", "OTLP collector address as host:port (no scheme); required when telemetry is enabled"},
		{"MAGUS_TELEMETRY_PROTOCOL", "telemetry.protocol", "grpc", "OTLP wire protocol: grpc or http"},
		{"MAGUS_TELEMETRY_INSECURE", "telemetry.insecure", "false", "Disable TLS for the OTLP exporter (plaintext local-collector setups)"},
		{"MAGUS_TELEMETRY_SERVICE_NAME", "telemetry.service_name", "magus", "Value of the resource attribute service.name on emitted spans/metrics"},
		{"MAGUS_TELEMETRY_SAMPLE_RATIO", "telemetry.sample_ratio", "1.0", "Head-based trace sampling ratio in [0,1]"},
		{"MAGUS_DAEMON_ADDRESS", "daemon.address", "", "Adopt-server socket as a unix:// URL; empty auto-generates a per-process socket"},
		{"MAGUS_DAEMON_IDLE_TTL", "daemon.idle_ttl", "6h", "Idle workspace eviction TTL for the multi-workspace daemon; e.g. \"6h\", \"30m\""},
		{"MAGUS_DAEMON_WORKSPACES", "daemon.workspaces", "", "Colon-separated list of workspace roots the daemon will serve; non-empty list triggers eager union of sandbox policies and rejection of out-of-list workspaces (MGS2010)"},
		{"MAGUS_MCP_ENABLED", "mcp.enabled", "true", "When 0 or false, refuse to start the MCP server"},
		{"MAGUS_MCP_ADDRESS", "mcp.address", "127.0.0.1:7391", "host:port for the MCP Streamable HTTP server started alongside the daemon"},
		{"MAGUS_HINTS_ENABLED", "hints.enabled", "true", "When false, suppress all hint messages printed to stderr"},
		{"MAGUS_VOLATILITY_ENABLED", "volatility.enabled", "true", "Master switch for volatility detection and auto-retry; false disables all retry logic"},
		{"MAGUS_VOLATILITY_BOOTSTRAP_SAMPLES", "volatility.bootstrap_samples", "20", "Number of outcomes below which all failures are retried once (bootstrap phase)"},
		{"MAGUS_VOLATILITY_MIN_SAMPLES", "volatility.min_samples", "20", "Minimum outcomes required before Wilson-score volatility rate gates retry decisions"},
		{"MAGUS_VOLATILITY_THRESHOLD", "volatility.threshold", "0.05", "Wilson lower-bound volatility rate above which a project+target is considered volatile"},
		{"MAGUS_VOLATILITY_ANNOTATE_GHA", "volatility.annotate_gha", "true", "When true, emit ::warning annotations and volatility summary to $GITHUB_STEP_SUMMARY"},
		{"MAGUS_REPORT_FILTER", "report.filter", "", "Comma-separated +type/-type terms restricting JSONL event emission (e.g. -graph.build,-graph.query)"},
		{"MAGUS_SANDBOX_ENABLED", "sandbox.enabled", "false", "When 1 or true, confine every subprocess and in-process spell to the workspace + a curated allowlist, scrub the child-process env to a minimum allowlist, and refuse paths outside it. See magus.yaml sandbox.allow and sandbox.env.passthrough for extension"},
		{"MAGUS_UPDATE_URL", "", "https://eli.gladman.cc/magus/public/release/index.json", "Env-only, no magus.yaml equivalent: override the release index URL for `magus self update`; set to a self-hosted copy of index.json to use a private update channel"},
		{"MAGUS_NO_WAIT", "", "false", "Env-only, no magus.yaml equivalent: when 1, true or yes, a run that finds a project's workspace lock held by another magus process fails immediately instead of queuing behind it, naming the holder and exiting 75 (EX_TEMPFAIL) so a caller can tell a busy machine from a broken build"},
	}
}

// Defaults returns a Config populated with the magus built-in defaults.
func Defaults() Config {
	return Config{
		CI: CI{MaxShards: 8, RecordRuns: true},
		Daemon: Daemon{
			Enabled: true,
			Maintenance: Maintenance{
				RotateActivities: time.Hour,          // the trail's only bound; cheap to run often (one stat when small)
				RotateLogs:       7 * 24 * time.Hour, // run-logs have no other bound, so trim weekly
				SyncGraph:        6 * time.Hour,      // safety net behind the VCS refresh hook
				CheckReview:      15 * time.Minute,   // the only one that reaches a forge; a merge happens once
			},
		},
		HistoryPath: DefaultHistoryPath(),
		// Kept in step with secret.DefaultTimeouts, which applies when a Resolver is built
		// without options (tests, and any caller outside the run path).
		Secret: Secret{
			Interactive: 60 * time.Second, // long enough to find your phone for a biometric
			Unattended:  10 * time.Second, // no terminal: a cached session answers fast or never
		},
		Volatility: Volatility{
			Enabled:          true,
			BootstrapSamples: 20,
			MinSamples:       20,
			Threshold:        0.05,
			AnnotateGHA:      true,
		},
		Hints:     Hints{Enabled: boolPtr(true)},
		Knowledge: Knowledge{VCS: KnowledgeVCSConfig{Authorship: boolPtr(true)}},
		Telemetry: Telemetry{
			Protocol:    "grpc",
			ServiceName: "magus",
			SampleRatio: 1.0,
		},
	}
}
