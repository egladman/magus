// Command magus is the magus CLI: a standalone build orchestrator and
// content-addressed cache for multi-language monorepos, and an evolution of
// Mage.
//
// magus provides workspace-aware subcommands for building, testing, linting,
// and inspecting projects without requiring Mage to be installed. It reads
// optional configuration from magus.yaml (XDG or CWD) and MAGUS_* environment
// variables.
//
// A few commands to start with:
//
//	magus ls                            list all discovered projects
//	magus run <target> [project...]     run a target for selected projects
//	magus affected <target>             run a target for VCS-diff affected projects
//	magus x [filter...]                 interactive shorthand: pick project + target
//	magus doctor                        validate the workspace
//
// magus help prints the full top-level surface, in the order and with the
// descriptions subcommands in surface.go declares as the single source of
// truth - kept short here rather than a second enumeration that can drift
// from it, as this comment once did (it advertised a `magus tail` that was
// never a real subcommand).
//
// Run any subcommand with -h/--help for its own flag list.
//
//go:generate go run ../magus-utils config -config ../../internal/config/config.go -fields-out ../../schema/gen/fields.go -bind-out gen/bind.go -apply-env-out ../../internal/config/gen/env.go
//go:generate go run ../magus-utils cliflags -out gen/cli_flags.go
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/jobs"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/observability/otlp"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/service"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/types"
)

func main() {
	os.Exit(runCLI())
}

// runCLI is the CLI entry point as a function returning an exit code, so both main
// (os.Exit(runCLI())) and the testscript harness (testscript.Main) can drive the
// real command in process. It must never call os.Exit itself.
func runCLI() int {
	log.SetFlags(0)
	log.SetPrefix("magus: ")

	// Deferred, not tied to the cleanup closure below: runCLI has several early
	// returns, and a profile that stops only on the happy path is a profile that is
	// empty exactly when a run failed slowly. main() calls os.Exit on runCLI's RESULT,
	// so this defer still runs. No-op unless MAGUS_PPROF is set.
	defer startProfiling()()

	args := expandVerbosityArgs(os.Args[1:])

	rootCtx, stopSignals, interrupted := watchInterrupts(context.Background())
	// Stamp the binary's version onto the root context so host methods (the drift
	// classifier) can tell a dev build from the pinned release without importing main.
	rootCtx = types.WithMagusVersion(rootCtx, version)
	// Adopt the ancestry a parent magus passed down, before anything can take a project
	// lock. Stamped here rather than in BeginInvocation because every subcommand that
	// locks needs it, including the ones with no invocation record of their own (clean).
	rootCtx = types.WithInvocationAncestors(rootCtx, run.AncestorsFromEnv())

	res, exitCode := startup(rootCtx, args)

	cleanup := func() {
		if res.cleanup != nil {
			res.cleanup()
		}
		// Restore the terminal before anything else tears down: an
		// interrupted run must not hand the shell back with the sticky
		// error region's scroll margins still set.
		restoreTerminal()
		stopSignals()
	}

	if exitCode >= 0 {
		cleanup()
		return withInterrupt(exitCode, nil, interrupted)
	}

	var dispatchErr error
	switch res.sub {
	case "help", "-h", "--help":
		usage()
	case "version", "--version":
		dispatchErr = runVersion(res.rootCtx, res.subArgs)
	default:
		dispatchErr = dispatchSub(res.rootCtx, res.root, res.rc, res.sub, res.subArgs)
	}
	code := exitCodeOf(dispatchErr)
	// Offer the run's pinned failures for rerun or inspection, while they are
	// still on screen. A no-op unless a run left failures on a terminal, so
	// every other command reaches it and returns immediately.
	//
	// HERE rather than inside the run commands, so that rerunning a failure
	// cannot re-enter the prompt from inside itself.
	// Not after an abort. The prompt blocks on a read that cannot be
	// interrupted, so opening one over the partial failures of a run the user
	// just told magus to stop is both unwanted and the way to get stuck there.
	if res.rootCtx.Err() == nil {
		if err := promptFailures(res.rootCtx, res.root, cache.StderrHandler()); err != nil {
			fmt.Fprintf(os.Stderr, "magus: %v\n", err)
		}
	}
	cleanup()
	return withInterrupt(code, dispatchErr, interrupted)
}

// withInterrupt reports a signal-stopped run as the conventional 128+N.
//
// A cancelled run's targets die with `context canceled`, which reaches
// [exitCodeOf] as a nil error - so without this the process printed [fail] and
// exited 0, and `magus run test . && deploy` deployed after a Ctrl+C.
//
// Only when code == 0, so a command that already failed for its own reason keeps
// the more specific code - with one exception. A command that RETURNS the
// cancellation instead of swallowing it (awaitInvocation returns ctx.Err()) reached
// exitCodeOf as a generic failure and reported 1, which says the WORK failed about a
// run the user stopped. Still gated on interrupted(), so a deadline or a
// caller-cancelled context - neither of which is a signal - keeps its own code.
func withInterrupt(code int, err error, interrupted func() (syscall.Signal, bool)) int {
	if code != 0 && !errors.Is(err, context.Canceled) {
		return code
	}
	if sig, ok := interrupted(); ok {
		return 128 + int(sig)
	}
	return code
}

// startupResult carries everything main needs to dispatch a subcommand.
// cleanup MUST be called on every exit path (os.Exit skips deferred functions).
type startupResult struct {
	rootCtx context.Context
	root    string
	rc      runConfig
	sub     string
	subArgs []string
	trace   *startupTracer
	cleanup func()
}

// dispatchProfile describes which pre-dispatch phases a subcommand needs.
type dispatchProfile struct {
	needsConfig    bool // load magus.yaml + env vars
	needsDaemonFwd bool // attempt forward to a running daemon
	needsWorkspace bool // call loadMagus + start per-process proc server
}

// isUsageOnlyInvocation reports whether a run/affected invocation only wants usage
// text: no target given, or the first token is a help flag. It mirrors the guards at
// the top of runTarget and affected exactly, so the forward decision agrees with what
// those handlers do locally.
func isUsageOnlyInvocation(subArgs []string) bool {
	if len(subArgs) == 0 {
		return true
	}
	switch subArgs[0] {
	case "-h", "--help", "help":
		return true
	}
	return false
}

// wantsUsage reports whether a subcommand's args ask for its help text.
//
// Distinct from isUsageOnlyInvocation, which additionally treats NO arguments as a usage
// request. That is right for run and affected, whose first positional is required, and wrong
// for everything else: bare `magus diff` is a real invocation.
//
// Scanning stops at "--", past which the tokens belong to a forwarded tool: a run that
// forwards `-h` after the marker is asking the test binary for help, not magus.
func wantsUsage(subArgs []string) bool {
	for i, a := range subArgs {
		if a == "--" {
			return false
		}
		// -h and --help are unambiguous: no subcommand takes either as a positional, so
		// they mean help wherever they sit. `magus diff --impact -h` is the case worth
		// catching.
		if a == "-h" || a == "--help" {
			return true
		}
		// The bare word is only a help request in the FIRST position. Elsewhere it is
		// ordinary data - `magus memory get help` fetches an entry named help, and
		// `magus notes show help` shows a note. Treating those as usage would skip the
		// workspace preload for a real invocation.
		if a == "help" && i == 0 {
			return true
		}
	}
	return false
}

