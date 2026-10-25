// Package config holds the magus configuration schema and yaml-based loader.
// Config is loaded in priority order: defaults → magus.yaml → MAGUS_* env vars → CLI flags.
// Env vars use MAGUS_ prefix + yaml-tag path uppercased (e.g. cache.dir → MAGUS_CACHE_DIR).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	queuetypes "github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/libs/cmdline"
	"github.com/egladman/magus/schema"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// Config is the top-level magus configuration.
type Config struct {
	Cache      Cache      `json:"cache" yaml:"cache"`
	CI         CI         `json:"ci" yaml:"ci"`
	Volatility Volatility `json:"volatility" yaml:"volatility"`
	Watch      Watch      `json:"watch" yaml:"watch"`
	Telemetry  Telemetry  `json:"telemetry" yaml:"telemetry"`
	Server     Server     `json:"server" yaml:"server"`
	VCS        VCS        `json:"vcs" yaml:"vcs"`
	MCP        MCP        `json:"mcp" yaml:"mcp"`
	Console    Console    `json:"console" yaml:"console"`
	Report     Report     `json:"report" yaml:"report"`
	Log        Log        `json:"log" yaml:"log"`
	Hints      Hints      `json:"hints" yaml:"hints"`
	Knowledge  Knowledge  `json:"knowledge" yaml:"knowledge"`
	Secret     Secret     `json:"secret" yaml:"secret"`
	Diff       Diff       `json:"diff" yaml:"diff"`
	Jobs       Jobs       `json:"jobs" yaml:"jobs"`
	Queue      Queue      `json:"queue" yaml:"queue"`
	Go         GoConfig   `json:"go" yaml:"go"`

	// Concurrency caps concurrent builds; top-level and in-process fan-out share one limiter. Overrides concurrency_profile when set.
	Concurrency int `json:"concurrency" yaml:"concurrency" validate:"gte=0" cli:"short=j"`

	// ConcurrencyProfile sets the default width relative to the machine: conservative (half the cores), balanced (min(cores, 8), the default) or aggressive (every core). Unset is balanced everywhere; magus reads no environment variable to change this, so CI asks for aggressive explicitly.
	ConcurrencyProfile types.ConcurrencyProfile `json:"concurrency_profile" yaml:"concurrency_profile"`

	// Broker decides whether a run asks the broker, the per-user process holding this host's slots, declared memory and shared services: required (refuse a step when none answers), best-effort (the default: run unarbitrated and say so once), or off (never start or contact one; a run hosts its services itself).
	Broker types.BrokerPolicy `json:"broker" yaml:"broker"`

	// ShutdownGrace bounds how long the broker and the server take to stop after a first SIGTERM. The broker seats nothing new and waits this long for the runs holding claims or services to finish; the server cancels the runs it adopted and waits this long for them to unwind. A second SIGTERM, or a SIGINT to the broker, stops either at once. Zero stops at once on the first signal; negative is an error.
	ShutdownGrace time.Duration `json:"shutdown_grace" yaml:"shutdown_grace" validate:"gte=0"`

	// MaxFailures bounds how many projects may fail before a run stops starting
	// more. Zero, the default, is unlimited: a batch runs everything it can and
	// reports every failure at once.
	//
	// A budget rather than a boolean, so one setting covers what other tools split
	// across a pair of flags: 1 is fail-fast, 3 tolerates three before giving up.
	//
	// Keeping going is the default because a gate over many projects is asked "what
	// is broken", and stopping at the first failure answers "something is", one
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
	// slowest legitimate target, not near it; off by default because a wrong
	// value here fails builds that were fine.
	TargetTimeout time.Duration `json:"target_timeout" yaml:"target_timeout"`

	// StallTimeout aborts an invocation that is making no progress at all: no target
	// has started, finished, or written a line of output for this long while every
	// selected project's lock is still held. Zero, the default, uses the built-in
	// 15-minute window; a negative value turns the watchdog off.
	//
	// It is the complement of TargetTimeout and a target's declared timeout, not a
	// duplicate of either. A ceiling bounds work that runs LONG, and only a target whose
	// author declared a bound; this catches a process that has stopped doing anything,
	// in work no target declared. It is on by default for the same reason: the case it
	// exists for is the one where nobody is watching.
	StallTimeout time.Duration `json:"stall_timeout" yaml:"stall_timeout"`

	// HistoryPath is the path to the runtime-history JSON Lines store used by volatility
	// detection, CI forecaster, graph timing, and bisect. Defaults to
	// $XDG_STATE_HOME/magus/history/history.jsonl.
	HistoryPath string `json:"history_path" yaml:"history_path"`

	// DryRun prints what would run without executing. Equivalent to MAGUS_DRY_RUN=1.
	DryRun bool `json:"dry_run" yaml:"dry_run" cli:"short=u"`

	// DefaultCharms are execution charms applied to every `magus run` / `magus x` by
	// default, e.g. ["rw"] to make targets write locally without typing :rw. Per-run
	// :charms stack on top. The ci anchor still strips "rw" (RunCI), so a local
	// `magus run ci` stays read-only; --no-default-charms ignores these for one run.
	// `magus affected` does not apply them, so CI stays read-only unless explicit.
	DefaultCharms []string `json:"default_charms" yaml:"default_charms"`

	// Sandbox confines spells and the processes magus starts to the workspace and an
	// allowlist. Kernel landlock (Linux 5.13+) enforces it on every file access; without
	// it, magus's own binding checks cover only what goes through a binding.
	// See SandboxConfig for the mode, allowlist and env knobs.
	Sandbox SandboxConfig `json:"sandbox" yaml:"sandbox"`

	// Spells configures workspace spell resolution (the import walk and its wards).
	Spells SpellsConfig `json:"spells" yaml:"spells"`

	// RequiredVersion is the oldest magus that can run this workspace, as a semver
	// constraint (">= 0.4.0", "^0.4"). Empty means no floor.
	//
	// The binary that hits the problem is the OLD one, and it cannot be told which
	// release added the key it is choking on: it has never heard of that release. A
	// declared minimum is the only thing it can evaluate against a future it does not
	// know, which is why Terraform's required_version and Go's `go` directive take
	// this shape. Checked before any magusfile is evaluated; see MGS1021.
	//
	// cli:"-": magus.yaml ONLY, deliberately. Config layering exists to let a caller
	// override the workspace, and a floor is the one field where that is backwards:
	// it protects the caller from a binary too old to read the workspace, so an
	// override switches off a check whose job is to stop you. An env var would be
	// worse than a flag: one MAGUS_REQUIRED_VERSION exported in a CI environment
	// would silently disable the floor for every workspace that session touches.
	RequiredVersion string `json:"required_version" yaml:"required_version" cli:"-"`

	// Agents states facts about the account agents run under and a person's guard level.
	// See Agents.
	//
	// cli:"-": magus.yaml ONLY. A flag would be an AI-specific CLI flag on every verb, and
	// one exported env var would flip billing or the level for every workspace a shell
	// touches without a trace in any file.
	Agents Agents `json:"agents" yaml:"agents" cli:"-"`
}

