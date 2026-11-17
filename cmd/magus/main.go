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
// magus help prints the full list of top-level subcommands, in the order and with the
// descriptions internal/commands declares as the single source of truth,
// kept short here rather than a second enumeration that can drift
// from it, as this comment once did (it advertised a `magus tail` that was
// never a real subcommand).
//
// Run any subcommand with -h/--help for its own flag list.
//
//go:generate go run ../magus-utils config -config ../../internal/config/config.go -settings-out ../../internal/config/gen/settings/settings.go -settings-pkg settings -apply-env-out ../../internal/config/gen/env.go -apply-env-pkg configgen
//go:generate go run ../magus-utils cliflags -out gen/cli_flags.go -pkg flaggen
//go:generate go run ../magus-utils completions -out completions
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
	"strings"
	"sync"
	"syscall"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
	"github.com/egladman/magus/internal/gcpolicy"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/interp/mcpclient"
	"github.com/egladman/magus/internal/interp/transform"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/log/quiet"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/pipe"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/server"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/types"
)

func main() {
	// First of all: a sandboxed child starts as this binary re-executed as its
	// launcher, which must confine itself and exec the child before anything else runs.
	magus.MaybeLaunchSandbox()
	gcpolicy.RelaxStartup(gcpolicy.StartupWindow)
	// Here and not in runCLI: testscript drives runCLI in process, where the width is the
	// test binary's own.
	if commands.SchedulesSteps(os.Args[1:], globalValueFlags()) {
		spawn.RestoreGoWidth()
	}
	os.Exit(runCLI())
}

// runCLI is the CLI entry point as a function returning an exit code, so both main
// (os.Exit(runCLI())) and the testscript harness (testscript.Main) can drive the
// real command in process. It must never call os.Exit itself.
func runCLI() int {
	// First: every worker and command below reads code origins off this registry.
	if err := commands.RegisterDiagnosticCatalogs(); err != nil {
		fmt.Fprintf(os.Stderr, "magus: %v\n", err)
		return 1
	}
	// Before the worker branches: a worker runs magus\workspace().putJob.
	// That opens its own Store inside std and grades the check through this.
	job.SetCheckGrader(guard.NewCheckGrader(loadGuardDependencies))
	// A worker's stderr is the pipe its parent reads, not a terminal a display draws on.
	if os.Getenv(transform.WorkerEnv) == "1" {
		return transform.Serve(context.Background(), os.Stdin, os.Stdout, os.Stderr)
	}
	if os.Getenv(mcpclient.WorkerEnv) == "1" {
		return runClientWorker(context.Background(), os.Stdin, os.Stdout, os.Stderr)
	}
	// Ahead of everything, including log setup: a binary below the workspace's
	// required_version floor cannot get far enough into startup to discover that on
	// its own, so this has to work on nothing but os.Args and a directory walk. See
	// bootstrap_exec.go.
	maybeBootstrapExec(os.Args)

	args := quiet.ExpandVerbosityArgs(os.Args[1:])

	// After the bootstrap exec, so the workspace's own binary judges the environment: a
	// variable a newer ./magus reads is unknown to the older PATH binary that found it.
	// Before everything else, help and version included, because a retired or misspelled
	// MAGUS_* value is misconfiguration whatever the command.
	//
	// Two commands answer it differently, because an agent host runs them as its guard
	// hook and reads a failed hook as no verdict, which it treats as allow. `shell` denies
	// with the refusal as its reason. `buzz` runs the hook glue, which calls `shell` in
	// this same environment, so it carries on and lets that verdict land.
	if err := config.MisconfiguredEnv(os.Environ()); err != nil {
		switch sub, subArgs := peekSub(args); sub {
		case "shell":
			return mapExitCode(shellCommand(guard.WithEnvRefusal(context.Background(), err), subArgs))
		case "buzz":
		default:
			// No display yet: the logger is built after this check.
			fmt.Fprintf(os.Stderr, "magus: %v\n", err)
			return 1
		}
	}

	log.SetFlags(0)
	log.SetPrefix("magus: ")

	// Deferred, not tied to the cleanup closure below: runCLI has several early
	// returns, and a profile that stops only on the happy path is a profile that is
	// empty exactly when a run failed slowly. main() calls os.Exit on runCLI's RESULT,
	// so this defer still runs. No-op unless MAGUS_PPROF is set.
	defer observability.StartProfiling()()

	var rootCtx context.Context
	var stopSignals func()
	var interrupted func() (syscall.Signal, bool)
	// A broker or a foreground server answers its own signals (see lifecycle); a run's
	// Ctrl+C policy would cancel it on the first SIGTERM it means to drain on.
	if ownsItsSignals(args) {
		proc.CaptureSignals()
		rootCtx, stopSignals = context.WithCancel(context.Background())
		interrupted = func() (syscall.Signal, bool) { return 0, false }
	} else {
		rootCtx, stopSignals, interrupted = cache.WatchInterrupts(context.Background())
	}
	// Freeze the caller's lease at the trust boundary. A Buzz script may change its
	// process environment later, but it must not shed the job identity it was handed
	// before invoking another Magus entry point.
	rootCtx = types.WithJobLease(rootCtx, trail.LeaseFromEnv())
	// The verbs that are another entry point (the hook, the server) restamp this.
	rootCtx = trail.ContextWithEntryPoint(rootCtx, types.EntryPointCLI)
	rootCtx = interactive.WithHints(rootCtx)
	// An agent host's hook judged this invocation before it ran, and a person typing it
	// answers for it: the Run family must not judge it a second time in process.
	rootCtx = guard.WithHostJudged(rootCtx)
	// Stamp the binary's version onto the root context so host methods (the drift
	// classifier) can tell a dev build from the pinned release without importing main.
	rootCtx = types.WithMagusVersion(rootCtx, version)
	rootCtx = types.WithMagusBuild(rootCtx, selfBuild())
	// Adopt the ancestry a parent magus passed down, before anything can take a project
	// lock. Stamped here rather than in BeginInvocation because every subcommand that
	// locks needs it, including the ones with no invocation record of their own (clean).
	rootCtx = types.WithInvocationAncestors(rootCtx, spawn.AncestorsFromEnv())
	// env\set lands here rather than in the process environment. An adopted run gets its
	// own from the proc server, so under `magus server` one run's PATH is not another's.
	rootCtx = spawn.WithEnvOverlay(rootCtx)
	rootCtx = proveStdio(rootCtx, args)

	dispatchCtx, res, exitCode := startup(rootCtx, args)

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
		code := withInterrupt(exitCode, nil, interrupted)
		pipe.StageFromContext(rootCtx).Linger()
		pipe.RecordProcessExit(commands.ExtractRootFlag(args), globalCfg, code, interrupted)
		return code
	}

	var dispatchErr error
	switch res.sub {
	case "help", "-h", "--help":
		usage()
	case "version", "--version":
		dispatchErr = runVersion(dispatchCtx, res.subArgs)
	default:
		dispatchErr = dispatchSub(dispatchCtx, res.root, res.rc, res.sub, res.subArgs)
	}
	code := mapExitCode(dispatchErr)
	// Offer the run's pinned failures for rerun or inspection, while they are
	// still on screen. A no-op unless a run left failures on a terminal, so
	// every other command reaches it and returns immediately.
	//
	// HERE rather than inside the run commands, so that rerunning a failure
	// cannot re-enter the prompt from inside itself.
	// Not after an abort. The prompt blocks on a read that cannot be
	// interrupted, so opening one over the partial failures of a run the user
	// just told magus to stop is both unwanted and the way to get stuck there.
	if dispatchCtx.Err() == nil {
		if err := promptFailures(dispatchCtx, res.root, cache.StderrHandler()); err != nil {
			slog.ErrorContext(dispatchCtx, "", attr.Notice(""), attr.Component("magus"), attr.Error(err))
		}
	}
	// Only a stage that succeeded settles: one that failed reports its own failure,
	// never an upstream's.
	if code == 0 {
		if err := pipe.SettleProcess(dispatchCtx, commands.ExtractRootFlag(args), globalCfg); err != nil {
			dispatchErr, code = err, mapExitCode(err)
		}
	}
	cleanup()
	code = withInterrupt(code, dispatchErr, interrupted)
	pipe.StageFromContext(rootCtx).Linger()
	pipe.RecordProcessExit(commands.ExtractRootFlag(args), globalCfg, code, interrupted)
	return code
}