// hasDetachFlag reports whether argv carries --detach, in every spelling the flag
// package accepts. Scanning stops at "--", past which the tokens belong to a forwarded
// tool rather than to magus.
func hasDetachFlag(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		if key, _, _ := strings.Cut(strings.TrimLeft(a, "-"), "="); key == gen.FlagRunDetach {
			return true
		}
	}
	return false
}

// isForensicAffected reports whether an `affected` invocation selects one of the forensic
// modes affectedUsage lists that reason about the set without executing a target:
// --explain, --plan, --impact. --bisect is excluded deliberately - it runs the target once
// per candidate commit, so it wants the shared pool a forward buys.
//
// It reuses affected()'s own routing predicates rather than restating them, so the forward
// decision agrees with what the handler does locally - the property isUsageOnlyInvocation
// exists for one layer up. That includes NOT stopping at "--": affected() routes on a
// bare scan too, and a guard here that disagreed would forward an invocation the handler
// then answers as a forensic mode.
func isForensicAffected(subArgs []string) bool {
	if _, _, ok := parseExplainArgs(subArgs); ok {
		return true
	}
	return hasModeFlag(subArgs, "plan") || hasModeFlag(subArgs, "impact")
}

// resolveProfile returns the work profile for a subcommand; defaults to "needs everything".
func resolveProfile(sub string, subArgs []string) dispatchProfile {
	// Asking a command what it does must not do anything. Every subcommand's own parser
	// already prints usage and returns before it loads a workspace, but the preload here
	// runs FIRST - so `magus diff -h` opened the workspace, and opening one refreshes the
	// VCS merge-driver registration, which WRITES the tracked .gitattributes and a git
	// config entry naming the running binary. A persona doing nothing but reading help
	// found a dangling registration pointing at a throwaway path.
	//
	// The config tier stays: usage text reads it (daemonDefaultAddr in `server start -h`),
	// and reading magus.yaml writes nothing.
	if wantsUsage(subArgs) {
		return dispatchProfile{needsConfig: true}
	}
	switch sub {
	case "help", "version":
		// Neither reads a workspace or a config: one prints text compiled into the
		// binary, the other a stamp. version dials the daemon for the server half, but
		// must never FORWARD - a forwarded version would report the daemon's binary as
		// the client's, which is exactly the difference it exists to show.
		return dispatchProfile{}
	case "buzz":
		// buzz is a standalone Buzz runner (and `buzz lsp` a stdio language server), so
		// it needs no workspace RESOLUTION and is never forwarded to a daemon. It does
		// need the config: a script run inside a workspace gets that workspace on its
		// context (see buzzCmd), and opening one reads magus.yaml and the MAGUS_* env -
		// the remote cache's trust set among them. Listed as config-free while it opened
		// a workspace anyway, it opened it against DEFAULTS, so a wired remote backend
		// came up with no trust set and the load failed with a message naming the very
		// setting the environment had set. That was invisible locally, where the trust
		// set is not what the yaml is consulted for, and fatal in CI.
		return dispatchProfile{needsConfig: true}
	case "completion", "self", "man":
		return dispatchProfile{needsConfig: true}
	case "agent":
		// agent install writes embedded skill files into a repo dir; it needs no
		// workspace resolution and must not forward to a daemon (the install is
		// local to the caller's directory).
		return dispatchProfile{needsConfig: true}
	case "vcs":
		// Never forwarded, never preloaded. Every vcs verb writes the CALLER's index and
		// working tree, so a daemon serving another workspace must not adopt one.
		//
		// The preload matters as much. Opening a workspace refreshes the merge-driver
		// registration, which writes the tracked .gitattributes - and both merge-facing
		// verbs run while that file may be unmerged, or while the VCS holds the index.
		// loadMagus is a sync.Once singleton, so a preload wins the race and performs the
		// write each verb defers: merge-driver would dirty the tree against what git
		// staged and stop `git rebase --continue`, resolve would splice a section between
		// conflict markers. Each verb opens what it needs under its own guard.
		return dispatchProfile{needsConfig: true}
	case "session":
		// The whole family reads or writes a file store keyed by repository identity:
		// it needs the root PATH but never the magusfile, and it must stay usable when
		// the workspace does not load - a broken magusfile is exactly when someone asks
		// what the last runs did, and an agent blocked on a person is exactly the state
		// a half-finished edit produces. Never forwarded: the hook subverb is the LAST
		// thing that should route through a remote process, notify must reach the local
		// OS notifier rather than one on the daemon's host, and a listing is one
		// directory read with no warm daemon state to reuse.
		return dispatchProfile{needsConfig: true}
	case "events":
		// Reads the run-log directory; the magusfile never. Loading the workspace would
		// refresh the merge-driver registration, and a subscriber an editor spawns must
		// not write .gitattributes.
		return dispatchProfile{needsConfig: true}
	case "status":
		return dispatchProfile{needsConfig: true, needsDaemonFwd: true}
	case "server":
		// server subcommands manage the daemon directly and must never forward or host
		// their own per-process proc server. start IS the daemon (special-cased in startup);
		// stop and job resolve the real daemon socket explicitly (resolveDaemonAddr). The old
		// default profile made `server stop` forward, and on a version-mismatched forward
		// (common across dev worktrees on one shared socket) it fell through to hosting its
		// own throwaway proc server, then shut THAT down instead of the real daemon - a silent
		// no-op stop. The rotate-* job workers that need a workspace load one themselves.
		return dispatchProfile{needsConfig: true}
	case "run", "affected":
		// A help/usage-only invocation (`run -h`, `affected --help`, bare `affected`)
		// must print its per-subcommand usage on the CALLER's stderr. run and affected are
		// the only daemon-adoptable verbs, so forwarding one of these would run the
		// usage path inside the daemon: usage lands on the daemon's stderr (invisible
		// here) and the client is left with a bare, silent non-zero exit. Skip both the
		// forward and the workspace load so the local dispatch prints usage directly.
		if isUsageOnlyInvocation(subArgs) {
			return dispatchProfile{needsConfig: true}
		}
		// --detach is the client's job for exactly the reason usage above is. It SUBMITS
		// the run to the daemon and reports the job id; forwarding it would have the
		// daemon submit to itself, print the id onto its own log, and leave the caller
		// with silence and exit 0 - observed before this guard existed. It needs no
		// workspace either: it hands off an argv and returns.
		if hasDetachFlag(subArgs) {
			return dispatchProfile{needsConfig: true}
		}
		// A forensic mode runs nothing, so there is no pool to share, and its report IS
		// its stdout. An adopted call has nowhere to put that: RunReply carries an exit
		// code and an error string and never output, so the daemon runs the mode in its
		// OWN process and prints the report on ITS stdout. A caller that CAPTURES the
		// child - magus\affectedImpact forks `affected --impact -o json` and decodes it -
		// then reads an empty stdout at exit 0 and reports an undecodable report. Same
		// shape as the usage bug above, one layer down.
		if sub == "affected" && isForensicAffected(subArgs) {
			return dispatchProfile{needsConfig: true}
		}
		return dispatchProfile{needsConfig: true, needsDaemonFwd: true, needsWorkspace: true}
	case "config":
		// config history/cache need the workspace; view/set/help do not.
		if len(subArgs) > 0 {
			switch subArgs[0] {
			case "view", "set", "help", "-h", "--help", "":
				return dispatchProfile{needsConfig: true, needsDaemonFwd: true}
			}
		}
		return dispatchProfile{needsConfig: true, needsDaemonFwd: true, needsWorkspace: true}
	default:
		return dispatchProfile{needsConfig: true, needsDaemonFwd: true, needsWorkspace: true}
	}
}

