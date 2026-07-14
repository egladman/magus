// Command magus is the magus CLI: a standalone build orchestrator and
// content-addressed cache for multi-language monorepos, and an evolution of
// Mage.
//
// magus provides workspace-aware subcommands for building, testing, linting,
// and inspecting projects without requiring Mage to be installed. It reads
// optional configuration from magus.yaml (XDG or CWD) and MAGUS_* environment
// variables.
//
// Usage:
//
//	magus ls                            list all discovered projects
//	magus describe <noun>               define a concept and list all entities (tools|targets|projects|workspaces|mcp-tools)
//	magus run <target> [project...]     run a target for selected projects (use --graph for dependency view)
//	magus x [filter...]                 interactive shorthand: pick project + target
//	magus where [filter...]             print the absolute path of a project
//	magus tail [-f] [-n N] [target]     stream the most recent cached log (interactive only)
//	magus affected <target>             run a target for VCS-diff affected projects
//	magus affected --plan               emit shard plan JSON for CI fan-out
//	magus graph <deps|export|stats>     the graphs as objects: project DAG, knowledge-graph export, shape stats
//	magus watch [flags]                 emit changed paths (pipe into affected --stdin)
//	magus status [flags]                inspect the concurrency pool of a running parent magus
//	magus doctor                        validate the workspace
//	magus config <subcommand>           view or update magus configuration
//	magus server <start|stop>            manage the persistent daemon (MCP starts alongside it)
//	magus completion <shell>            print a shell completion script
//	magus init [flags]                  bootstrap a workspace (magus.yaml + magusfile.buzz + merge driver)
//	magus self update [flags]           update magus to the latest release
//	magus version                       print version info
//	magus help                          show this message
//
// Run any subcommand with -h/--help for its own flag list.
//
//go:generate go run ../magus-utils config -config ../../internal/config/config.go -out gen/config_flags.go -fields-out ../../schema/gen/fields.go -bind-out gen/bind.go -apply-env-out ../../internal/config/gen/env.go
//go:generate go run ../magus-configdocs -out ../../docs/config.md
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
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/observability/otlp"
	"github.com/egladman/magus/internal/proc"
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

	args := expandVerbosityArgs(os.Args[1:])

	rootCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)

	res, exitCode := startup(rootCtx, args)

	cleanup := func() {
		if res.cleanup != nil {
			res.cleanup()
		}
		stopSignals()
	}

	if exitCode >= 0 {
		cleanup()
		return exitCode
	}

	code := 0
	switch res.sub {
	case "help", "-h", "--help":
		usage()
	case "version", "-v", "--version":
		runVersion(res.subArgs)
	default:
		code = exitCodeOf(dispatchSub(res.rootCtx, res.root, res.rc, res.sub, res.subArgs))
	}
	cleanup()
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