// withInterrupt reports a signal-stopped run as the conventional 128+N.
//
// A cancelled run's targets die with `context canceled`, which reaches
// [mapExitCode] as a nil error, so without this the process printed [fail] and
// exited 0, and `magus run test . && deploy` deployed after a Ctrl+C.
//
// Only when code == 0, so a command that already failed for its own reason keeps
// the more specific code, with one exception. A command that RETURNS the
// cancellation instead of swallowing it (awaitInvocation returns ctx.Err()) reached
// mapExitCode as a generic failure and reported 1, which says the WORK failed about a
// run the user stopped. Still gated on interrupted(), so a deadline or a
// caller-cancelled context (neither of which is a signal) keeps its own code.
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
	root    string
	rc      runConfig
	sub     string
	subArgs []string
	trace   *observability.StartupTracer
	cleanup func()
}

// isForensicAffected reports whether an `affected` invocation selects one of the forensic
// modes affectedUsage lists that reason about the set without executing a target:
// --explain, --plan, --impact. --bisect is excluded deliberately: it runs the target once
// per candidate commit, so it wants the shared pool a forward buys.
//
// It reuses affected()'s own routing predicates rather than restating them, so the forward
// decision agrees with what the handler does locally; the property commands.IsUsageOnlyInvocation
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
func resolveProfile(sub string, subArgs []string) commands.DispatchProfile {
	// Asking a command what it does must not do anything. Every subcommand's own parser
	// already prints usage and returns before it loads a workspace, but the preload here
	// runs FIRST, so `magus diff -h` opened the workspace, and opening one refreshes the
	// VCS merge-driver registration, which WRITES the tracked .gitattributes and a git
	// config entry naming the running binary. A persona doing nothing but reading help
	// found a dangling registration pointing at a throwaway path.
	//
	// The config tier stays: usage text reads it (server.address in `server -h`),
	// and reading magus.yaml writes nothing.
	if sub == "self" {
		// self update fixes a stale binary that cannot decode magus.yaml.
		return commands.DispatchProfile{}
	}
	if commands.WantsUsage(subArgs) {
		return commands.DispatchProfile{NeedsConfig: true}
	}
	switch sub {
	case "help", "version":
		// Neither reads a workspace or a config: one prints text compiled into the
		// binary, the other a stamp. version dials the server for the server half, but
		// must never FORWARD: a forwarded version would report the server's binary as
		// the client's, which is exactly the difference it exists to show.
		return commands.DispatchProfile{}
	case "buzz":
		// buzz is a standalone Buzz runner (and `buzz lsp` a stdio language server), so
		// it needs no workspace RESOLUTION and is never forwarded to a server. It does
		// need the config: a script run inside a workspace gets that workspace on its
		// context (see buzzCommand), and opening one reads magus.yaml and the MAGUS_* env,
		// the remote cache's trust set among them. Listed as config-free while it opened
		// a workspace anyway, it opened it against DEFAULTS, so a wired remote backend
		// came up with no trust set and the load failed with a message naming the very
		// setting the environment had set. That was invisible locally, where the trust
		// set is not what the yaml is consulted for, and fatal in CI.
		return commands.DispatchProfile{NeedsConfig: true}
	case "agent":
		// agent install writes embedded skill files into a repo dir; it needs no
		// workspace resolution and must not forward to a server (the install is
		// local to the caller's directory).
		return commands.DispatchProfile{NeedsConfig: true}
	case "queue":
		// Never forwarded, never preloaded. The queue works in the caller's checkout, a
		// server serving another workspace must not act on it, and the verbs that need a
		// workspace open it themselves, on the base's declarations.
		return commands.DispatchProfile{NeedsConfig: true}
	case "vcs":
		// Never forwarded, never preloaded. Every vcs verb writes the CALLER's index and
		// working tree, so a server serving another workspace must not adopt one.
		//
		// The preload matters as much. Opening a workspace refreshes the merge-driver
		// registration, which writes the tracked .gitattributes, and both merge-facing
		// verbs run while that file may be unmerged, or while the VCS holds the index.
		// loadMagus is a sync.Once singleton, so a preload wins the race and performs the
		// write each verb defers: merge-driver would dirty the tree against what git
		// staged and stop `git rebase --continue`, resolve would splice a section between
		// conflict markers. Each verb opens what it needs under its own guard.
		return commands.DispatchProfile{NeedsConfig: true}
	case "session":
		// The whole family reads or writes a file store keyed by repository identity:
		// it needs the root PATH but never the magusfile, and it must stay usable when
		// the workspace does not load: a broken magusfile is exactly when someone asks
		// what the last runs did, and an agent blocked on a person is exactly the state
		// a half-finished edit produces. Never forwarded: the hook subverb is the LAST
		// thing that should route through a remote process, notify must reach the local
		// OS notifier rather than one on the server's host, and a listing is one
		// directory read with no warm server state to reuse.
		//
		// --brief is the exception that pays for the host: it Inspects the workspace
		// (ClassifyFiles, harness names), and that load is the same class of cost a run
		// step declares with memory_mb. Starting the broker and claiming capacity keeps
		// a stack of sessionStart hooks from outspending the machine the way a stack of
		// runs cannot. The listing stays config-only.
		if commands.HasBriefFlag(subArgs) {
			return commands.DispatchProfile{NeedsConfig: true, SpawnsWork: true}
		}
		return commands.DispatchProfile{NeedsConfig: true}
	case "shell":
		// The guard an agent host calls before every tool call. It reads the root magusfile's
		// guard rules itself (loadGuardRules), and the few rules that need the workspace open
		// it lazily, so a preload would put a full workspace load in front of every tool
		// call for nothing. Never forwarded: a verdict is not adoptable work.
		return commands.DispatchProfile{NeedsConfig: true}
	case "events":
		// Reads the run-log directory; the magusfile never. Loading the workspace would
		// refresh the merge-driver registration, and a subscriber an editor spawns must
		// not write .gitattributes.
		return commands.DispatchProfile{NeedsConfig: true}
	case "job":
		// Never forwarded, never preloaded. The job store answers most of what a job verb
		// asks, the server adopts none of them, and the verbs that need the workspace (a
		// fork's notes, a wait's gates) open it themselves through loadMagus, so a fork
		// or an exit that reads only the store no longer waits on a magusfile evaluation.
		//
		// `job run` is the sharpest case: it submits one of the server's OWN jobs from a
		// VCS hook, where it promises to be a silent no-op when no server answers, and a
		// checkout carrying one unparsable local spell would log the load error on every
		// hook.
		return commands.DispatchProfile{NeedsConfig: true}
	case "ls":
		// `ls jobs` reads the job store alone; the other nouns list the workspace.
		if i := slices.IndexFunc(subArgs, func(a string) bool { return slices.Contains(lsNouns, a) }); i >= 0 && strings.HasPrefix(subArgs[i], "job") {
			return commands.DispatchProfile{NeedsConfig: true}
		}
		return commands.DispatchProfile{NeedsConfig: true, NeedsForward: true, NeedsWorkspace: true}
	case "describe":
		// As `job`: `describe job` opens the workspace and the knowledge graph itself, only
		// for the sections that read them.
		if len(subArgs) > 0 && describeAlias[subArgs[0]] == "job" {
			return commands.DispatchProfile{NeedsConfig: true}
		}
		return commands.DispatchProfile{NeedsConfig: true, NeedsForward: true, NeedsWorkspace: true}
	case "server":
		// server subcommands manage the server directly and must never forward or host
		// their own per-process proc server. start IS the server (special-cased in startup);
		// stop and job resolve the real server socket explicitly (resolveServerAddr). The old
		// default profile made `server stop` forward, and on a version-mismatched forward
		// (common across dev worktrees on one shared socket) it fell through to hosting its
		// own throwaway proc server, then shut THAT down instead of the real server: a silent
		// no-op stop. The rotate-* job workers that need a workspace load one themselves.
		if len(subArgs) > 0 && subArgs[0] == hint.ServerStatus.Leaf() {
			// It reads a running parent's pool. Forwarding starts no server.
			return commands.DispatchProfile{NeedsConfig: true, NeedsForward: true}
		}
		// The broker's config tier is only for concurrency_profile.
		return commands.DispatchProfile{NeedsConfig: true}
	case "mcp":
		// Never forwarded: this process's stdin and stdout ARE the protocol, and a server
		// that adopted the call would serve its own. The preload opens the workspace the
		// host launched it in and hosts the proc server the tools' runs share.
		return commands.DispatchProfile{NeedsConfig: true, NeedsWorkspace: true, HostOnly: true}
	case "run", "affected":
		// A help/usage-only invocation (`run -h`, `affected --help`, bare `affected`)
		// must print its per-subcommand usage on the CALLER's stderr. run and affected are
		// the only server-adoptable verbs, so forwarding one of these would run the
		// usage path inside the server: usage lands on the server's stderr (invisible
		// here) and the client is left with a bare, silent non-zero exit. Skip both the
		// forward and the workspace load so the local dispatch prints usage directly.
		if commands.IsUsageOnlyInvocation(subArgs) {
			return commands.DispatchProfile{NeedsConfig: true}
		}
		// --detach starts a copy of this invocation in the background and returns, so the
		// work, the workspace load and the broker all belong to that copy. Forwarded to a
		// server, the pid and log path it prints would land in the server's log instead
		// of in front of the person who asked.
		if commands.HasDetachFlag(subArgs) {
			return commands.DispatchProfile{NeedsConfig: true}
		}
		// A forensic mode runs nothing, so there is no pool to share, and its report IS
		// its stdout. An adopted call has nowhere to put that: RunReply carries an exit
		// code and an error string and never output, so the server runs the mode in its
		// OWN process and prints the report on ITS stdout.
		// A caller capturing the child reads an empty stdout at exit 0.
		// magus\change().impact is one: the usage bug above, a layer down.
		if sub == "affected" && isForensicAffected(subArgs) {
			return commands.DispatchProfile{NeedsConfig: true}
		}
		// The saved plan is on THIS process's stdin, which a forward does not carry. Under
		// --dry-run it only renders the plan, so it loads nothing.
		if sub == "run" && runReadsPlan(subArgs) {
			if globalCfg.DryRun {
				return commands.DispatchProfile{NeedsConfig: true}
			}
			return commands.DispatchProfile{NeedsConfig: true, NeedsWorkspace: true, SpawnsWork: true}
		}
		return commands.DispatchProfile{NeedsConfig: true, NeedsForward: true, NeedsWorkspace: true, SpawnsWork: true}
	case "config":
		// config history/cache need the workspace; view/set/help do not.
		if len(subArgs) > 0 {
			switch subArgs[0] {
			case "view", "set", "help", "-h", "--help", "":
				return commands.DispatchProfile{NeedsConfig: true, NeedsForward: true}
			}
		}
		return commands.DispatchProfile{NeedsConfig: true, NeedsForward: true, NeedsWorkspace: true}
	case "query", "explain", "path", "refs":
		// A graph read asks a running server first and opens the workspace itself only
		// when none answers, so a preload and a per-process pool would be paid for a read
		// that spawns no child, and a forward would only be refused: the server adopts run
		// and affected alone.
		return commands.DispatchProfile{NeedsConfig: true}
	case "graph":
		// stats and export are graph reads too: the stored graph answers them, and the
		// workspace opens lazily only if that graph is stale (see inspectForRead), so a
		// preload would put the full load the lazy open exists to skip in front of them.
		// build runs the symbol indexers, which need the pool the preload hosts, and the
		// other subcommands keep the default with it.
		if len(subArgs) > 0 && (subArgs[0] == hint.GraphStats.Leaf() || subArgs[0] == hint.GraphExport.Leaf()) {
			return commands.DispatchProfile{NeedsConfig: true}
		}
		return commands.DispatchProfile{NeedsConfig: true, NeedsForward: true, NeedsWorkspace: true}
	default:
		return commands.DispatchProfile{NeedsConfig: true, NeedsForward: true, NeedsWorkspace: true}
	}
}