// bindGlobalsAfterSubcommand reads the generated config flags out of the args that
// FOLLOW the subcommand and applies them to globalCfg, so the value is present before
// the workspace preload snapshots it. See the call site for why that ordering matters.
//
// It filters to KNOWN config flags rather than parsing the whole tail, because
// stdlib flag stops dead at the first name it does not recognize - so an unknown
// local flag would hide every global flag behind it.
//
// Scanning stops at "--": past that the tokens belong to the tool being
// forwarded to. The subcommand parses these again later, which is harmless.
func bindGlobalsAfterSubcommand(subArgs []string) {
	if len(subArgs) == 0 {
		return
	}
	fs := flag.NewFlagSet("globals-after-subcommand", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.SetOutput(io.Discard)
	gen.BindFlags(fs, &globalCfg)
	takesValue := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) { takesValue[f.Name] = !flagIsBool(f) })

	var keep []string
	for i := 0; i < len(subArgs); i++ {
		a := subArgs[i]
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name, _, hasInline := strings.Cut(strings.TrimLeft(a, "-"), "=")
		wantsValue, known := takesValue[name]
		if !known {
			continue
		}
		keep = append(keep, a)
		if wantsValue && !hasInline && i+1 < len(subArgs) {
			keep = append(keep, subArgs[i+1])
			i++
		}
	}
	// Errors are not actionable here: this is a best-effort pre-read, and the
	// subcommand's own parse reports anything genuinely malformed with better context.
	_ = fs.Parse(keep)
}

// globalValueFlags is the set of "-name"/"--name" tokens for every value-taking
// global flag, derived once from the real bindings (the config-generated
// gen.BindFlags plus the display flags) rather than hand-listed, so peekSub can
// never drift from them. Two bootstrap flags are added explicitly: root and config
// precede config loading, so they are not part of the generated set. Deriving it
// closes the latent gap where a value global flag missing from the old hand-kept
// list made `magus --cache-dir x run` misread x as the subcommand.
var globalValueFlags = sync.OnceValue(func() map[string]bool {
	fs := flag.NewFlagSet("global", flag.ContinueOnError)
	gen.BindFlags(fs, &globalCfg)
	bindDisplayFlags(fs)
	// Both short forms belong here beside their long names: peekSub reads this set to
	// know a token consumes the next one, and a missing -C made `magus -C /ws run`
	// misread the path as the subcommand.
	out := map[string]bool{
		"-root": true, "--root": true, "-C": true, "--C": true,
		"-config": true, "--config": true, "-c": true, "--c": true,
	}
	fs.VisitAll(func(f *flag.Flag) {
		if !flagIsBool(f) {
			out["-"+f.Name] = true
			out["--"+f.Name] = true
		}
	})
	return out
})

// peekSub returns the subcommand and trailing args, scanning past global flags.
// Intentionally approximate: disagreement with fs.Parse costs extra work, not correctness.
func peekSub(args []string) (sub string, subArgs []string) {
	i := 0
	for i < len(args) {
		a := args[i]
		if len(a) == 0 {
			i++
			continue
		}
		// --flag=value form: consume the whole token.
		if a[0] == '-' && strings.ContainsRune(a, '=') {
			i++
			continue
		}
		// -version/--version is a subcommand in a flag's clothing: the stdlib flag
		// package special-cases -h/-help this way already (fs.Parse itself returns
		// ErrHelp for those), but has nothing built in for version, so without this
		// `magus --version` fell through to the generic dash-skip below, peekSub
		// returned no subcommand at all, and the later fs.Parse died on an
		// unregistered flag ("flag parse failed") instead of printing the version.
		if a == "-version" || a == "--version" {
			return "version", args[i+1:]
		}
		// --flag value form: consume both tokens.
		if globalValueFlags()[a] && i+1 < len(args) {
			i += 2
			continue
		}
		// Any other dash-prefixed token is a boolean/counted flag (-v, -vv).
		if a[0] == '-' {
			i++
			continue
		}
		return a, args[i+1:]
	}
	return "", nil
}