// resolveProfile returns the work profile for a subcommand; defaults to "needs everything".
func resolveProfile(sub string, subArgs []string) dispatchProfile {
	switch sub {
	case "help", "version", "buzz":
		// buzz is a standalone Buzz runner (and `buzz lsp` a stdio language server);
		// both analyze source text directly, needing no workspace, config, or daemon.
		return dispatchProfile{}
	case "completion", "self":
		return dispatchProfile{needsConfig: true}
	case "agent":
		// agent install writes embedded skill files into a repo dir; it needs no
		// workspace resolution and must not forward to a daemon (the install is
		// local to the caller's directory).
		return dispatchProfile{needsConfig: true}
	case "status":
		return dispatchProfile{needsConfig: true, needsDaemonFwd: true}
	case "run", "affected":
		// A help/usage-only invocation (`run -h`, `affected --help`, bare `affected`)
		// must print its per-verb usage on the CALLER's stderr. run and affected are
		// the only daemon-adoptable verbs, so forwarding one of these would run the
		// usage path inside the daemon: usage lands on the daemon's stderr (invisible
		// here) and the client is left with a bare, silent non-zero exit. Skip both the
		// forward and the workspace load so the local dispatch prints usage directly.
		if isUsageOnlyInvocation(subArgs) {
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
	out := map[string]bool{
		"-root": true, "--root": true,
		"-config": true, "--config": true,
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
	// Pass config to the workspace singletons via package-level state.
	globalCfg = cfg
	// Hints default on when Hints.Enabled is nil.
	hintsOn := cfg.Hints.Enabled == nil || *cfg.Hints.Enabled
	interactive.SetEnabled(hintsOn)

	global.quiet = extractQuietFlag(args)
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
	// when a forward reached it but it declined this subcommand (ErrNotAdoptable).
	// It gates leaf behavior below: a nested process suppresses its own server
	// only while it has a live parent to forward to.
	parentLive := false
	if profile.needsDaemonFwd {
		stopSock := trace.phase("startup.daemon_socket_lookup")
		sock := os.Getenv("MAGUS_DAEMON_SOCKET")
		stableSock := false
		if sock == "" {
			if s, ok := proc.LookupStableSocket(rootCtx); ok {
				sock = s
				stableSock = true
				// Propagate to child processes spawned by this invocation.
				_ = os.Setenv("MAGUS_DAEMON_SOCKET", sock)
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
			// A live daemon declining a non-adoptable subcommand (only run/affected
			// adopt) is the normal path for every other verb: run it locally as a leaf
			// without alarming the user. Reserve warn for genuine forward failures of
			// an adoptable subcommand (transport error, dead daemon).
			if errors.Is(fwdErr, proc.ErrNotAdoptable) {
				slog.Debug("proc forward declined; running locally", slog.String("error", fwdErr.Error()))
			} else {
				slog.Warn("proc forward failed; running locally", slog.String("error", fwdErr.Error()))
			}
			// Tell apart a live parent that simply won't adopt this subcommand (only
			// run/affected adopt) from an unreachable one. When alive, keep
			// MAGUS_DAEMON_SOCKET pointed at it: this process runs the command locally
			// as a leaf, but deeper adoptable calls still forward to the single
			// top-level pool and probes (e.g. doctor's daemon check) see the real
			// daemon. On a transport failure the daemon is gone: clear the pointer so
			// nothing keeps dialing a corpse, and fall through to hosting our own pool.
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
	// Parse until first non-flag arg (the subcommand).
	if err := fs.Parse(args); err != nil && !errors.Is(err, flag.ErrHelp) {
		stopFlags()
		slog.Error("flag parse failed", slog.String("error", err.Error()))
		return startupResult{cleanup: cleanup}, 1
	}
	applyDisplay()
	rest := fs.Args()
	stopFlags()

	if len(rest) == 0 {
		usage()
		return startupResult{cleanup: cleanup}, 0
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
		startMultiWorkspaceDaemon(rootCtx, cfg, rc)
	case !profile.needsWorkspace:
		// skip loadMagus + proc server for subcommands that need no workspace
	default:
		concurrency := cfg.Concurrency
		if concurrency <= 0 {
			concurrency = cache.DefaultConcurrency()
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
	case "tail":
		return tailCmd(ctx, root, subArgs)
	case "affected":
		return affected(ctx, root, rc, subArgs)
	case "insight":
		return insightCmd(ctx, root, subArgs)
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
	case "status":
		return status(ctx, subArgs)
	case "clean":
		return cleanCmd(ctx, root, subArgs)
	case "merge-driver":
		return mergeDriverCmd(ctx, root, subArgs)
	case "doctor":
		return doctorCmd(ctx, root, subArgs)
	case "config":
		return configCmd(ctx, root, globalCfg, subArgs)
	case "repl":
		return replCmd(ctx, root, subArgs)
	case "server":
		return serverCmd(ctx, subArgs)
	case "mcp":
		return mcpCmd(ctx, subArgs)
	case "completion":
		return completion(subArgs)
	case "init":
		return initCmd(ctx, root, subArgs)
	case "agent":
		return agentCmd(ctx, subArgs)
	case "self":
		return selfCmd(ctx, root, subArgs)
	case "buzz":
		return buzzCmd(ctx, subArgs)
	default:
		fmt.Fprintf(os.Stderr, "magus: unknown subcommand %q\n", sub)
		if suggestion := interactive.SuggestNearest(sub, knownSubcommands); suggestion != "" {
			interactive.Emit(os.Stderr, fmt.Sprintf("did you mean %q?", suggestion))
		}
		fmt.Fprintln(os.Stderr, "")
		usage()
		return errSilent{exitCode: 2}
	}
}

var knownSubcommands = []string{
	"ls", "describe", "run", "x", "where", "tail",
	"affected", "insight", "query", "explain", "path", "refs", "graph", "watch", "status", "doctor",
	"config", "server", "repl", "completion", "init", "self", "version",
	"clean", "merge-driver", "buzz", "agent",
	"help",
}

func usage() {
	fmt.Fprintln(os.Stderr, "Usage: magus [flags] <subcommand> [args]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  ls             list all discovered projects")
	fmt.Fprintln(os.Stderr, "  describe       define a magus concept and list all entities (tools|targets|projects|workspaces|mcp-tools)")
	fmt.Fprintln(os.Stderr, "  run            run a target for selected projects")
	fmt.Fprintln(os.Stderr, "  x              interactive shorthand: pick project + target (TTY only)")
	fmt.Fprintln(os.Stderr, "  where          print the absolute path of a project (fuzzy match)")
	fmt.Fprintln(os.Stderr, "  tail           stream the most recent cached log for cwd project")
	fmt.Fprintln(os.Stderr, "  affected       run a target for VCS-diff affected projects")
	fmt.Fprintln(os.Stderr, "  query          search the knowledge graph and show a node's neighborhood")
	fmt.Fprintln(os.Stderr, "  explain        show one knowledge-graph node: its edges, provenance, blast radius")
	fmt.Fprintln(os.Stderr, "  path           show the shortest path between two knowledge-graph nodes")
	fmt.Fprintln(os.Stderr, "  refs           list where an ingested code symbol is defined and referenced")
	fmt.Fprintln(os.Stderr, "  graph          the graphs as objects: deps (project DAG), export (knowledge graph), stats (shape)")
	fmt.Fprintln(os.Stderr, "  insight        VCS-history analytics: hotspots, affinity, ownership, trend, volatility")
	fmt.Fprintln(os.Stderr, "  watch          emit changed file paths (pipe into affected --stdin)")
	fmt.Fprintln(os.Stderr, "  status         inspect the concurrency pool of a running parent magus")
	fmt.Fprintln(os.Stderr, "  clean          remove declared Outputs (regenerable build artifacts) [--cache to also drop entries]")
	fmt.Fprintln(os.Stderr, "  merge-driver   VCS merge driver for generated outputs (invoked by git/hg; wired via `config init`)")
	fmt.Fprintln(os.Stderr, "  doctor         validate the workspace")
	fmt.Fprintln(os.Stderr, "  config         view or update magus configuration")
	fmt.Fprintln(os.Stderr, "  server         manage the persistent daemon (start / stop / status; MCP starts with it)")
	fmt.Fprintln(os.Stderr, "  repl           open an interactive Buzz interpreter")
	fmt.Fprintln(os.Stderr, "  buzz           run a Buzz script (stdlib only; no host bindings)")
	fmt.Fprintln(os.Stderr, "  completion     print a shell completion script (bash, zsh, fish)")
	fmt.Fprintln(os.Stderr, "  init           bootstrap a workspace (magus.yaml + magusfile.buzz + merge driver)")
	fmt.Fprintln(os.Stderr, "  agent          install the knowledge-graph agent skill into a repo (agent install claude)")
	fmt.Fprintln(os.Stderr, "  self           manage the magus binary (self update / install)")
	fmt.Fprintln(os.Stderr, "  version        print version, commit, and build date")
	fmt.Fprintln(os.Stderr, "  help           show this message")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Global flags (work before or after the subcommand):")
	fmt.Fprintln(os.Stderr, "  --help, -h           show help (top-level or subcommand)")
	fmt.Fprintln(os.Stderr, "  --output, -o <fmt>   output format (text|json|yaml|name|template=<go-template>)")
	fmt.Fprintln(os.Stderr, "  -q, --quiet          suppress progress; only print errors + dump failing project output")
	fmt.Fprintln(os.Stderr, "  -s, --silent         like -q, but bound failing dumps (tail + log path) and bubble up only 'magus:notice:' lines")
	fmt.Fprintln(os.Stderr, "  -v, -vv, -vvv        increase log verbosity (-v/-vv: debug; -vvv: trace)")
	fmt.Fprintln(os.Stderr, "  --concurrency N      max parallel build steps (0 = config / MAGUS_CONCURRENCY / min(NumCPU,8))")
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

// dispatchAdopted routes adopted child args; only "run" and "affected" are accepted.
func dispatchAdopted(ctx context.Context, root string, rc runConfig, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no subcommand in forwarded args")
	}
	// Strip global flags; display flags are ignored (parent's settings are authoritative).
	var (
		ignoredRoot   string
		ignoredCfg    string
		ignoredOutput string
		ignoredConc   int
		ignoredV      verbosity
		ignoredQ      bool
	)
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

// daemonProvider is the single observability provider the daemon shares between its
// per-workspace registry and its bridge Magus. startMultiWorkspaceDaemon builds it once
// (it runs before the `server start` command handler); serverStart then hands it to
// startMCPWithDaemon. Same process, sequential, so the write happens-before the read.
var daemonProvider observability.Provider

// daemonRuns is the daemon's live-run registry: a capture handler folded into every adopted
// dispatch's journal, tracking per-target execution state. startMultiWorkspaceDaemon builds
// it and threads it onto each dispatch's context; startMCPWithDaemon hands its Snapshot to
// the console service so /api/v1/status and the status SSE report active runs. Same process,
// sequential, so the write happens-before the reads.
var daemonRuns *console.RunRegistry

// daemonServices is the daemon's shared-service registry. startMultiWorkspaceDaemon builds
// it (as svcReg) and points this global at it so startMCPWithDaemon can hand its Snapshot to
// the console service, surfacing hosted services on /api/v1/status alongside the pool and
// runs. Same process, sequential, so the write happens-before the reads.
var daemonServices *service.Registry

// daemonTrailBase is the ONE daemon-wide activity-trail location: the bridge Magus's cache dir,
// the same base the MCP handler writes to and the ActivityService reads from. startMCPWithDaemon
// sets it after loading the bridge Magus; the proc OnJobDone callback reads it so every producer
// (MCP calls, background jobs) appends to a single trail, disambiguated by Event.Workspace rather
// than fragmented across per-workspace directories. Empty until the bridge starts (and stays
// empty when MCP is disabled, when there is no console to read the trail anyway), so a job that
// completes in that window is dropped best-effort.
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
	go func() {
		<-ctx.Done()
		// Drain in-flight handlers (srv.Close waits on connWg) before reg.close so a
		// workspace can't be closed under an in-flight build.
		srv.Close()
		svcReg.Shutdown() // stop every hosted service on daemon teardown
		reg.close()
	}()
}

func extractRootFlag(args []string) string {
	for i, a := range args {
		switch {
		case a == "-root" || a == "--root":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(a, "-root="):
			return strings.TrimPrefix(a, "-root=")
		case strings.HasPrefix(a, "--root="):
			return strings.TrimPrefix(a, "--root=")
		}
	}
	return ""
}

func extractQuietFlag(args []string) bool {
	for _, a := range args {
		if a == "-q" || a == "--quiet" || a == "-quiet" {
			return true
		}
	}
	return false
}

func extractVerbosityCount(args []string) int {
	n := 0
	for _, a := range expandVerbosityArgs(args) {
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
	// os.exit(code) from a magusfile: honor the requested code without an extra
	// generic error line; the magusfile already logged whatever it wanted to.
	var exitErr types.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	slog.Error(err.Error())
	return 1
}