// finalizeConfig runs once every global flag, in either position, is bound into
// globalCfg: it applies the verbosity flags and validates the result. Flags overwrite
// yaml and env, so without this pass `--log-level bogus` ran with a value the yaml
// loader refuses.
func finalizeConfig() error {
	applyDisplay()
	if err := config.Validate(globalCfg); err != nil {
		return err
	}
	// -o jsonl has to decide the CACHE logger's format here, before dispatch: a
	// command with NeedsWorkspace preloads the workspace (loadMagus, a sync.Once
	// singleton) ahead of the verb's own handler, which builds the cache from
	// globalCfg.Log.Format at that moment. Setting it inside runTarget/runAffected
	// was too late -- the preload had already won the race and built a pretty
	// logger no later mutation could replace. applyDisplay's own jsonl case (the
	// process-wide default logger for general diagnostics) reads global.output
	// directly for the same reason, so it needs no such ordering fix.
	//
	// After Validate, which refuses "jsonl" from magus.yaml, env and --log-format:
	// only -o jsonl opens the report stream the withheld results go to.
	if global.output == string(outputJSONL) {
		globalCfg.Log.Format = "jsonl"
	}
	return nil
}

// bindGlobalsAfterSubcommand reads the generated config flags out of the args that
// FOLLOW the subcommand and applies them to globalCfg, so the value is present before
// the workspace preload snapshots it. See the call site for why that ordering matters.
//
// It filters to KNOWN config flags rather than parsing the whole tail, because
// stdlib flag stops dead at the first name it does not recognize, so an unknown
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
	flaggen.BindFlags(fs, &globalCfg)
	takesValue := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) { takesValue[f.Name] = !commands.FlagIsBool(f) })

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