// startup runs all pre-dispatch steps (config, daemon forward, flag parse, workspace init, proc server).
// exitCode >= 0 means exit without dispatching; -1 means proceed.
func startup(rootCtx context.Context, args []string) (startupResult, int) {
	trace := newStartupTracer(startupTraceEnabled(args))
	cleanup := trace.done

	peekedSub, peekedSubArgs := peekSub(args)
	profile := resolveProfile(peekedSub, peekedSubArgs)

	if !profile.needsConfig {
		// This branch skips the main flag parse entirely, so anything written BEFORE
		// the subcommand was silently dropped: `magus -o json version` printed text
		// and exited 0, while `magus version -o json` worked. The top-level usage
		// advertises global flags as working "before or after the subcommand", so
		// bind the display flags here to make that true for these profiles too.
		if err := applyPreSubDisplayFlags(args, peekedSubArgs, peekedSub); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return startupResult{cleanup: cleanup}, exitUsage
		}
		return startupResult{
			rootCtx: rootCtx,
			sub:     peekedSub,
			subArgs: peekedSubArgs,
			trace:   trace,
			cleanup: cleanup,
		}, -1
	}

	stopEarlyRoot := trace.phase("startup.find_root_early")
	earlyRoot, _ := magus.FindRoot(extractRootFlag(args))
	stopEarlyRoot()

	stopCfgLoad := trace.phase("startup.config_load")
	cfg, err := config.LoadWithRoot(config.ExtractFlag(args), earlyRoot)
	stopCfgLoad()
	if err != nil {
		slog.Error("load config failed", slog.String("error", err.Error()))
		return startupResult{cleanup: cleanup}, 1
	}
	configgen.ApplyEnv(&cfg, os.Getenv)
	// LoadWithRoot validates the yaml; ApplyEnv then overwrites those fields.
	// Without a second pass the whole MAGUS_* surface goes unchecked while the
	// equivalent yaml is rejected. Exit 1 to match the load failure above.
	if err := config.Validate(cfg); err != nil {
		slog.Error("invalid configuration from the environment", slog.String("error", err.Error()))
		return startupResult{cleanup: cleanup}, 1
	}
	// Pass config to the workspace singletons via package-level state.
	globalCfg = cfg
	// The shared-daemon discovery below runs before the main flag parse, so peek the
	// --daemon-enabled flag early (like --root/--quiet) to let it override yaml/env for
	// this invocation. yaml/env already land via LoadWithRoot + ApplyEnv above.
	if v, set := extractDaemonEnabledFlag(args); set {
		globalCfg.Daemon.Enabled = v
	}
	// Hints default on when Hints.Enabled is nil.
	hintsOn := cfg.Hints.Enabled == nil || *cfg.Hints.Enabled
	interactive.SetHintsEnabled(hintsOn)

	global.quiet = extractQuietFlag(args)
	global.silent = extractSilentFlag(args)
	if v := extractVerbosityCount(args); v > 0 {
		global.verbose = verbosity(v)
	}
	applyDisplay()

	// Retrofit-enable the tracer if the config file set trace level (pre-config phases are missed).
	if !trace.enabled && cfg.Log.SlogLevel() <= config.LevelTrace {
		trace.enabled = true
		trace.start = time.Now()
	}

	// parentLive records whether a parent daemon is alive and reachable: true only
	// when a forward reached it but it did not adopt this subcommand (ErrNotAdoptable).
	// It gates leaf behavior below: a nested process suppresses its own server
	// only while it has a live parent to forward to.
	parentLive := false
	if profile.needsDaemonFwd {
		stopSock := trace.phase("startup.daemon_socket_lookup")
		sock := os.Getenv("MAGUS_DAEMON_SOCKET")
		stableSock := false
		if sock == "" {
			// daemon.enabled gates discovery of the SHARED, persistent daemon only.
			// When off, this invocation never adopts the stable per-user daemon and
			// runs self-contained. It does NOT disable recursion: a child with
			// MAGUS_DAEMON_SOCKET already set (below) still forwards to its parent, and
			// a top-level still stands up its own per-process pool for its children.
			if globalCfg.Daemon.Enabled {
				if s, ok := proc.LookupStableSocket(rootCtx); ok {
					sock = s
					stableSock = true
					// Propagate to child processes spawned by this invocation.
					_ = os.Setenv("MAGUS_DAEMON_SOCKET", sock)
				}
			}
		} else {
			stableSock = strings.HasSuffix(sock, "/"+proc.StableSocketName())
		}
		stopSock()
		if sock != "" {
			stopFwd := trace.phase("startup.daemon_forward")
			// Skip client-side FindRoot when forwarding to the stable daemon; the daemon walks itself.
			var fwdRoot string
			if !stableSock {
				if r, err := magus.FindRoot(""); err == nil {
					fwdRoot = r
				}
			}
			code, fwdErr := proc.Forward(rootCtx, args, version, fwdRoot)
			stopFwd()
			if fwdErr == nil {
				return startupResult{cleanup: cleanup}, code
			}
			// A call the daemon did not adopt is the normal path here: run locally
			// without alarming the user. The daemon does not adopt a subcommand that
			// never adopts (only run/affected do), nor a client whose build or protocol
			// differs from its own (version/protocol mismatch) - in every case the daemon
			// is alive and answered, it just will not take THIS call, and retrying it
			// will not help. A version mismatch is common when multiple worktrees run
			// different builds against one shared per-user daemon; it is not a failure,
			// so it must not warn. Reserve warn for a genuine forward failure (transport
			// error, dead daemon). proc.NotAdopted owns the classification - the errors
			// carry it (a NotAdopted() method).
			if proc.NotAdopted(fwdErr) {
				slog.Debug("proc forward not adopted; running locally", slog.String("error", fwdErr.Error()))
			} else {
				slog.Warn("proc forward failed; running locally", slog.String("error", fwdErr.Error()))
			}
			// parentLive is a narrower question than "not adopted": keep MAGUS_DAEMON_SOCKET
			// pointed at the parent only when it is a usable pool for deeper adoptable
			// calls. A not-adoptable subcommand leaves a live, same-version daemon worth
			// forwarding to (nested adoptable calls hit the single top-level pool; probes
			// like doctor's daemon check see the real daemon). A version/protocol mismatch
			// - like a transport failure - leaves a daemon we cannot use: clear the
			// pointer so nothing keeps dialing it, and fall through to hosting our own pool.
			parentLive = errors.Is(fwdErr, proc.ErrNotAdoptable)
			if !parentLive {
				_ = os.Unsetenv("MAGUS_DAEMON_SOCKET")
			}
		}
	}

	stopFlags := trace.phase("startup.flag_parse")
	var (
		root    string
		cfgPath string
	)
	fs := flag.NewFlagSet("magus", flag.ContinueOnError)
	fs.StringVar(&root, "root", "", "Workspace root (must precede subcommand; default: walk up from cwd to find go.mod)")
	fs.StringVar(&root, "C", "", "Short for --root")
	fs.StringVar(&cfgPath, "config", "", "Config file path (must precede subcommand; default: search magus.yaml in CWD / XDG)")
	fs.StringVar(&cfgPath, "c", "", "Short for --config")
	gen.BindFlags(fs, &globalCfg)
	bindDisplayFlags(fs)
	fs.Usage = usage
	// Parse until first non-flag arg (the subcommand). ErrHelp means an explicit -h or
	// --help in the GLOBAL position, which the flag package has already answered by
	// printing usage; it is recorded rather than swallowed so the no-subcommand branch
	// below can tell "the user asked for help" (exit 0) from "the user asked for
	// nothing" (exit 2).
	helpRequested := false
	if err := fs.Parse(args); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			stopFlags()
			slog.Error("flag parse failed", slog.String("error", err.Error()))
			return startupResult{cleanup: cleanup}, 1
		}
		helpRequested = true
	}
	applyDisplay()
	rest := fs.Args()
	// The parse above stops at the subcommand, so a global config flag written AFTER it
	// - which the usage text promises works, and which is where people naturally put it
	// - had not been read yet when the workspace preload below snapshots globalCfg into
	// the loadMagus singleton. The subcommand's own cmdParse does read it, but that runs
	// after the snapshot, so the value landed in a config nothing consulted again:
	// `magus run build --concurrency 4` silently ran at the default width, and every
	// other generated config flag was dead in that position too.
	bindGlobalsAfterSubcommand(rest)
	// globalCfg is the one the flags were bound into; cfg is the copy taken before any
	// of them were parsed, and the startup path below still reads it - for the watch
	// ignores, the daemon address, and (worst) the bootstrap limiter's width. That
	// limiter is INJECTED into the workspace and wins over m.cfg.Concurrency via
	// limOnce, so sizing it from the pre-flag copy meant `--concurrency` never governed
	// the pool from the command line in either position; only magus.yaml and
	// MAGUS_CONCURRENCY ever reached it. Re-syncing here is what makes the flag real.
	cfg = globalCfg
	stopFlags()

	// exitUsage, not 0. No subcommand is the same category as an unknown one - the
	// invocation was wrong and nothing was attempted - and that path already exits 2
	// (see dispatchSub's default). Returning 0 made bare `magus` a command that
	// reports success having done nothing, so `magus $CMD` with an empty CMD is a
	// green step in any script or CI action that builds its argv dynamically.
	//
	// An EXPLICIT `magus help` / `-h` / `--help` still exits 0: there the usage text
	// is what was asked for, so printing it IS the work succeeding. That distinction
	// is the whole of exitUsage's contract in helpers.go - 0 did what was asked, 2 was
	// asked wrong - and it is why this cannot simply key on "did we print usage".
	if len(rest) == 0 {
		if helpRequested {
			return startupResult{cleanup: cleanup}, 0
		}
		usage()
		return startupResult{cleanup: cleanup}, exitUsage
	}

	rootCtx = withTrace(rootCtx, trace)

	sub, subArgs := rest[0], rest[1:]
	rc := runConfig{watchIgnores: cfg.Watch.Ignore}

	profile = resolveProfile(sub, subArgs) // re-resolve in case peekSub was approximate

	if sub == "server" && len(subArgs) > 0 && subArgs[0] == "start" && cfg.Daemon.Address == "" {
		cfg.Daemon.Address = "unix://" + filepath.Join(proc.SockDir(), "magus-daemon.sock")
	}

	var adoptCloser func()
	switch {
	case sub == "server" && len(subArgs) > 0 && subArgs[0] == "start":
		// A help request must print usage and build no daemon, so it skips both the
		// background handoff and the in-process daemon and falls through to normal dispatch
		// (serverStart's flag parse prints the usage). Without this guard `server start -h`
		// would hit the idempotency check and report "already running" instead of help.
		if !isServerStartHelp(subArgs) {
			// By default `server start` auto-backgrounds: the parent re-execs a detached child
			// and returns once it is accepting. done==true means we are that parent (or a daemon
			// was already running, or spawning failed); only the foreground child falls through
			// to actually build and run the daemon in this process.
			if code, done := startDaemonBackground(rootCtx, cfg, subArgs); done {
				return startupResult{cleanup: cleanup}, code
			}
			startMultiWorkspaceDaemon(rootCtx, cfg, rc)
		}
	case !profile.needsWorkspace:
		// skip loadMagus + proc server for subcommands that need no workspace
	default:
		concurrency := cfg.Concurrency
		if concurrency <= 0 {
			concurrency = cache.DefaultConcurrency()
		}
		// THE site that governs: this limiter is injected into the workspace and wins over
		// m.cfg.Concurrency via limOnce, so a cap applied only in Magus.limiter never runs.
		// Announced rather than silent - a run quietly narrower than requested is as hard
		// to attribute as one that thrashes.
		if clamped, was := cache.ClampConcurrency(concurrency); was {
			slog.Warn("magus: concurrency capped to this machine",
				slog.Int("requested", concurrency), slog.Int("running_with", clamped),
				slog.Int("cpus", cache.MachineCeiling()))
			concurrency = clamped
		}
		lim := cache.NewLimiter(concurrency)
		// Host our own proc server only when there's no live daemon to forward to.
		// Any process (nested OR top-level) with a reachable daemon (parentLive)
		// runs locally as a leaf and forwards adoptable calls to that single daemon,
		// rather than standing up a second socket that fragments the concurrency pool
		// and trips doctor's `sockets` check ("multiple daemons running"). The earlier
		// `CurrentLevel() > 0` guard left a gap: a top-level non-adoptable command
		// (describe, ls, watch, ...) still hosted its own daemon even when the stable
		// `magus server start` daemon was alive. A process with no daemon to forward
		// to (parentLive == false: a true top-level, or an orphaned nested one whose
		// parent is gone) hosts its own pool. loadMagus wires the limiter into the
		// loaded workspace regardless, so a leaf still has its concurrency pool.
		leaf := parentLive
		if _, err := loadMagus(withBootstrapLimiter(rootCtx, lim), root); err == nil && !leaf {
			srv, err := proc.New(proc.Options{
				Handler: func(ctx context.Context, args []string) error {
					return dispatchAdopted(ctx, root, rc, args)
				},
				Context: rootCtx,
				Limiter: lim,
				Version: version,
				Address: cfg.Daemon.Address,
			})
			if err == nil {
				_ = os.Setenv("MAGUS_DAEMON_SOCKET", srv.Addr())
				err = srv.Start()
			}
			if err == nil {
				adoptCloser = func() { srv.Close() }
			} else {
				_ = os.Unsetenv("MAGUS_DAEMON_SOCKET")
			}
		}
	}

	finalCleanup := cleanup
	if adoptCloser != nil {
		finalCleanup = func() {
			adoptCloser()
			cleanup()
		}
	}

	return startupResult{
		rootCtx: rootCtx,
		root:    root,
		rc:      rc,
		sub:     sub,
		subArgs: subArgs,
		trace:   trace,
		cleanup: finalCleanup,
	}, -1
}