// GoConfig configures what magus reports about the Go toolchain.
type GoConfig struct {
	// CacheBudgetMB is the size in MB (binary) past which `magus doctor` advises cleaning the Go
	// build cache or the golangci-lint cache. Default 20480; 0 turns the check off.
	//
	// A budget rather than a cap: neither cache is magus's, so doctor measures and names the
	// command that frees it and deletes nothing.
	CacheBudgetMB int `json:"cache_budget_mb" yaml:"cache_budget_mb" validate:"gte=0"`
}

// Queue configures the merge queue, `magus queue`.
type Queue struct {
	// CarryApprovals lists the tiers of change since an approval that leave it standing:
	// rebase, generated, prose and comment-only. A rebase that changed nothing always
	// carries, so [] and [rebase] are the strictest policy; code never carries. Unset is
	// every tier but code.
	//
	// It has no environment variable or flag: the queue reads it from the base branch's
	// own magus.yaml, and an override would loosen review where the base's file is
	// reviewed.
	CarryApprovals CarryApprovals `json:"carry_approvals" yaml:"carry_approvals" merge:"written"`
}

// CarryApprovals is queue.carry_approvals. Decoding refuses a name outside
// [queuetypes.CarryTier.Values], and code, with MGS1050.
type CarryApprovals queuetypes.CarryPolicy

// Policy is the merge queue's policy.
func (c CarryApprovals) Policy() queuetypes.CarryPolicy { return queuetypes.CarryPolicy(c) }

// UnmarshalYAML reports every refused entry, each with its line.
func (c *CarryApprovals) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.SequenceNode {
		return types.DiagnosticErrorf(types.CarryApprovalsInvalid, "line %d: queue.carry_approvals must be a list of tiers", n.Line)
	}
	var problems []string
	out := make(CarryApprovals, 0, len(n.Content))
	for _, item := range n.Content {
		if err := queuetypes.CheckCarryTier(item.Value); item.Kind != yaml.ScalarNode || err != nil {
			if err == nil {
				err = errors.New("a tier is a plain name")
			}
			problems = append(problems, fmt.Sprintf("line %d: queue.carry_approvals: %v", item.Line, err))
			continue
		}
		out = append(out, queuetypes.CarryTier(item.Value))
	}
	if len(problems) > 0 {
		return types.DiagnosticErrorf(types.CarryApprovalsInvalid, "%s", strings.Join(problems, "\n"))
	}
	*c = out
	return nil
}

// Diff configures `magus diff`.
type Diff struct {
	// Tui opens the interactive viewer when the terminal can draw it. nil = default true.
	//
	// Default ON because the viewer is the same report plus navigation (it renders the same
	// annotation lines, from the same diffFileFacts the text mode uses), and a reader who has
	// to know a flag exists before they can step through a changeset mostly never does.
	//
	// Turning it off is a preference, not a workaround: `magus diff --no-tui` for one run, this
	// for every run. Neither is needed to make the command scriptable: the viewer already
	// stands aside on its own for anything that is not a person at a terminal.
	Tui *bool `json:"tui" yaml:"tui"`
}

// Jobs bounds the job tree `magus job fork` declares. Every limit is unset by default, and
// unset means no limit: a bound belongs to the workspace that chose it, never to magus.
// StaleAfter and Retain are the exceptions, because they decide when magus ends a row
// nobody took and when it drops one that is over. So are MaxClaimFiles, MaxClaimProjects and
// MaxClaims, which are never zero: a claim has to be bounded by something.
type Jobs struct {
	// MaxDepth is how many levels below its root job a fork may land. 0 = unlimited.
	MaxDepth int `json:"max_depth" yaml:"max_depth" validate:"gte=0"`
	// MaxLive is how many live jobs one root's tree may hold at once, the new one included.
	// 0 = unlimited.
	MaxLive int `json:"max_live" yaml:"max_live" validate:"gte=0"`
	// DefaultTimeout bounds a fork that names no --timeout. Zero = no bound.
	DefaultTimeout time.Duration `json:"default_timeout" yaml:"default_timeout"`
	// StaleAfter ends a declared job nobody took and nobody updated for this long, and flags
	// a taken one in `magus ls jobs` and `magus doctor`. Default 2h; 0 = never. Ending a row
	// reads the workspace's own magus.yaml, never user-global config or the environment.
	StaleAfter time.Duration `json:"stale_after" yaml:"stale_after" validate:"gte=0" merge:"written"`
	// Retain is how long a job that is over (graded, ended, or exited with its result never
	// collected), and the archive a clear or a delete wrote, stay in the job store once
	// nothing touches them. Default 168h; 0 keeps them forever. Like StaleAfter it reads the
	// workspace's own magus.yaml only.
	Retain time.Duration `json:"retain" yaml:"retain" validate:"gte=0" merge:"written"`
	// MaxValidations is how many `magus run`s one job tree's leased workers may have going at
	// once; a run past it waits for a seat and says whose run holds it. A run with no lease
	// (the root, a person) is never counted. Default 1; 0 = unlimited. Workspace only, like
	// StaleAfter.
	MaxValidations int `json:"max_validations" yaml:"max_validations" validate:"gte=0" merge:"written"`
	// MaxClaimFiles is how many files one glob write path may match before a job record
	// refuses it as too broad (MGS3043). Default 50; zero or negative is an error. A sweep
	// that needs more keeps the path with a deviation. Workspace only, like StaleAfter.
	MaxClaimFiles int `json:"max_claim_files" yaml:"max_claim_files" validate:"gt=0" merge:"written"`
	// MaxClaimProjects is how many projects one glob write path may reach before a job
	// record refuses it as too broad (MGS3043). Default 1; zero or negative is an error.
	// Workspace only, like StaleAfter.
	MaxClaimProjects int `json:"max_claim_projects" yaml:"max_claim_projects" validate:"gt=0" merge:"written"`
	// IntegrateAdviseAt is how many finished branches may wait off the base before a fork of
	// anything but an integration job advises forking one. Default 5; workspace only.
	IntegrateAdviseAt int `json:"integrate_advise_at" yaml:"integrate_advise_at" validate:"gt=0" merge:"written"`
	// IntegrateRefuseAt is the count past which such a fork is refused until an integration
	// job names the branches; it must exceed IntegrateAdviseAt. Default 10; workspace only.
	IntegrateRefuseAt int `json:"integrate_refuse_at" yaml:"integrate_refuse_at" validate:"gtfield=IntegrateAdviseAt" merge:"written"`
	// MaxClaims is the most proved claims any job may be given through its
	// max_claims; a fork or a claim past it is refused. Default 10; zero or negative is an
	// error. Workspace only, like StaleAfter.
	MaxClaims int `json:"max_claims" yaml:"max_claims" validate:"gt=0" merge:"written"`
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
	// Registries are the credentials `magus spell` verbs present to a container
	// registry, one entry per host. A registry with no entry is reached anonymously.
	Registries []SpellRegistry `json:"registries" yaml:"registries" validate:"unique=Host,dive"`
	// Imports declares spells by the path a magusfile imports them under, so an entry
	// reads like the import it answers:
	//
	//	spells:
	//	  ghcr.io/team/spells/lint:
	//	    tag: "1.4"            # a remote spell; magus.lock pins its digest
	//	  magus/spell/go:
	//	    path: spells/go       # a workspace copy replaces the embedded spell
	//
	// Inline beside allow_shadow and registries rather than under a key of its own; a
	// spell path always has a slash, so it can never be mistaken for either.
	Imports map[string]SpellImport `json:",inline" yaml:",inline"`
}