// globalValueFlags is the set of value-taking global flags, derived from the bindings
// this package makes (see commands.GlobalValueFlags).
var globalValueFlags = sync.OnceValue(func() map[string]bool {
	return commands.GlobalValueFlags(func(fs *flag.FlagSet) {
		flaggen.BindFlags(fs, &globalCfg)
		bindDisplayFlags(fs)
	})
})

// peekSub is commands.PeekSub over the global flags this package binds.
func peekSub(args []string) (sub string, subArgs []string) {
	return commands.PeekSub(args, globalValueFlags())
}

// startup runs all pre-dispatch steps (config, server forward, flag parse, workspace init, proc server).
// exitCode >= 0 means exit without dispatching; -1 means proceed.
func startup(rootCtx context.Context, args []string) (context.Context, startupResult, int) {
	trace := observability.NewStartupTracer(startupTraceEnabled(args))
	cleanup := trace.Done

	peekedSub, peekedSubArgs := peekSub(args)
	profile := resolveProfile(peekedSub, peekedSubArgs)
	// Before the first applyDisplay below, so the workspace preload already logs under it.
	if profile.HostOnly {
		quietForced.Store(true)
	}

	if !profile.NeedsConfig {
		// This branch skips the main flag parse entirely, so anything written BEFORE
		// the subcommand was silently dropped: `magus -o json version` printed text
		// and exited 0, while `magus version -o json` worked. The top-level usage
		// advertises global flags as working "before or after the subcommand", so
		// bind the display flags here to make that true for these profiles too.
		if err := applyPreSubDisplayFlags(args, peekedSubArgs, peekedSub); err != nil {
			// The display flags are what failed to parse, so no display was installed.
			fmt.Fprintln(os.Stderr, err)
			return rootCtx, startupResult{cleanup: cleanup}, exit.Usage
		}
		return rootCtx, startupResult{
			sub:     peekedSub,
			subArgs: peekedSubArgs,
			trace:   trace,
			cleanup: cleanup,
		}, -1
	}

	stopEarlyRoot := trace.Phase("startup.find_root_early")
	earlyRoot, _ := workspace.FindRoot(commands.ExtractRootFlag(args))
	stopEarlyRoot()

	stopCfgLoad := trace.Phase("startup.config_load")
	cfg, err := config.LoadWithRoot(config.ExtractFlag(args), earlyRoot)
	stopCfgLoad()
	if err != nil {
		err = config.WithRunningVersion(err, version)
		if code, answered := refuseUnjudgedHook(rootCtx, earlyRoot, peekedSub, peekedSubArgs, err); answered {
			return rootCtx, startupResult{cleanup: cleanup}, code
		}
		// Printed rather than logged: the unknown-key error names a file, a line and a
		// suggestion per line, and slog's text handler escapes every quote and newline
		// in it back into one unreadable run.
		fmt.Fprintf(os.Stderr, "magus: %v\n", err)
		return rootCtx, startupResult{cleanup: cleanup}, 1
	}
	// LoadWithRoot validates the yaml; ApplyEnv then overwrites those fields.
	// Without a second pass the whole set of MAGUS_* variables goes unchecked while the
	// equivalent yaml is rejected. Printed and exiting 1 like the load failure
	// above, for the same reason: it is the same multi-line validator text.
	//
	// Retired names are hidden from it: runCLI already reported them (MGS1046), and the
	// `buzz` it lets through must not exit here instead, or the hook glue never runs.
	knownEnv := func(name string) string {
		if !config.KnownEnvVar(name) {
			return ""
		}
		return os.Getenv(name)
	}
	if err := errors.Join(configgen.ApplyEnv(&cfg, knownEnv), config.Validate(cfg)); err != nil {
		err = fmt.Errorf("invalid configuration from the environment: %w", err)
		if code, answered := refuseUnjudgedHook(rootCtx, earlyRoot, peekedSub, peekedSubArgs, err); answered {
			return rootCtx, startupResult{cleanup: cleanup}, code
		}
		fmt.Fprintf(os.Stderr, "magus: %v\n", err)
		return rootCtx, startupResult{cleanup: cleanup}, 1
	}
	// A hook glue names and passes the repair from these lines alone, never a copy of the rules.
	if err := guard.ExportRepairLines(earlyRoot); err != nil {
		fmt.Fprintf(os.Stderr, "magus: %v\n", err)
	}
	// Pass config to the workspace singletons via package-level state.
	globalCfg = cfg
	// The shared-server discovery below runs before the main flag parse, so peek the
	// --server-enabled flag early (like --root/--quiet) to let it override yaml/env for
	// this invocation. yaml/env already land via LoadWithRoot + ApplyEnv above.
	if v, set := commands.ExtractServerEnabledFlag(args); set {
		globalCfg.Server.Enabled = v
	}
	// Hints default on when Hints.Enabled is nil.
	hintsOn := cfg.Hints.Enabled == nil || *cfg.Hints.Enabled
	interactive.SetHintsEnabled(hintsOn)

	global.quiet = commands.ExtractQuietFlag(args)
	global.silent = commands.ExtractSilentFlag(args)
	if v := commands.ExtractVerbosityCount(args); v > 0 {
		global.verbose = quiet.Verbosity(v)
	}
	applyDisplay()

	// Retrofit-enable the tracer if the config file set trace level (pre-config phases are missed).
	if !trace.Enabled() && cfg.Log.SlogLevel() <= config.LevelTrace {
		trace.Enable()
	}

	// parentLive records whether a parent proc server is alive and reachable: true only
	// when a forward reached it but it did not adopt this subcommand (ErrNotAdoptable).
	// It gates leaf behavior below: a nested process suppresses its own server
	// only while it has a live parent to forward to.
	parentLive := false
	if profile.NeedsForward {
		stopSock := trace.Phase("startup.socket_lookup")
		sock := os.Getenv(proc.SocketEnv)
		// topLevel: no parent exported a socket, so this process is the head of its own
		// tree rather than a magus a magusfile spawned.
		topLevel := sock == ""
		// A TOP-LEVEL run stays in this process. The server executes an adopted run in its
		// own process, where the run's console output goes to the server's log and the
		// caller's terminal shows nothing at all (measured, not theoretical). What a run
		// needs from outside is the host's capacity, and the broker holds that. A NESTED
		// call still forwards to the socket its parent exported, which is the parent's own
		// process and prints where the parent does.
		//
		// server.enabled gates discovery of the server only. When off, this invocation
		// never adopts it and runs self-contained. It does NOT disable recursion: a child
		// with MAGUS_PROC_SOCKET already set still forwards to its parent, and a
		// top-level still stands up its own per-process pool for its children.
		if topLevel && !profile.SpawnsWork && globalCfg.Server.Enabled {
			if s, ok := proc.LookupServerSocket(rootCtx); ok {
				sock = s
				// Propagate to child processes spawned by this invocation.
				_ = os.Setenv(proc.SocketEnv, sock)
				_ = os.Setenv(proc.TokenEnv, proc.ReadToken(sock))
			}
		}
		stopSock()
		if sock != "" {
			stopFwd := trace.Phase("startup.forward")
			// Flags are unparsed, so this is magus.yaml and the environment.
			// A --sandbox flag rides in args (see sandbox.Adoption).
			fwdCtx := rootCtx
			if globalCfg.Sandbox.Mode.Enabled() {
				fwdCtx = proc.WithSandboxFloor(rootCtx, globalCfg.Sandbox.Mode)
			}
			// earlyRoot, not the cwd's root: --root is not parsed yet, and a run forwarded
			// without it executes in whatever workspace the receiver resolves from the cwd.
			code, fwdErr := proc.Forward(fwdCtx, args, version, earlyRoot)
			stopFwd()
			if fwdErr == nil {
				return rootCtx, startupResult{cleanup: cleanup}, code
			}
			logForwardFallback(rootCtx, slog.Default(), fwdErr)
			// Keep MAGUS_PROC_SOCKET on the parent only while it can serve.
			// A refused or dead parent is cleared and we host our own pool,
			// which keeps MGS3048 to once per run.
			parentLive = errors.Is(fwdErr, proc.ErrNotAdoptable)
			if !parentLive {
				_ = os.Unsetenv(proc.SocketEnv)
				_ = os.Unsetenv(proc.TokenEnv)
			}
		}
	}

	stopFlags := trace.Phase("startup.flag_parse")
	var (
		root    string
		cfgPath string
	)
	fs := flag.NewFlagSet("magus", flag.ContinueOnError)
	bindGlobalFlags(fs, &root, &cfgPath)
	bindDisplayFlags(fs)
	fs.Usage = usage
	// Parse until first non-flag arg (the subcommand). ErrHelp means an explicit -h or
	// --help in the GLOBAL position, which the flag package has already answered by
	// printing usage; it is recorded rather than swallowed so the no-subcommand branch
	// below can tell "the user asked for help" (exit 0) from "the user asked for
	// nothing" (exit 2).
	helpRequested := false
	if err := command.Parse(fs, args); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			stopFlags()
			slog.Error("flag parse failed", attr.Error(err))
			return rootCtx, startupResult{cleanup: cleanup}, 1
		}
		helpRequested = true
	}
	applyDisplay()
	rest := fs.Args()
	// The parse above stops at the subcommand, so a global config flag written AFTER it
	// - which the usage text promises works, and which is where people naturally put it
	// - had not been read yet when the workspace preload below snapshots globalCfg into
	// the loadMagus singleton. The subcommand's own commandParse does read it, but that runs
	// after the snapshot, so the value landed in a config nothing consulted again:
	// `magus run build --concurrency 4` silently ran at the default width, and every
	// other generated config flag was dead in that position too.
	bindGlobalsAfterSubcommand(rest)
	if err := refuseOutputOnPath(global.output, rest); err != nil {
		stopFlags()
		slog.ErrorContext(rootCtx, "", attr.Notice(""), attr.Component("magus"), attr.Error(err))
		return rootCtx, startupResult{cleanup: cleanup}, exit.Usage
	}
	if err := errors.Join(finalizeConfig(), sandbox.RefuseInheritedWeakened(globalCfg.Sandbox.Mode, spawn.CurrentLevel() != 0, types.SandboxMode(os.Getenv(spawn.SandboxEnvVar)))); err != nil {
		stopFlags()
		if code, answered := refuseUnjudgedHook(rootCtx, earlyRoot, peekedSub, peekedSubArgs, fmt.Errorf("invalid configuration from flags: %w", err)); answered {
			return rootCtx, startupResult{cleanup: cleanup}, code
		}
		slog.ErrorContext(rootCtx, "invalid configuration from flags", attr.Notice(""), attr.Component("magus"), attr.Error(err))
		return rootCtx, startupResult{cleanup: cleanup}, 1
	}
	// globalCfg is the one the flags were bound into; cfg is the copy taken before any
	// of them were parsed, and the startup path below still reads it: for the watch
	// ignores, the server address, and (worst) the bootstrap limiter's width. That
	// limiter is INJECTED into the workspace and wins over m.cfg.Concurrency via
	// limOnce, so sizing it from the pre-flag copy meant `--concurrency` never governed
	// the pool from the command line in either position; only magus.yaml and
	// MAGUS_CONCURRENCY ever reached it. Re-syncing here is what makes the flag real.
	cfg = globalCfg
	stopFlags()

	// exit.Usage, not 0. No subcommand is the same category as an unknown one (the
	// invocation was wrong and nothing was attempted), and that path already exits 2
	// (see dispatchSub's default). Returning 0 made bare `magus` a command that
	// reports success having done nothing, so `magus $CMD` with an empty CMD is a
	// green step in any script or CI action that builds its argv dynamically.
	//
	// An EXPLICIT `magus help` / `-h` / `--help` still exits 0: there the usage text
	// is what was asked for, so printing it IS the work succeeding. That distinction
	// is the whole of exit.Usage's contract in helpers.go (0 did what was asked, 2 was
	// asked wrong), and it is why this cannot simply key on "did we print usage".
	if len(rest) == 0 {
		if helpRequested {
			return rootCtx, startupResult{cleanup: cleanup}, 0
		}
		usage()
		return rootCtx, startupResult{cleanup: cleanup}, exit.Usage
	}

	rootCtx = observability.WithTrace(rootCtx, trace)
	if trace.Profile() != nil {
		rootCtx = buzz.WithProfile(rootCtx, trace.Profile())
	}

	sub, subArgs := rest[0], rest[1:]
	rc := runConfig{watchIgnores: cfg.Watch.Ignore}

	profile = resolveProfile(sub, subArgs) // re-resolve in case peekSub was approximate

	// Work that claims host capacity needs the broker before it starts. Only now,
	// after the flag parse, so --broker off
	// is honored and -o decides the notice's shape.
	if profile.SpawnsWork && globalCfg.Broker.Resolved() != types.BrokerOff {
		stopBroker := trace.Phase("startup.broker")
		pid := ensureBroker(rootCtx)
		stopBroker()
		noticeBroker(rootCtx, pid)
	}

	runsServer := sub == "server" && server.IsServerRun(subArgs)
	if runsServer && cfg.Server.Address == "" {
		cfg.Server.Address = proc.ServerDefaultAddr()
	}

	var adoptCloser func()
	switch {
	case runsServer:
		// By default `server start` auto-backgrounds: the parent spawns a detached
		// `server --foreground` and returns once it is accepting. done==true means we are
		// that parent (or a server was already running, or spawning failed); only a
		// foreground server falls through to build and run it in this process.
		if code, done := server.StartBackground(rootCtx, cfg, subArgs, server.LogPath(), os.Args[1:]); done {
			return rootCtx, startupResult{cleanup: cleanup}, code
		}
		startServer(rootCtx, cfg, rc)
	case !profile.NeedsWorkspace:
		// skip loadMagus + proc server for subcommands that need no workspace
	default:
		concurrency := cfg.Concurrency
		if concurrency <= 0 {
			concurrency = cache.ProfileConcurrency(cfg.ConcurrencyProfile)
		}
		// THE site that governs: this limiter is injected into the workspace and wins over
		// m.cfg.Concurrency via limOnce, so a cap applied only in Magus.limiter never runs.
		// Announced rather than silent: a run quietly narrower than requested is as hard
		// to attribute as one that thrashes.
		if clamped, was := cache.ClampConcurrency(concurrency); was {
			slog.With(attr.Component("magus")).Warn("concurrency capped to this machine",
				slog.Int("requested", concurrency), slog.Int("running_with", clamped),
				slog.Int("cpus", cache.MachineCeiling()))
			concurrency = clamped
		}
		lim := cache.NewLimiter(concurrency)
		// Host our own proc server only when there's no live server to forward to.
		// Any process (nested OR top-level) with a reachable server (parentLive)
		// runs locally as a leaf and forwards adoptable calls to that single server,
		// rather than standing up a second socket that fragments the concurrency pool
		// and trips doctor's `sockets` check ("multiple servers running"). The earlier
		// `CurrentLevel() > 0` guard left a gap: a top-level non-adoptable command
		// (describe, ls, watch, ...) still hosted its own proc server even when
		// `magus server` was alive. A process with no server to forward
		// to (parentLive == false: a true top-level, or an orphaned nested one whose
		// parent is gone) hosts its own pool. loadMagus wires the limiter into the
		// loaded workspace regardless, so a leaf still has its concurrency pool.
		leaf := parentLive
		if m, err := loadMagus(withBootstrapLimiter(rootCtx, lim), root); err == nil && !leaf {
			srv, err := proc.New(rootCtx, proc.Options{
				Handler: poolHandler(m.Root(), func(ctx context.Context, args []string) error {
					return dispatchAdopted(ctx, root, rc, args)
				}),
				// No Address: server.address is where the SERVER listens, and a per-process
				// pool bound there would answer as the server for as long as this
				// command ran.
				Limiter: lim,
				Version: version,
			})
			if err == nil {
				_ = os.Setenv(proc.SocketEnv, srv.Addr())
				_ = os.Setenv(proc.TokenEnv, srv.Token())
				err = srv.Start(rootCtx)
			}
			if err == nil {
				adoptCloser = func() { srv.Close() }
			} else {
				_ = os.Unsetenv(proc.SocketEnv)
				_ = os.Unsetenv(proc.TokenEnv)
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

	return rootCtx, startupResult{
		root:    root,
		rc:      rc,
		sub:     sub,
		subArgs: subArgs,
		trace:   trace,
		cleanup: finalCleanup,
	}, -1
}

// logForwardFallback says why a forwarded call runs locally instead.
// A subcommand that never adopts is routine, so it stays at debug.
// A server from another build warns with MGS3048, or it hides a stale
// daemon. Anything else failed the forward.
func logForwardFallback(ctx context.Context, log *slog.Logger, err error) {
	switch {
	case errors.Is(err, proc.ErrNotAdoptable):
		log.DebugContext(ctx, "proc forward not adopted; running locally", attr.Error(err))
	case proc.NotAdopted(err):
		log.WarnContext(ctx, types.FormatDiagnostic(types.PeerFromAnotherBuild,
			"a magus server or pool parent from another build did not adopt this call, so it runs locally until that process restarts"),
			attr.Error(err))
	default:
		log.WarnContext(ctx, "proc forward failed; running locally", attr.Error(err))
	}
}

func usage() { commands.WriteRootUsage(os.Stderr, printRootFlags) }

// snapshotGlobals captures globalCfg and global, returning a restore func that puts them
// back. runTarget/affected write flags straight into these package globals via commandParse
// (flaggen.BindFlags binds the CURRENT field as each flag's default, so an unset flag keeps
// whatever a previous dispatch left there); dispatchAdopted defers the returned restore
// so one adopted client's flags cannot bleed into the next one dispatched on this process.
func snapshotGlobals() (restore func()) {
	savedCfg, savedGlobal := globalCfg, global
	return func() { globalCfg, global = savedCfg, savedGlobal }
}

// poolHandler is a per-process pool's handler: it hands dispatch the runs forwarded from
// this process's own workspace, at poolRoot, and declines every other one, so a nested
// magus aimed at another workspace runs that work itself, under that workspace's locks.
// dispatch only ever opens poolRoot; adopting a foreign run would execute it against
// this tree, where the project locks this process holds refuse it (MGS3007).
func poolHandler(poolRoot string, dispatch func(context.Context, []string) error) func(context.Context, []string) error {
	return func(ctx context.Context, args []string) error {
		if client := proc.RootFromContext(ctx); !sameDir(client, poolRoot) {
			return fmt.Errorf("%w: the client's workspace %q is not this process's %s", proc.ErrNotAdoptable, client, poolRoot)
		}
		return dispatch(ctx, args)
	}
}

// sameDir reports whether a and b name one directory, either spelled through a symlink:
// a cwd under macOS's /var reads back as /private/var.
func sameDir(a, b string) bool {
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
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
	// globals for the duration of both runs and can still stomp each other; a real
	// fix means threading config explicitly through the call chain instead of
	// reading it off ambient globals, which is out of scope here.
	defer snapshotGlobals()()
	// Strip global flags; display flags are ignored (parent's settings are authoritative).
	var (
		ignoredRoot   string
		ignoredCfg    string
		ignoredOutput string
		ignoredConc   int
		ignoredV      quiet.Verbosity
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
	_ = fs.Parse(quiet.ExpandVerbosityArgs(args))
	rest := fs.Args()
	if len(rest) == 0 {
		return fmt.Errorf("no subcommand after global flags in forwarded args")
	}
	sub, subArgs := rest[0], rest[1:]
	if sub != "run" && sub != "affected" {
		return fmt.Errorf("%w: %q (only run, affected)", proc.ErrNotAdoptable, sub)
	}
	adoption := sandbox.Adoption{
		Floor:     proc.SandboxFloorFromContext(ctx),
		Workspace: func() (types.SandboxMode, error) { return workspaceSandboxMode(ctx, root) },
		Declined:  proc.ErrNotAdoptable,
	}
	if err := adoption.Check(subArgs); err != nil {
		return err
	}
	if sub == "run" {
		return runTarget(ctx, root, rc, subArgs)
	}
	return affected(ctx, root, rc, subArgs)
}

// dispatchJob routes a background job submitted through proc.SubmitJob. Unlike an adopted run
// (dispatchAdopted, limited to run/affected), a job runs a maintenance command, but only one
// whose worker argv the jobs registry recognizes, so the fire-and-forget job RPC can never be
// used to run an arbitrary command. A recognized worker routes through the full dispatchSub
// command set and reuses the server's warm workspace (withMagus is already on ctx). This is the
// dispatch half that makes `graph build`, `clean --cache`, and the rotate workers actually run
// as jobs; without it they returned ErrNotAdoptable and the submitted job was a silent no-op.
func dispatchJob(ctx context.Context, root string, rc runConfig, args []string) error {
	if !job.IsWorkerArgv(args) && !commands.IsDeclaredRun(ctx, args, declaredTargets(ctx)) {
		return fmt.Errorf("%w: %q is not a registered job worker", proc.ErrNotAdoptable, strings.Join(args, " "))
	}
	return dispatchSub(ctx, root, rc, args[0], args[1:])
}

// applyPreSubDisplayFlags binds the global display flags that appear BEFORE the
// subcommand, for the profiles whose startup returns before the main flag parse
// (help, version, buzz: the ones needing no config or workspace).
//
// --root and --config are bound to throwaway targets: they are legal here and
// would otherwise abort the parse at the first one, taking any later -o with them.
//
// The parse error is RETURNED, not swallowed: these profiles have no later parse
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
	if err := command.Parse(fs, pre); err != nil {
		return exit.Usagef("magus: %v", err)
	}
	return nil
}

func startupTraceEnabled(args []string) bool {
	if strings.EqualFold(os.Getenv("MAGUS_LOG_LEVEL"), "trace") {
		return true
	}
	return quiet.EffectiveLevel(quiet.Verbosity(commands.ExtractVerbosityCount(args)), commands.ExtractQuietFlag(args)) <= config.LevelTrace
}

// declaredTargets lists the targets the workspace on ctx declares for a project, or nothing
// when ctx carries none.
func declaredTargets(ctx context.Context) func(context.Context, string) []string {
	m, ok := magusFromContext(ctx)
	if !ok {
		return nil
	}
	return m.ProjectTargets
}

// workspaceSandboxMode is the mode a run of the workspace at root in this process runs
// under. The sandbox is applied as the run itself is about to, so a mode this host
// cannot honor (required without landlock) is the error the run would have met.
func workspaceSandboxMode(ctx context.Context, root string) (types.SandboxMode, error) {
	m, err := loadMagus(ctx, root)
	if err != nil {
		return "", err
	}
	if _, err := m.ApplySandbox(ctx); err != nil {
		return "", err
	}
	return m.SandboxMode(), nil
}