func dispatchSub(ctx context.Context, root string, rc runConfig, sub string, subArgs []string) error {
	switch sub {
	case "ls":
		return ls(ctx, root, subArgs)
	case "describe":
		return describeCmd(ctx, root, subArgs)
	case "run":
		return runTarget(ctx, root, rc, subArgs)
	case "x":
		return x(ctx, root, rc, subArgs)
	case "where":
		return whereCmd(ctx, root, subArgs)
	case "affected":
		return affected(ctx, root, rc, subArgs)
	case "query":
		return queryCmd(ctx, root, subArgs)
	case "explain":
		return explainCmd(ctx, root, subArgs)
	case "path":
		return pathCmd(ctx, root, subArgs)
	case "refs":
		return refsCmd(ctx, root, subArgs)
	case "graph":
		return graphCmd(ctx, root, subArgs)
	case "watch":
		return watchCmd(ctx, root, rc, subArgs)
	case "events":
		return eventsCmd(ctx, root, subArgs)
	case "status":
		return status(ctx, subArgs)
	case "clean":
		return cleanCmd(ctx, root, subArgs)
	case "vcs":
		return vcsCmd(ctx, root, rc, subArgs)
	case "doctor":
		return doctorCmd(ctx, root, rc, subArgs)
	case "config":
		return configCmd(ctx, root, globalCfg, subArgs)
	case "session":
		return sessionCmd(ctx, root, subArgs)
	case "memory":
		return memoryCmd(ctx, root, subArgs)
	case "notes":
		return notesCmd(ctx, root, subArgs)
	case "diff":
		return diffCmd(ctx, root, subArgs)
	case "server":
		return serverCmd(ctx, root, subArgs)
	case "mcp":
		return mcpCmd(ctx, subArgs)
	case "completion":
		return completion(subArgs)
	case "man":
		return manCmd(subArgs)
	case "init":
		return initCmd(ctx, root, subArgs)
	case "agent":
		return agentCmd(ctx, subArgs)
	case "self":
		return selfCmd(ctx, root, subArgs)
	case "buzz":
		return buzzCmd(ctx, root, subArgs)
	default:
		fmt.Fprintf(os.Stderr, "magus: unknown subcommand %q\n", sub)
		if suggestion := hint.Nearest(sub, knownSubcommands); suggestion != "" {
			interactive.Emit(os.Stderr, fmt.Sprintf("did you mean %q?", suggestion))
		}
		fmt.Fprintln(os.Stderr, "")
		usage()
		return errSilent{exitCode: 2}
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: magus [flags] <subcommand> [args]")
	group := ""
	for _, sc := range subcommands {
		if sc.Group != group {
			group = sc.Group
			fmt.Fprintf(os.Stderr, "\n%s:\n", group)
		}
		fmt.Fprintf(os.Stderr, "  %-14s %s\n", sc.Name, sc.Short)
	}
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Global flags (work before or after the subcommand):")
	fmt.Fprintln(os.Stderr, "  --help, -h           show help (top-level or subcommand)")
	fmt.Fprintln(os.Stderr, "  --output, -o <fmt>   "+outputFormatHelp)
	fmt.Fprintln(os.Stderr, "  -q, --quiet          suppress progress; only print errors + dump failing project output")
	fmt.Fprintln(os.Stderr, "  -s, --silent         like -q, but bound failing dumps (tail + log path) and bubble up only 'magus:notice:' lines")
	fmt.Fprintln(os.Stderr, "  -v, -vv, -vvv        detail (-v), plus live target output (-vv), plus tracing (-vvv)")
	fmt.Fprintln(os.Stderr, "  --concurrency N      max parallel target runs (0 = config / MAGUS_CONCURRENCY / min(NumCPU,8))")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Pre-subcommand flags (must precede the subcommand):")
	fmt.Fprintln(os.Stderr, "  --root <path>        workspace root (default: walk up to go.mod)")
	fmt.Fprintln(os.Stderr, "  --config <path>      config file (default: search magus.yaml)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Run `magus <subcommand> -h` for subcommand-specific flags.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Config file: magus.yaml (CWD or $XDG_CONFIG_HOME/magus/)")
	fmt.Fprintln(os.Stderr, "Env vars: MAGUS_* (see magus help for the full list).")
}

// snapshotGlobals captures globalCfg and global, returning a restore func that puts them
// back. runTarget/affected write flags straight into these package globals via cmdParse
// (gen.BindFlags binds the CURRENT field as each flag's default, so an unset flag keeps
// whatever a previous dispatch left there) - dispatchAdopted defers the returned restore
// so one adopted client's flags cannot bleed into the next one dispatched on this process.
func snapshotGlobals() (restore func()) {
	savedCfg, savedGlobal := globalCfg, global
	return func() { globalCfg, global = savedCfg, savedGlobal }
}

// dispatchAdopted routes adopted child args; only "run" and "affected" are accepted.
func dispatchAdopted(ctx context.Context, root string, rc runConfig, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no subcommand in forwarded args")
	}
	// Restore globalCfg/global to what they were before this dispatch touched them.
	// This fixes only the SEQUENTIAL bleed between one adopted dispatch and the next
	// on this process (e.g. a --dry-run or --cache-dir left set after this call
	// returns). Two adopted dispatches running truly CONCURRENTLY still share these
	// globals for the duration of both runs and can still stomp each other - a real
	// fix means threading config explicitly through the call chain instead of
	// reading it off ambient globals, which is out of scope here.
	defer snapshotGlobals()()
	// Strip global flags; display flags are ignored (parent's settings are authoritative).
	var (
		ignoredRoot   string
		ignoredCfg    string
		ignoredOutput string
		ignoredConc   int
		ignoredV      verbosity
		ignoredQ      bool
	)
	// nodisplayflags: this FlagSet exists to ABSORB and discard the global flags
	// a caller repeated after the subcommand, so it redeclares them into ignored
	// variables on purpose. Binding the real ones here would double-register and
	// panic, and would also write through to the live globals this is designed
	// to swallow.
	fs := flag.NewFlagSet("adopted", flag.ContinueOnError)
	fs.StringVar(&ignoredRoot, "root", "", "")
	fs.StringVar(&ignoredRoot, "C", "", "")
	fs.StringVar(&ignoredCfg, "config", "", "")
	fs.StringVar(&ignoredCfg, "c", "", "")
	fs.StringVar(&ignoredOutput, "output", "", "")
	fs.StringVar(&ignoredOutput, "o", "", "")
	fs.IntVar(&ignoredConc, "concurrency", 0, "")
	fs.Var(&ignoredV, "v", "")
	fs.BoolVar(&ignoredQ, "quiet", false, "")
	fs.BoolVar(&ignoredQ, "q", false, "")
	fs.SetOutput(io.Discard)
	_ = fs.Parse(expandVerbosityArgs(args))
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("no subcommand after global flags in forwarded args")
	}
	sub, subArgs := rest[0], rest[1:]
	switch sub {
	case "run":
		return runTarget(ctx, root, rc, subArgs)
	case "affected":
		return affected(ctx, root, rc, subArgs)
	default:
		return fmt.Errorf("%w: %q (only run, affected)", proc.ErrNotAdoptable, sub)
	}
}