// SpellImport declares one spell import path. Exactly one field is set: Tag for a
// remote spell, which only the update charm resolves, or Path for a workspace
// directory that replaces an embedded or remote spell, the way Go's replace does.
type SpellImport struct {
	Tag  string `json:"tag,omitempty" yaml:"tag,omitempty"`
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
}

// SpellRegistry authenticates to one registry host. Password is a secret REFERENCE,
// never the value: it resolves through the workspace's selected secret provider
// exactly as magus\secret.read does, so under the built-in provider it names an
// environment variable (GITHUB_TOKEN for ghcr.io in Actions), and under a provider
// spell it is that provider's own path.
type SpellRegistry struct {
	// Host is the registry as a reference spells it: "ghcr.io", "localhost:5000".
	Host     string `json:"host" yaml:"host" validate:"required,registry_host"`
	Username string `json:"username" yaml:"username" validate:"required"`
	Password string `json:"password" yaml:"password" validate:"required"`
}

// Registry returns the entry for host, and whether there is one.
func (s SpellsConfig) Registry(host string) (SpellRegistry, bool) {
	for _, r := range s.Registries {
		if r.Host == strings.ToLower(host) {
			return r, true
		}
	}
	return SpellRegistry{}, false
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
	// Mode is off (the default), best-effort (landlock confines each child where the host has it, magus's own binding checks where it does not), or required (refuse to run, MGS2012, unless the kernel can confine every child: landlock ABI 3).
	//
	// MAGUS_SANDBOX is a floor, not an override: a sandboxed run hands its children its
	// mode there, and a nested workspace's own mode may raise it and never lower it.
	Mode types.SandboxMode `json:"mode" yaml:"mode" cli:"name=sandbox,floor"`
	// Allow and Env are the workspace layer of the sandbox declaration a spell's
	// mgs_getSandbox and a target's `sandbox` policy also make, in the same shape; the
	// three merge, and none narrows another.
	Allow []spells.SandboxAllow `json:"allow" yaml:"allow"` // extra entries extending the filesystem allowlist
	Env   SandboxEnv            `json:"env" yaml:"env"`     // env-var passthrough rules
}

// Declaration is the workspace layer as the declaration shape the other layers use.
func (c SandboxConfig) Declaration() spells.Sandbox {
	return spells.Sandbox{Allow: c.Allow, Env: spells.SandboxEnv{Passthrough: c.Env.Passthrough}}
}

// SandboxEnv controls per-child env passthrough when the sandbox is active.
type SandboxEnv struct {
	// Passthrough adds exact names and prefix patterns such as MISE_* to the built-in
	// env allowlist. A prefix must be at least three characters and end in "_".
	Passthrough []string `json:"passthrough" yaml:"passthrough"`
}

// Log controls log output.
type Log struct {
	// Format is the log format: pretty, plain, text or json.
	//
	// "jsonl" is set only by `-o jsonl`, after validation: it withholds target output and routes results to the report stream,
	// which a magus.yaml setting has no stream to route them to.
	Format string `json:"format" yaml:"format" validate:"omitempty,oneof=pretty plain text json"`
	// Level is the minimum log level; "trace" also enables the startup timing table.
	Level string `json:"level" yaml:"level" validate:"omitempty,oneof=trace debug info warn error"`
	// Silent suppresses progress like --quiet, and additionally bounds the failing-project
	// dump (tail + path to the full log) and bubbles up only lines a target marks as a
	// notice ("magus:notice:"). Like --quiet it prints a failure's reasoning only at -v. Normally set via -s/--silent; MAGUS_LOG_SILENT=1 is the env equivalent.
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
	Dir string `json:"dir" yaml:"dir"` // one dir for the entries and the checkout's own files (default: entries shared per repository, the rest in .magus/)
	// Write gates producing entries. It replaces a cache.immutable flag that named the
	// absence of the behavior: answering "can this run write?" meant parsing a double
	// negative, and the CI snippet that used it read inverted from its own intent.
	Write   CacheWrite   `json:"write" yaml:"write"`
	Include CacheInclude `json:"include" yaml:"include"`
	SizeMB  int          `json:"size_mb" yaml:"size_mb" validate:"gte=0"` // disk cap in MB (binary); 0 = unlimited
	Remote  CacheRemote  `json:"remote" yaml:"remote"`                    // settings specific to a shared remote cache backend
}

// CacheWrite gates writing the local cache tier. Off, the remote tier gets nothing
// either, because a remote-tier entry is exported from the local-tier one.
//
// Restoring is deliberately NOT gated: a read-only run still populates its local tier
// from a remote-tier hit.
type CacheWrite struct {
	Enabled *bool `json:"enabled" yaml:"enabled"` // when false, replay cache hits but never write new entries, locally or to a remote (default true)
}

// CacheRemoteWrite gates writing the remote cache tier. Separate from CacheWrite so a
// pull request writes its local tier for its next push and never the remote tier, by
// setting rather than by a missing signing key. Declared true, remote writes are
// required (see cache.WithRemoteWrite).
type CacheRemoteWrite struct {
	Enabled *bool `json:"enabled" yaml:"enabled"` // when false, write the local tier but never the remote; unset writes the remote when the local tier is written and a signing key is held; true makes remote writes required
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
	Enabled *bool `json:"enabled" yaml:"enabled"` // when true, this host property keys every cache entry; off by default because a manifest guard already refuses a cross-platform replay
}

// WriteEnabled reports whether this run may write the local cache tier.
func (c Cache) WriteEnabled() bool { return c.Write.Enabled == nil || *c.Write.Enabled }

// RemoteWriteEnabled reports whether config allows writing the remote cache tier. The
// cache also needs a signing key when a trust set is declared, which config cannot see.
// It is never true while WriteEnabled is false; [Validate] refuses an explicit true there.
func (c Cache) RemoteWriteEnabled() bool {
	if !c.WriteEnabled() {
		return false
	}
	return c.Remote.Write.Enabled == nil || *c.Remote.Write.Enabled
}

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
	InsecureReason string           `json:"insecure_reason,omitempty" yaml:"insecure_reason,omitempty"`
	Write          CacheRemoteWrite `json:"write" yaml:"write"`
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
	// branch name, so a workspace whose policy forbids either leaving the repository
	// (however scoped the CI cache is) turns this off and gives up only the last-passed
	// base, keeping every timing and volatility field. Off, magus records nothing and
	// `--base last-passed` says so loudly rather than resolving to something arbitrary.
	RecordRuns bool `json:"record_runs" yaml:"record_runs"`
}

// Volatility controls volatility detection and auto-retry for test runs.
type Volatility struct {
	// Enabled is the master switch for volatility detection and auto-retry; false disables all retry logic.
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
// "how long will you hold the build open for a person to complete an unlock", long
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
	Enabled *bool  `json:"enabled" yaml:"enabled"`                                  // when false, refuse to start the MCP server (default true)
	Address string `json:"address" yaml:"address" validate:"omitempty,mcp_address"` // host:port; default 127.0.0.1:7391
	// HTTP serves /mcp on the server's HTTP listener. False leaves the route unmounted while
	// `magus mcp` (stdio) and the server socket keep serving MCP, and the listener keeps the
	// console and health routes. Default true.
	HTTP *bool `json:"http" yaml:"http"`
	// InsecureBind permits a non-loopback Address. That listener serves bearer tokens over
	// plaintext HTTP, so without it the server refuses to start rather than warn.
	InsecureBind bool `json:"insecure_bind" yaml:"insecure_bind"`
}

// HTTPEnabled reports whether the server mounts /mcp on its HTTP listener.
func (m MCP) HTTPEnabled() bool { return m.HTTP == nil || *m.HTTP }

// Console controls the console service. The console mounts read-only GET endpoints on the MCP
// HTTP server (/api/v1/graph, /api/v1/events) plus the typed StatusService, so a browser running
// the hosted Graph Explorer can read the current workspace, plus the bearer-gated magus.job.v1alpha1 JobService
// for triggering maintenance jobs (the server's one mutating route). Loopback only; bearer auth.
type Console struct {
	Enabled *bool `json:"enabled" yaml:"enabled"` // when false, the MCP HTTP server does not mount the console's read-only API and job service (default true when MCP is up)
}

// Server configures `magus server`: where it listens, which workspaces it serves, and its
// maintenance schedule.
type Server struct {
	// Enabled lets a command hand itself to a running `magus server`; false runs each invocation self-contained. Default true.
	// With Enabled false an invocation never discovers or adopts the server and hosts its
	// own per-process pool. Recursive magus calls still forward over a per-process socket
	// to share the concurrency budget; only the server is opted out of.
	Enabled bool `json:"enabled" yaml:"enabled"`
	// Address is the unix:// socket `magus server` listens on; empty means server.sock in the runtime dir.
	Address string `json:"address" yaml:"address" validate:"omitempty,magus_endpoint"`
	// IdleTTL controls workspace eviction in the multi-workspace server; 0 = default 6h.
	IdleTTL time.Duration `json:"idle_ttl" yaml:"idle_ttl"`
	// Workspaces is the explicit list of workspace roots to serve; non-empty enables eager union of sandbox
	// policies and rejects out-of-list workspaces (MGS2010).
	Workspaces []string `json:"workspaces" yaml:"workspaces"`
	// Maintenance configures the server's built-in background maintenance scheduler.
	Maintenance Maintenance `json:"maintenance" yaml:"maintenance"`
}

// Maintenance sets how often the server runs each low-key background maintenance job on its own.
// Each field is the MINIMUM interval since that job last ran before the server runs it again;
// the run is idle-gated (only when the pool is quiet) and submitted through the same coalescing
// path as a manual run, so the two never double-run. "Last run" is read from the activity trail,
// so a manual trigger through any path (CLI or RPC) resets the countdown, and the schedule
// survives a server restart. A zero (or negative) interval disables that job's scheduling.
// clear-cache is intentionally absent: wiping the cache is user-triggered only, never scheduled.
type Maintenance struct {
	// RotateActivities is only how often the server CHECKS whether the activity trail is due
	// for a trim; it is not a retention age. The trail rotates on event count instead: 10000
	// events, with a per-kind floor that keeps a rare kind's newest entries no matter how loud
	// its neighbors are (see maxEvents and perKindFloor in internal/trail). So there is no age
	// cutoff this field could hand it: an age cutoff would have to decide whether it or the
	// per-kind floor wins when the two disagree, and that policy does not exist. It runs
	// hourly because a rotate on an already-small trail costs one stat.
	RotateActivities time.Duration `json:"rotate_activities" yaml:"rotate_activities"` // poll cadence only; retention is trail.maxEvents (10000), not this
	// RotateLogs is both how often the server checks the run-log journals AND, since the
	// check doubles as the enforcement, the maximum age a kept journal may reach: a journal
	// older than this is dropped even when the workspace is well under the count (500) and
	// size (2GB) caps those also enforce, and the tightest of the three wins. Weekly by
	// default, because run-logs otherwise have no age bound at all.
	RotateLogs time.Duration `json:"rotate_logs" yaml:"rotate_logs"` // trim run-log journals older than this; default 7d
	// PrunePreserved is only how often the server checks for expired `vcs checkpoint
	// --preserve` captures; it is not the retention window. That window is fixed at 30 days
	// (vcs.preserveRetention) and stays out of this field on purpose: the days a preserved
	// capture survives is a promise the `--preserve` flag itself advertises, and letting a
	// per-server poll knob also change what gets deleted would make that promise mean
	// whatever this config happened to say (see the rationale on vcs.PrunePreserved).
	// Preserve prunes on its own call too, but only a repository preserved a SECOND time
	// ever reaches that pass, so this scheduled job is what makes the promise hold for a
	// repository preserved once and never again. Daily, since a capture surviving an
	// extra day past the fixed window costs nothing.
	PrunePreserved time.Duration `json:"prune_preserved" yaml:"prune_preserved"` // poll cadence only; retention is vcs.preserveRetention (30d), not this
	SyncGraph      time.Duration `json:"sync_graph" yaml:"sync_graph"`           // reconcile the graph; default 6h (a safety net behind the VCS hook)
	// CheckReview notices a merge or a new remark on a review this tree took part in. The only
	// scheduled job that leaves the machine, so its default is the longest here: a pull request
	// merges once, and a remark waiting fifteen minutes costs nobody anything.
	CheckReview time.Duration `json:"check_review" yaml:"check_review"`
}