// dispatchJob routes a background job submitted through proc.SubmitJob. Unlike an adopted run
// (dispatchAdopted, limited to run/affected), a job runs a maintenance command - but only one
// whose worker argv the jobs registry recognizes, so the fire-and-forget job RPC can never be
// used to run an arbitrary command. A recognized worker routes through the full dispatchSub
// command set and reuses the daemon's warm workspace (withMagus is already on ctx). This is the
// dispatch half that makes `graph build`, `clean --cache`, and the rotate workers actually run
// as jobs; without it they returned ErrNotAdoptable and the submitted job was a silent no-op.
func dispatchJob(ctx context.Context, root string, rc runConfig, args []string) error {
	if !jobs.IsWorkerArgv(args) && !isDeclaredRun(ctx, args) {
		return fmt.Errorf("%w: %q is not a registered job worker", proc.ErrNotAdoptable, strings.Join(args, " "))
	}
	return dispatchSub(ctx, root, rc, args[0], args[1:])
}

// isDeclaredRun is the second, deliberately narrow admission path into dispatchJob: a plain
// `run <target> <project>` whose target the workspace's own magusfile declares for that project.
// The review surface submits these so a reader can run a project's tests against the code they
// are looking at.
//
// It admits strictly less than a terminal's `magus run`: exactly three tokens, no flags, no
// charms, no `spell::op` form, and a target name that has to appear in the generated target graph
// rather than being taken from the caller. So the property dispatchJob exists to hold - a job RPC
// can never name an arbitrary command - still holds by construction: the allowlist is the
// magusfile, not the request.
func isDeclaredRun(ctx context.Context, args []string) bool {
	if len(args) != 3 || args[0] != "run" {
		return false
	}
	target, project := args[1], args[2]
	if strings.HasPrefix(target, "-") || strings.Contains(target, ":") {
		return false
	}
	m, ok := magusFromContext(ctx)
	if !ok {
		return false
	}
	return slices.Contains(m.ProjectTargets(ctx, project), target)
}