// VCS controls VCS-driven affected detection.
type VCS struct {
	Enabled *bool  `json:"enabled" yaml:"enabled"` // master switch for VCS-driven affected detection; false makes affected fall back to all projects (default true)
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
	// PublishedRef is the OCI artifact a published knowledge graph is READ from, as
	// <registry>/<repository>:<tag>. Empty (the default) means this workspace pulls
	// nothing and every graph is built locally.
	//
	// Opt-in per repository and never derived. A derived destination would mean a fork
	// silently reading a namespace nobody chose, and the graph decides what magus answers
	// about this tree, so it is not a thing to guess at. `magus graph push` writes it (see
	// the magusfile's own registry declaration); this is the read side.
	//
	// When set, a history change (branch switch, merge, rebase) tries the pull before
	// rebuilding, which is what makes a fresh worktree cheap: the SCIP shards are the
	// expensive half and are never committed. A pull that fails for any reason is not an
	// error, it is a local build.
	PublishedRef string `json:"published_ref" yaml:"published_ref"`
	// MaxSizeMB is a soft cap on the knowledge shard store (<cache>/knowledge). When
	// exceeded after a build, least-recently-used shard files are evicted; an evicted
	// shard is restored from the remote cache or rebuilt on the next query. 0
	// (default) is unlimited: the store self-reconciles deleted projects, so a cap
	// mainly bounds transient bloat.
	MaxSizeMB int `json:"max_size_mb" yaml:"max_size_mb" validate:"gte=0"`
	// Symbols overrides symbol ingestion for specific projects. Ingestion is normally
	// AUTOMATIC: every project bound to a symbol-capable spell (go, ts, py, rust, buzz;
	// any spell declaring mgs_getSymbolIndexer) is ingested from its cached indexes with
	// no config here. Each entry below instead points a named project at a
	// workspace-relative .scip path your own build emits, for a project whose index does
	// not come from a magus indexer op. A project's entries replace every index magus
	// derives for it, one entry per index, each naming its language when the project
	// holds more than one. A declared (or derived) index that does not exist yet is
	// skipped, so the shard appears once the index is built.
	Symbols []SymbolIndex `json:"symbols" yaml:"symbols"`
	// VCS enables folding git history metadata (last-commit SHA and time, commit
	// count) onto file nodes as a @vcs shard. Opt-in and best-effort: disabled by
	// default, and a non-git workspace yields no shard. The history scan is
	// bounded and cached against HEAD, so it runs at build time on a commit change,
	// never per query.
	VCS KnowledgeVCSConfig `json:"vcs" yaml:"vcs"`
	// SymbolIndexing configures the server's background auto-indexing: it runs each
	// symbol-capable project's `scip` op for you when its sources change, so symbols
	// stay fresh with no manual `magus run ::scip`. ON by default in the server (a
	// one-shot CLI never auto-indexes); throttled and idle-gated so it never delays
	// your own work. Set disabled to opt out.
	SymbolIndexing SymbolIndexingConfig `json:"symbol_indexing" yaml:"symbol_indexing"`
	// Duplication tunes the insight lens that ranks copied functions from the call graph.
	// The defaults were set against this repository; a codebase of many small helpers
	// will want MinShared higher, one of large functions may want MinScore lower.
	Duplication DuplicationConfig `json:"duplication" yaml:"duplication"`
	// Conformance is the policy of the conformance lens and its checks: which code distributions are
	// measured, the shipped anchor each is bounded by, and the committed ratchet record.
	//
	// cli:"-": magus.yaml ONLY. A ratchet is committed policy, so a per-run flag or one
	// exported env var would loosen every check without a trace in any file.
	Conformance ConformanceConfig `json:"conformance" yaml:"conformance" cli:"-"`
	// Sessions declares which agent hosts' transcripts to fold into the @session overlay,
	// the layer that answers which code agents actually touch. `magus graph build` runs
	// each declared adapter before assembling, so the server's sync-graph job keeps it
	// current without a second schedule.
	//
	// Nothing is derived: an adapter reads a transcript store outside the workspace,
	// usually under $HOME, and magus does not go looking through a person's home
	// directory because a config key was left blank. The ingest is also deliberately
	// LOCAL. Everything it produces lives in the per-repo session store and the local
	// shard, never in a committed graph, because a transcript carries the paths someone
	// worked on and the prose their host recorded.
	Sessions SessionsConfig `json:"sessions" yaml:"sessions"`
	// Notes declares where this workspace keeps its human-authored notes (see
	// NotesConfig). Empty (the default) means no notes store at all and every part of the
	// feature is inert.
	Notes NotesConfig `json:"notes" yaml:"notes"`
}

// NotesConfig declares where human-authored notes live. There are two locations because
// there are two audiences: the team (shared) and only you (private). An agent writes to
// neither, which the guard enforces: an agent's derived claims are never pushed at the
// team under a person's name.
//
// A note is prose a PERSON wrote about the code, anchored to graph entities but derived
// from none of them, the one node class magus cannot regenerate, because its only
// provenance is the author. Both locations are DECLARED rather than assumed: magus judges
// writes to them, and a rule fired on a guessed location would act on a workspace that
// never opted in.
type NotesConfig struct {
	// Shared is the workspace-relative directory holding notes the TEAM has: committed,
	// so git attributes each one to whoever wrote it and review sees every change.
	// Empty disables it entirely: no shard, no verification, no guard rule.
	//
	// It must stay inside the workspace. That is not a restriction so much as what the
	// word means here: a note outside the checkout is not shared with anyone.
	Shared string `json:"shared" yaml:"shared"`
	// Private is a SECOND notes location, yours rather than the team's, and it may sit
	// anywhere on disk: a vault, a scratch directory, somewhere outside any repository.
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
	// the same reason: private content must not leak into a shared cache), and its nodes
	// carry a scope attr so the distinction is visible wherever they surface.
	//
	// Agents still may not write here. That is the point: it is the vault case.
	//
	// compat(until: no workspace here or in the wild sets knowledge.notes.private):
	// SUPERSEDED, still read, no longer the recommended shape. A private note is
	// uncommitted, unattributed, unreviewed and unrecoverable, and nothing that reads notes
	// has needed one. Observe that it is safe to drop by checking that nothing sets it: it
	// was never set in this repository, and a store nobody points at holds nothing to lose.
	// Kept readable until then because the path may name someone's vault, and deleting the
	// key would orphan real prose to save a struct field.
	Private string `json:"private" yaml:"private"`
}

// DuplicationConfig is knowledge.duplication in magus.yaml. See types.DuplicationOptions for
// what each threshold filters and why each can err in both directions.
type DuplicationConfig struct {
	MinCallees   int     `json:"min_callees" yaml:"min_callees" validate:"gte=1"`             // fewest callees a function needs before the lens compares it
	MinShared    int     `json:"min_shared" yaml:"min_shared" validate:"gte=1"`               // fewest shared callees a pair needs to be reported
	MinScore     float64 `json:"min_score" yaml:"min_score" validate:"gte=0,lte=1"`           // lowest similarity score in [0,1] the lens reports
	MinSpanRatio float64 `json:"min_span_ratio" yaml:"min_span_ratio" validate:"gte=0,lte=1"` // lowest ratio in [0,1] of the shorter function's span to the longer one's
	IncludeTests bool    `json:"include_tests" yaml:"include_tests"`                          // when true, the lens also compares test functions
}

// Options is the config as the graph package reads it, which cannot import this one.
func (c DuplicationConfig) Options() types.DuplicationOptions {
	return types.DuplicationOptions{
		MinCallees:   c.MinCallees,
		MinShared:    c.MinShared,
		MinScore:     c.MinScore,
		MinSpanRatio: c.MinSpanRatio,
		IncludeTests: c.IncludeTests,
	}
}

// ConformanceConfig is knowledge.conformance in magus.yaml. See types.ConformanceOptions for how the
// graph reads it.
//
// A key written in a magus.yaml replaces the default for that whole field, as every
// non-zero config value does: writing checks states all of them.
type ConformanceConfig struct {
	// Ratchet is the workspace-relative path of the committed record of measured
	// thresholds. Empty enforces the anchors alone and records no proposals.
	Ratchet string `json:"ratchet" yaml:"ratchet"`
	// MinSample is how many measurements a population needs before a proposal is made
	// from it; a smaller population keeps its anchor.
	MinSample int `json:"min_sample" yaml:"min_sample" validate:"gte=1"`
	// Step is the fraction a live percentile must fall below its committed value before a
	// tighter one is proposed.
	Step float64 `json:"step" yaml:"step" validate:"gt=0,lt=1"`
	// Exclude are workspace-relative globs of code kept out of every population, such as
	// a vendored or upstream-shaped tree. Generated output is always out.
	Exclude []string `json:"exclude" yaml:"exclude"`
	// Gates are the absolute noise gates of the conformance checks.
	Gates ConformanceGatesConfig `json:"gates" yaml:"gates"`
	// Checks is the policy per metric and population. A metric or population left out is
	// not checked.
	Checks map[types.ConformanceMetric]map[types.ConformancePopulation]ConformanceLimitConfig `json:"checks" yaml:"checks"`
}