// daemonProvider is the single observability provider the daemon shares between its
// per-workspace registry and its bridge Magus. startMultiWorkspaceDaemon builds it once
// (it runs before the `server start` command handler); serverStart then hands it to
// startMCPWithDaemon. Same process, sequential, so the write happens-before the read.
var daemonProvider observability.Provider

// daemonRegistry is the daemon's per-workspace registry (the wsRegistry built as reg in
// startMultiWorkspaceDaemon). It is the single source of truth the WorkspaceLister reports,
// so startMCPWithDaemon publishes the bridge workspace into it (reg.adoptBridge) and /readyz
// sees the daemon's own MCP workspace as loaded, not just workspaces populated by adopted
// runs. Same process, sequential, so the write happens-before the read.
var daemonRegistry *wsRegistry

// daemonServer is the running proc server for `magus server start`. startMultiWorkspaceDaemon
// publishes it so serverStart's blocking loop can select on its Done channel: an RPC-driven
// `server stop` closes the server, and without observing that the daemon process would keep
// running after its listener was already gone. Same process, sequential, so the write
// happens-before the read.
var daemonServer *proc.Server

// daemonRuns is the daemon's live-run registry: a capture handler folded into every adopted
// dispatch's journal, tracking per-target execution state. startMultiWorkspaceDaemon builds
// it and threads it onto each dispatch's context; startMCPWithDaemon hands its Snapshot to
// the console service so the StatusService and the status SSE report active runs. Same process,
// sequential, so the write happens-before the reads.
var daemonRuns *console.RunRegistry

// daemonServices is the daemon's shared-service registry. startMultiWorkspaceDaemon builds
// it (as svcReg) and points this global at it so startMCPWithDaemon can hand its Snapshot to
// the console service, surfacing hosted services on the StatusService alongside the pool and
// runs. Same process, sequential, so the write happens-before the reads.
var daemonServices *service.Registry

// daemonTrailBase is the ONE daemon-wide activity-trail location: the bridge Magus's cache dir,
// the same base the MCP handler writes to and the ActivityService reads from. startMCPWithDaemon
// publishes it before the MCP gate, since the location is a cache dir rather than anything MCP
// owns; the proc OnJobDone callback reads it so every producer (MCP calls, background jobs)
// appends to a single trail, disambiguated by Event.Workspace rather than fragmented across
// per-workspace directories. Empty only until daemon startup reaches that call, or where the
// root does not resolve, so a job completing in that window is dropped best-effort.
var daemonTrailBase string

// startMultiWorkspaceDaemon starts the stable multi-workspace proc server for `magus server start`.
// When cfg.Daemon.Workspaces is non-empty it eagerly loads declared workspaces and applies landlock.
func startMultiWorkspaceDaemon(ctx context.Context, cfg config.Config, rc runConfig) {
	n := cfg.Concurrency
	if n <= 0 {
		n = cache.DefaultConcurrency()
	}
	lim := cache.NewLimiter(n)

	ttl := cfg.Daemon.IdleTTL
	if ttl <= 0 {
		ttl = defaultIdleTTL
	}

	// Build the ONE observability provider the whole daemon shares: every per-workspace
	// registry Magus AND the bridge Magus (startMCPWithDaemon) adopt this same instance via
	// WithProvider, so a build routed to any workspace records into the same instruments the
	// /dashboard reads, and workspace eviction never discards accumulated counters. The
	// provider is owned by the daemon process, not any workspace, and is never shut down
	// (magus.Close does not touch it), so sharing it carries no double-shutdown hazard. On
	// init failure fall back to a disabled provider so the daemon still starts.
	telCfg := observability.ConfigFromTelemetry(cfg.Telemetry, version, "")
	telCfg.LocalCollect = true
	sharedTel, terr := otlp.New(ctx, telCfg)
	if terr != nil {
		slog.Warn("daemon: telemetry init failed; dashboard metrics disabled", slog.String("error", terr.Error()))
		sharedTel, _ = otlp.New(ctx, observability.Config{})
	}
	daemonProvider = sharedTel

	// The live-run registry taps every adopted dispatch (threaded onto its context below) and
	// backs the dashboard's active-runs view via the console service.
	daemonRuns = console.NewRunRegistry()

	declared := resolveDeclaredWorkspaces(cfg.Daemon.Workspaces, os.Getenv("MAGUS_DAEMON_WORKSPACES"))
	reg := newWSRegistry(ctx, lim, ttl, sharedTel)
	reg.setDeclared(declared)
	daemonRegistry = reg // publish so startMCPWithDaemon can adopt the bridge workspace into it

	// The daemon hosts shared services so they stay warm across separate `magus run`
	// invocations. Only the stable daemon does this (a per-process proc server leaves
	// ServiceHost nil), which is why cross-invocation sharing needs the daemon. A
	// journal records each hosted service's stop command so this daemon can reap
	// orphans left by a previous one that crashed; sweep them before hosting anything.
	svcJournal, jerr := service.NewJournal(filepath.Join(proc.SockDir(), "services"))
	if jerr != nil {
		slog.Warn("daemon service journal unavailable; crash reaping disabled", slog.String("error", jerr.Error()))
	} else if res := svcJournal.Sweep(ctx); res.Reaped > 0 || res.Unreapable > 0 {
		slog.Info("daemon reaped orphaned services from a previous run",
			slog.Int("reaped", res.Reaped), slog.Int("left_running", res.Unreapable))
	}
	svcReg := service.New(service.ExecRunner{}, defaultServiceIdle, service.WithJournal(svcJournal))
	daemonServices = svcReg // publish for startMCPWithDaemon's console wiring

	if len(declared) > 0 {
		if err := reg.preloadAndApplySandbox(ctx, declared); err != nil {
			slog.Error("daemon workspace union setup failed", slog.String("error", err.Error()))
			return
		}
		reg.warmInBackground(ctx, declared)
	}

	srv, err := proc.New(proc.Options{
		Handler: func(hctx context.Context, args []string) error {
			root := proc.RootFromContext(hctx)
			if root == "" {
				cwd := proc.CwdFromContext(hctx)
				r, rerr := magus.FindRoot(cwd)
				if rerr != nil {
					return fmt.Errorf("proc: cannot locate workspace root from %s: %w", cwd, rerr)
				}
				root = r
			}
			// Fold this adopted run's journal into the live-run registry so the dashboard
			// sees its per-target execution state. BeginInvocation (in run/affected) reads
			// the sink off the context and attaches it as an extra capture handler.
			hctx = console.WithRunSink(hctx, daemonRuns)
			return reg.dispatch(hctx, root, rc, args)
		},
		OnJobDone:       recordJobActivity,
		WorkspaceLister: reg.status,
		ServiceLister:   func() []types.StatusService { return serviceStatuses(svcReg) },
		ServiceHost:     serviceHost{svcReg},
		ConfigReloader:  reg.evictAll,
		Context:         ctx,
		Limiter:         lim,
		Version:         version,
		Address:         cfg.Daemon.Address,
	})
	if err != nil {
		slog.Error("daemon server init failed", slog.String("error", err.Error()))
		return
	}
	_ = os.Setenv("MAGUS_DAEMON_SOCKET", srv.Addr())
	if err := srv.Start(); err != nil {
		_ = os.Unsetenv("MAGUS_DAEMON_SOCKET")
		slog.Error("daemon server start failed", slog.String("error", err.Error()))
		return
	}
	daemonServer = srv // publish so serverStart's blocking loop unblocks on an RPC shutdown
	go func() {
		// Tear down on either path: a signal (ctx cancelled via NotifyContext) or an RPC
		// `server stop` (which calls srv.Close, closing srv.Done). Waiting only on ctx.Done
		// missed the RPC path - srv.Close cancels the listener's own context, not this one -
		// so a stopped daemon leaked its hosted services and warm workspaces.
		select {
		case <-ctx.Done():
		case <-srv.Done():
		}
		// Drain in-flight handlers (srv.Close waits on connWg) before reg.close so a
		// workspace can't be closed under an in-flight build. Close is idempotent.
		srv.Close()
		// ctx may already be Done here (the signal path above), and Shutdown's wait
		// for an in-flight service Start returns immediately once its ctx is done -
		// so passing the cancelled ctx straight through would skip stopping anything
		// still starting. context.WithoutCancel plus a fresh bound keeps teardown
		// running (and still cancellable) instead of either hanging forever (the bug
		// this fixes) or aborting outright (worse: leaks the process).
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), service.DefaultShutdownTimeout)
		svcReg.Shutdown(shutdownCtx) // stop every hosted service on daemon teardown
		cancel()
		reg.close()
	}()
}