// ConformanceGatesConfig is knowledge.conformance.gates: the cohort a convention needs before it
// binds a change, and how many findings are reported.
type ConformanceGatesConfig struct {
	MinCohort   int     `json:"min_cohort" yaml:"min_cohort" validate:"gte=1"`
	MinShare    float64 `json:"min_share" yaml:"min_share" validate:"gte=0,lte=1"`
	Bar         float64 `json:"bar" yaml:"bar" validate:"gte=0,lte=1"`
	MaxFindings int     `json:"max_findings" yaml:"max_findings" validate:"gte=1"`
}

// ConformanceLimitConfig is the policy for one metric and population.
type ConformanceLimitConfig struct {
	// Percentile is the percentile of the live distribution a proposal measures at.
	Percentile float64 `json:"percentile" yaml:"percentile"`
	// Anchor is the ceiling no committed value may exceed, and the value enforced while
	// nothing is committed.
	Anchor int `json:"anchor" yaml:"anchor"`
	// MinSubject is the fewest importers a package needs before alias-share or name-clash
	// judges it; 0 judges every package with one. Other metrics ignore it.
	MinSubject int `json:"min_subject" yaml:"min_subject" validate:"gte=0"`
	// Scopes bounds a package directory or layer apart from the rest of the population,
	// as directory to anchor, measured at this Percentile. Only dep-fanout has scopes. A
	// package that legitimately sits past the population's anchor takes its own here, so
	// the anchor itself stays tight.
	Scopes map[string]int `json:"scopes,omitempty" yaml:"scopes,omitempty"`
}

// Options is the config as the graph package reads it, which cannot import this one.
// Ratchet and Generated are the caller's to fill from files. A scope becomes its own key.
func (c ConformanceConfig) Options() types.ConformanceOptions {
	limits := map[types.ConformanceKey]types.ConformanceLimit{}
	for metric, pops := range c.Checks {
		for pop, l := range pops {
			limits[types.ConformanceKey{Metric: metric, Population: pop}] = types.ConformanceLimit{
				Percentile: l.Percentile, Anchor: l.Anchor, MinSubject: l.MinSubject,
			}
			for scope, anchor := range l.Scopes {
				limits[types.ConformanceKey{Metric: metric, Population: pop, Scope: scope}] = types.ConformanceLimit{
					Percentile: l.Percentile, Anchor: anchor, MinSubject: l.MinSubject,
				}
			}
		}
	}
	return types.ConformanceOptions{
		Limits:      limits,
		RatchetPath: c.Ratchet,
		MinSample:   c.MinSample,
		Step:        c.Step,
		Exclude:     slices.Clone(c.Exclude),
		Gates: types.ConformanceGates{
			MinCohort:   c.Gates.MinCohort,
			MinShare:    c.Gates.MinShare,
			Bar:         c.Gates.Bar,
			MaxFindings: c.Gates.MaxFindings,
		},
	}
}

// conformanceDefaults are magus's own numbers, in the way DuplicationConfig's are: a
// workspace with no knowledge.conformance fails nothing magus would not fail on itself.
//
// An anchor is magus's own p99 at the measuring rev, rounded up to two significant
// figures, with two exceptions. clone-tokens' anchor is the smallest body worth one shared
// home, just above the typical function (p50 58). call-sequence's 7 is the run just below
// maint-review's measured precision cut: a shared run of 8 callees caught B1, B7 and B15.
// The coupling keys pool source and main code under source, and mode-knobs is a list, so
// its anchor is 0. The alias keys judge packages with 5 or more importers, the median, and
// name-clash's anchor is its one measured case, 62. The percentile is where a proposal
// measures: p99 for test files, function lengths and call sequences, since a long table
// test is deliberate, p50 for clones, p90 for a shared body, p95 elsewhere. They name no
// path: an exclusion or a scope is one workspace's layout, so it lives in that workspace's
// magus.yaml.
func conformanceDefaults() ConformanceConfig {
	limit := func(percentile float64, anchor int) ConformanceLimitConfig {
		return ConformanceLimitConfig{Percentile: percentile, Anchor: anchor}
	}
	const (
		src  = types.ConformancePopSource
		main = types.ConformancePopMain
		test = types.ConformancePopTest
	)
	return ConformanceConfig{
		MinSample: 30,
		Step:      0.05,
		Gates:     ConformanceGatesConfig{MinCohort: 5, MinShare: 0.8, Bar: 0.5, MaxFindings: 5},
		Checks: map[types.ConformanceMetric]map[types.ConformancePopulation]ConformanceLimitConfig{
			types.ConformanceFileLines:    {src: limit(95, 2200), main: limit(95, 3000), test: limit(99, 1900)},
			types.ConformanceFuncLines:    {src: limit(99, 92), main: limit(99, 140), test: limit(99, 73)},
			types.ConformanceTypeMethods:  {src: limit(95, 49), main: limit(95, 22), test: limit(95, 32)},
			types.ConformanceFileSymbols:  {src: limit(95, 82)},
			types.ConformanceCloneTokens:  {src: limit(50, 80), main: limit(50, 80)},
			types.ConformanceDepFanout:    {src: limit(95, 62)},
			types.ConformanceCallSequence: {src: limit(99, 7), main: limit(99, 7)},
			types.ConformanceSharedBody:   {src: limit(90, 94)},
			types.ConformanceSharedReach:  {src: limit(95, 7)},
			types.ConformanceCallerSpread: {src: limit(95, 3)},
			types.ConformanceModeKnobs:    {src: limit(0, 0)},
			types.ConformanceAliasShare:   {src: {Percentile: 95, Anchor: 100, MinSubject: 5}},
			types.ConformanceNameClash:    {src: {Percentile: 99, Anchor: 62, MinSubject: 5}},
		},
	}
}