// applyPreSubDisplayFlags binds the global display flags that appear BEFORE the
// subcommand, for the profiles whose startup returns before the main flag parse
// (help, version, buzz - the ones needing no config or workspace).
//
// --root and --config are bound to throwaway targets: they are legal here and
// would otherwise abort the parse at the first one, taking any later -o with them.
//
// The parse error is RETURNED, not swallowed - these profiles have no later parse
// to catch it, so ignoring it made `magus --bogus -o json version` print text and
// exit 0.
//
// subArgs delimits the pre-subcommand slice rather than a search for sub, because
// slices.Index finds its FIRST occurrence and a flag value equal to the
// subcommand name truncated at the value.
func applyPreSubDisplayFlags(args, subArgs []string, sub string) error {
	pre := args
	if sub != "" {
		if n := len(args) - len(subArgs) - 1; n >= 0 && n <= len(args) {
			pre = args[:n]
		}
	}
	if len(pre) == 0 {
		return nil
	}
	fs := flag.NewFlagSet("magus", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	bindDisplayFlags(fs)
	var discardRoot, discardConfig string
	fs.StringVar(&discardRoot, "root", "", "")
	fs.StringVar(&discardRoot, "C", "", "")
	fs.StringVar(&discardConfig, "config", "", "")
	fs.StringVar(&discardConfig, "c", "", "")
	if err := fs.Parse(pre); err != nil {
		return usagef("magus: %v", err)
	}
	return nil
}

// extractRootFlag peeks the workspace root before the main flag parse. -C is the
// bound short form of --root and has to be read here too: skipping it loaded THIS
// workspace's magus.yaml while the magusfile came from the one -C names. Matching
// is exact and case-sensitive, so -c stays the short form of --config
// (config.ExtractFlag owns that one).
func extractRootFlag(args []string) string {
	for i, a := range args {
		if a == "--" {
			return ""
		}
		switch {
		case a == "-root" || a == "--root" || a == "-C" || a == "--C":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "-root="):
			return strings.TrimPrefix(a, "-root=")
		case strings.HasPrefix(a, "--root="):
			return strings.TrimPrefix(a, "--root=")
		case strings.HasPrefix(a, "-C="):
			return strings.TrimPrefix(a, "-C=")
		case strings.HasPrefix(a, "--C="):
			return strings.TrimPrefix(a, "--C=")
		}
	}
	return ""
}

// extractQuietFlag peeks --quiet/--silent before the main flag parse, because
// applyDisplay runs during early startup and decides progress suppression from
// global.quiet. --silent counts here: it is documented as "like --quiet, but
// ...", and reading only --quiet made -s byte-identical to no flag on a passing
// run - the flag parse set global.silent long after the display was configured.
func extractQuietFlag(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		switch a {
		case "-q", "--quiet", "-quiet", "-s", "--silent", "-silent":
			return true
		}
	}
	return false
}

// extractSilentFlag peeks --silent for the same reason, so the bounded-dump and
// notice-bubbling behavior is configured in the same early pass.
func extractSilentFlag(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		switch a {
		case "-s", "--silent", "-silent":
			return true
		}
	}
	return false
}

// extractDaemonEnabledFlag peeks the --daemon-enabled bool flag before the main flag
// parse, so it can gate the shared-daemon discovery that runs during early startup
// (mirrors extractRootFlag/extractQuietFlag). Returns the parsed value and whether the
// flag was present; a bare --daemon-enabled means true (Go bool-flag convention).
func extractDaemonEnabledFlag(args []string) (val, set bool) {
	for _, a := range args {
		if a == "--" {
			return false, false
		}
		switch {
		case a == "-daemon-enabled" || a == "--daemon-enabled":
			return true, true
		case strings.HasPrefix(a, "-daemon-enabled="), strings.HasPrefix(a, "--daemon-enabled="):
			_, v, _ := strings.Cut(a, "=")
			if b, err := strconv.ParseBool(v); err == nil {
				return b, true
			}
			return false, false
		}
	}
	return false, false
}

func extractVerbosityCount(args []string) int {
	n := 0
	for _, a := range expandVerbosityArgs(args) {
		if a == "--" {
			break
		}
		if a == "-v" {
			n++
		}
	}
	return n
}

func startupTraceEnabled(args []string) bool {
	if strings.EqualFold(os.Getenv("MAGUS_LOG_LEVEL"), "trace") {
		return true
	}
	return effectiveLevel(verbosity(extractVerbosityCount(args)), extractQuietFlag(args)) <= config.LevelTrace
}

// exitCodeOf maps a dispatch error to an exit code; errSilent means the caller already printed.
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	var silent errSilent
	if errors.As(err, &silent) {
		return silent.exitCode
	}
	// A misuse of the command line exits 2, not 1: the work was never attempted.
	var usage errUsage
	if errors.As(err, &usage) {
		slog.Error(err.Error())
		return exitUsage
	}
	// os.exit(code) from a magusfile: honor the requested code without an extra
	// generic error line; the magusfile already logged whatever it wanted to.
	var exitErr types.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	slog.Error(err.Error())
	// A failure that names its own status keeps it, the same question internal/proc's
	// server asks of an adopted run. A contended no-wait workspace lock exits 75
	// (EX_TEMPFAIL), so a caller can retry a busy machine and not a broken build.
	if code, ok := proc.ExitCode(err); ok {
		return code
	}
	return 1
}