// SymbolIndexingConfig tunes server background symbol auto-indexing (see
// Knowledge.SymbolIndexing). Zero value = enabled with built-in timings.
type SymbolIndexingConfig struct {
	// Disabled opts out of background auto-indexing. Auto-indexing is on by default
	// in the server, so this is the switch to turn it off (e.g. the indexers are not
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
	// Enabled folds VCS history (last commit, commit count) onto file nodes as the @vcs shard.
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

// SessionsConfig configures agent-session ingestion (see Knowledge.Sessions). Zero value
// = enabled, with nothing to run until an adapter is declared.
type SessionsConfig struct {
	// Disabled opts out entirely, so `graph build` runs no adapter and `--no-sessions`
	// becomes the permanent answer. The switch to reach for when the transcripts on this
	// machine are not yours to read.
	Disabled bool `json:"disabled" yaml:"disabled"`
	// Adapters are the commands that load each host's transcripts, run in the workspace
	// root before the graph is assembled.
	Adapters []SessionAdapter `json:"adapters" yaml:"adapters"`
}

// SessionAdapter declares one agent host's transcript loader.
//
// It is a COMMAND rather than a host magus knows how to read, and that is the whole
// design: transcript formats belong to the hosts, so a host renaming a tool costs this
// one line instead of a magus release. magus ships a ready adapter per host it documents
// (docs/guides/integrations/agents/magus-session-load-*.buzz); declaring one here is how a
// workspace opts in, and writing your own is how an undocumented host gets supported
// without waiting for anybody.
//
// The command's contract is `magus session load`'s: normalize the host's transcripts into
// the event stream and load them. It owns its own incremental state, so re-running it is
// expected to be cheap and to load only what is new.
type SessionAdapter struct {
	Host string `json:"host" yaml:"host"` // the host whose transcripts this reads, for reporting
	// Command is the program and its arguments, run from the workspace root. A LIST, not a
	// command line: magus executes this directly, with no shell, so a declared adapter
	// runs the one program it names. That is how magus represents every command it runs
	// (see job.CatalogEntry.Argv). A committed value is still code that runs on the
	// machine that pulled the branch: `magus graph build` runs it, and only an explicit
	// verb does.
	//
	// Something needing a pipeline or a variable writes a script and names the script,
	// which is what the adapters magus documents already are.
	Command []string `json:"command" yaml:"command"`
}

// SymbolIndex declares one project's SCIP index for symbol ingestion.
type SymbolIndex struct {
	Project string `json:"project" yaml:"project"` // workspace-relative project path the symbols belong to
	Index   string `json:"index" yaml:"index"`     // workspace-relative path to the .scip index file
	// Language is the language the index's symbols are written in, for an index that
	// does not say. Empty takes the language of the project's indexing spell, which
	// mislabels all but one index of a project that lists one per language.
	Language string `json:"language,omitempty" yaml:"language,omitempty"`
}

// Telemetry holds OpenTelemetry exporter settings. OFF by default; no magus-operated backend exists.
// When Enabled, magus connects to the OTLP collector you configure and sends data there only.
type Telemetry struct {
	// Enabled turns OTLP export on; magus connects to Endpoint when true.
	Enabled     bool              `json:"enabled" yaml:"enabled"`
	Endpoint    string            `json:"endpoint" yaml:"endpoint"`                                      // host:port; required when Enabled
	Protocol    string            `json:"protocol" yaml:"protocol" validate:"omitempty,oneof=grpc http"` // OTLP wire protocol: grpc or http
	Insecure    bool              `json:"insecure" yaml:"insecure"`                                      // disable TLS
	ServiceName string            `json:"service_name" yaml:"service_name"`                              // resource attribute service.name
	SampleRatio float64           `json:"sample_ratio" yaml:"sample_ratio" validate:"gte=0,lte=1"`       // head-based sampling ratio; 1.0 = all
	Headers     map[string]string `json:"headers" yaml:"headers"`                                        // static OTLP request headers
}

// Report controls JSONL event emission for magus run.
type Report struct {
	// Filter restricts event types via +type/-type/bare terms; any "+" sets default-deny.
	Filter []string `json:"filter" yaml:"filter"`
}

// EnvVars documents every MAGUS_* environment variable magus reads: one per config
// field, from the generated schema with its built-in default, then the ones no field
// carries.
//
// KnownEnvVar and EnvVarProblem (MGS1046) answer from it, so a name missing here is
// reported as unknown by doctor and can draw a did-you-mean refusal.
// TestEveryReadMagusEnvVarIsRegistered fails when shipped code reads a name it lacks.
func EnvVars() []cmdline.Env {
	defaults := reflect.ValueOf(Defaults())
	out := make([]cmdline.Env, 0, len(schema.Fields)+len(envOnly))
	for _, f := range schema.Fields {
		out = append(out, cmdline.Env{Name: f.EnvVar, Key: f.YamlPath, Default: defaultText(defaults, f.GoPath), Doc: f.Doc})
	}
	return append(out, envOnly...)
}

// defaultText renders the field at goPath in defaults, or "" for a zero value. A path
// the struct does not have panics: the schema is generated from this struct, so a miss
// means the generated inventory is stale.
func defaultText(defaults reflect.Value, goPath string) string {
	v := defaults
	for _, name := range strings.Split(goPath, ".") {
		v = v.FieldByName(name)
		if !v.IsValid() {
			panic(fmt.Sprintf("config: schema field %s is not in Config; regenerate schema/gen", goPath))
		}
	}
	if v.IsZero() {
		return ""
	}
	if d, ok := v.Interface().(time.Duration); ok {
		// 6h0m0s reads as 6h: drop each trailing zero unit.
		s := d.String()
		if trimmed, ok := strings.CutSuffix(s, "m0s"); ok {
			s = trimmed + "m"
		}
		if trimmed, ok := strings.CutSuffix(s, "h0m"); ok {
			s = trimmed + "h"
		}
		return s
	}
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() == reflect.Slice {
		parts := make([]string, v.Len())
		for i := range parts {
			parts[i] = fmt.Sprint(v.Index(i).Interface())
		}
		return strings.Join(parts, ",")
	}
	return fmt.Sprint(v.Interface())
}

// envOnly are the variables no config field carries, so the schema cannot list them:
// secrets kept out of committed files, overrides of build-time values, and the
// plumbing magus sets for its own children.
var envOnly = []cmdline.Env{
	{Name: "MAGUS_CACHE_SIGNING_KEY", Key: "", Default: "", Doc: "Env-only, no magus.yaml equivalent: the Ed25519 seed that signs remote-cache writes (see `magus config cache-key`); env-only because a signing secret in a committed file is not a secret"},
	{Name: "MAGUS_CACHE_TOOL_VERSION", Key: "", Default: "project", Doc: "Env-only, no magus.yaml equivalent: how tool versions key the cache: project (per project), workspace (once for the workspace), or off"},
	{Name: "MAGUS_VCS_<NAME>_BASE_REF", Key: "", Default: "", Doc: "Per-VCS base-ref override, e.g. MAGUS_VCS_GIT_BASE_REF; dynamic pattern, read directly by package vcs"},
	{Name: "MAGUS_PROC_SOCKET", Key: "", Default: "", Doc: "Env-only, no magus.yaml equivalent: the proc-server socket a magus process exports for the magus processes it spawns; unix:// URL or bare path, read directly by the process that adopts it"},
	{Name: "MAGUS_PROC_TOKEN", Key: "", Default: "", Doc: "Env-only, no magus.yaml equivalent: the secret the MAGUS_PROC_SOCKET server requires on every request, exported beside it and inherited only by a nested magus"},
	{Name: "MAGUS_SHARD", Key: "", Default: "", Doc: "CI matrix shard ID (e.g. \"0\"); equivalent to magus run --shard; set by .github/actions/magus"},
	{Name: "MAGUS_N_SHARDS", Key: "", Default: "", Doc: "Total shard count for this matrix run; equivalent to magus run --n-shards; set by .github/actions/magus"},
	{Name: "MAGUS_UPDATE_URL", Key: "", Default: "https://eli.gladman.cc/magus/public/release/index.json", Doc: "Env-only, no magus.yaml equivalent: override the release index URL for `magus self update`; set to a self-hosted copy of index.json to use a private update channel"},
	{Name: "MAGUS_STAMPED_LEASE", Key: "", Default: "", Doc: "Set by a shared server on a script it forks for a remote caller, and by an authenticated MCP caller for a child magus: the BAGGAGE lease was stamped by the transport rather than claimed by the spawner. It only downgrades: a missing lease reads as unstamped instead of unbound, never as a grant; never set it by hand"},
	{Name: "MAGUS_NO_BOOTSTRAP_EXEC", Key: "", Default: "false", Doc: "Env-only, no magus.yaml equivalent: when 1, true or yes, disable the pre-workspace-load check that replaces this process with a workspace-local ./magus found by walking up from the working directory (or --root); set it to force the binary actually invoked to run instead, e.g. while debugging that binary itself"},
	{Name: "MAGUS_OFFLINE", Key: "", Default: "false", Doc: "Env-only, no magus.yaml equivalent: when set to anything but 0 or false, a remote-spell fetch and every std/http request a script sends fail with a named error instead of sending a request, and the lifecycle provider is not asked: `magus describe tools` replays its stored answer or reads unknown (offline)"},
	{Name: "MAGUS_DIFFTOOL", Key: "", Default: "$DIFFTOOL, then git diff --no-index", Doc: "Env-only, no magus.yaml equivalent: the command, taking two paths, that `--then file <path> diff` compares a cached artifact with"},
	{Name: "MAGUS_LOG_VIEWER_URL", Key: "", Default: "the hosted log viewer", Doc: "Env-only, no magus.yaml equivalent: base URL of the log viewer page a live run opens, for a self-hosted mirror"},
	{Name: "MAGUS_CONSOLE_DIR", Key: "", Default: "<root>/console/gen", Doc: "Env-only, no magus.yaml equivalent: directory of the built console the LAN share serves"},
	{Name: "MAGUS_PPROF", Key: "", Default: "", Doc: "Env-only, no magus.yaml equivalent: write Go profiles for one invocation, as kind:path pairs (cpu, mem, trace), comma separated"},
	{Name: "MAGUS_MCP_TOKEN", Key: "", Default: "", Doc: "Env-only: the secret reference shipped harness spells name for the MCP bearer token; the environment secret provider reads it from this variable"},
	{Name: "MAGUS_MCP_BUZZ_PURE", Key: "", Default: "", Doc: "Set internally by the buzz MCP tool for its forked transform worker; never set it by hand"},
	{Name: "MAGUS_MCP_CLIENT", Key: "", Default: "", Doc: "Set internally by the client MCP tool for its forked worker; never set it by hand"},
	{Name: "MAGUS_MCP_CLIENT_HOST", Key: "", Default: "", Doc: "Set internally by the client MCP tool: the calling client's name, stamped onto the worker's activity trail; never set it by hand"},
	{Name: "MAGUS_MCP_CLIENT_LEASE", Key: "", Default: "", Doc: "Set internally by the client MCP tool: the calling client's job lease, or empty when the transport stamped none; never set it by hand"},
	{Name: "MAGUS_S3_BUCKET", Key: "", Default: "", Doc: "Env-only: the bucket the aws/s3-cache spell stores remote-cache artifacts in"},
	{Name: "MAGUS_S3_ENDPOINT", Key: "", Default: "https://s3.<region>.amazonaws.com", Doc: "Env-only: the S3-compatible endpoint the aws/s3-cache spell talks to"},
	{Name: "MAGUS_LEVEL", Key: "", Default: "", Doc: "Set by magus for the processes it spawns: the magus recursion depth, like make's MAKELEVEL; never set it by hand"},
	{Name: "MAGUS_INVOCATION_ANCESTORS", Key: "", Default: "", Doc: "Set by magus for the processes it spawns: the comma-separated invocations a nested magus runs underneath; never set it by hand"},
	{Name: "MAGUS_BOOTSTRAP_EXEC_DONE", Key: "", Default: "", Doc: "Set by magus when it replaces itself with a workspace-local ./magus, so the replacement never hops again; never set it by hand"},
	{Name: "MAGUS_SYMBOL_INDEX", Key: "", Default: "", Doc: "Set by magus for a spell's symbol indexer op: the path the indexer writes its SCIP index to; never set it by hand"},
	{Name: "MAGUS_WORKSPACE_ROOT", Key: "", Default: "", Doc: "Set by magus for a spell's symbol indexer op: the workspace root the index's paths are relative to; never set it by hand"},
	{Name: "MAGUS_INTERNAL_ADVICE_MODE", Key: "", Default: "", Doc: "Set by `magus diff` for the advice script it runs; the two halves of one feature, not a setting, and either may be renamed without notice"},
	{Name: "MAGUS_INTERNAL_ADVICE_BASE_BRANCH", Key: "", Default: "", Doc: "Set by `magus diff` alongside MAGUS_INTERNAL_ADVICE_MODE; not a setting"},
}

// DefaultGoCacheBudgetMB is go.cache_budget_mb when nothing sets it: 20 GiB.
const DefaultGoCacheBudgetMB = 20480

// DefaultShutdownGrace is shutdown_grace when nothing sets it. Long enough for most test
// and build steps to finish; a supervisor that will not wait this long sends its own
// second signal or SIGKILL, and runs outlive the broker anyway, re-asserting their claims
// on the next one.
const DefaultShutdownGrace = 5 * time.Minute

// Defaults returns a Config populated with the magus built-in defaults. It reads no
// environment, so HistoryPath is empty; the loaders set it from [DefaultHistoryPath].
func Defaults() Config {
	return Config{
		CI: CI{MaxShards: 8, RecordRuns: true},
		Server: Server{
			Enabled: true,
			Maintenance: Maintenance{
				RotateActivities: time.Hour,          // poll cadence; cheap to check often (one stat when small)
				RotateLogs:       7 * 24 * time.Hour, // poll cadence AND the age cap it enforces: weekly
				PrunePreserved:   24 * time.Hour,     // poll cadence only; the 30-day window is fixed elsewhere
				SyncGraph:        6 * time.Hour,      // safety net behind the VCS refresh hook
				CheckReview:      15 * time.Minute,   // the only one that reaches a forge; a merge happens once
			},
		},
		ShutdownGrace: DefaultShutdownGrace,
		Go:            GoConfig{CacheBudgetMB: DefaultGoCacheBudgetMB},
		Jobs:          Jobs{StaleAfter: 2 * time.Hour, Retain: 7 * 24 * time.Hour, MaxValidations: 1, MaxClaimFiles: 50, MaxClaimProjects: 1, IntegrateAdviseAt: 5, IntegrateRefuseAt: 10, MaxClaims: 10},
		Queue:         Queue{CarryApprovals: CarryApprovals(queuetypes.DefaultCarryPolicy())},
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
		Hints: Hints{Enabled: new(true)},
		Knowledge: Knowledge{
			VCS: KnowledgeVCSConfig{Authorship: new(true)},
			// MinShared 4 is the line that mattered: at 3, identical triples of small
			// helpers scored a perfect 1.0 and were most of the list.
			Duplication: DuplicationConfig{MinCallees: 3, MinShared: 4, MinScore: 0.7, MinSpanRatio: 0.5},
			Conformance: conformanceDefaults(),
		},
		Telemetry: Telemetry{
			Protocol:    "grpc",
			ServiceName: "magus",
			SampleRatio: 1.0,
		},
	}
}
