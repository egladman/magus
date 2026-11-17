package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/selfupdate"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/types"
)

// magus shell is the guard with one door.
//
// The rules it applies are the workspace's own conventions, and nothing about them is
// agent-shaped: `grep -r` has a better answer here whoever typed it, and so does a raw
// `go build`. They reached an agent host first only because a host had a hook to put them
// in, which is an accident of wiring rather than what they are for. A new contributor
// learns a workspace's tooling the slow way, by reading a CONTRIBUTING file nobody
// maintains or by being corrected in review a week later; this answers at the prompt, in
// the moment the habit is forming.
//
// A person and a host therefore run the SAME command, not two that agree. The only
// difference is how the input arrives: an operand for someone typing, stdin for a wrapper
// that already has the command in a variable. Verdict, flags, output formats and exit
// codes are one implementation, so there is no second code path to keep honest.
//
// It is deliberately not a sandbox and not a supervisor. Nothing is executed and nothing
// is prevented; the exit code is what a host chooses to block on.

// shellUsage describes the guard's commands.
func shellUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus shell '<command>' [flags]   # judge one command")
	fmt.Fprintln(w, "       magus shell [flags]               # the command or path arrives on stdin")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Check a shell command against this workspace's conventions before running")
	fmt.Fprintln(w, "it, and print what to run instead when there is a better answer. The rules")
	fmt.Fprintln(w, "are the workspace's, not an agent's: a raw `go build` misses the cache and")
	fmt.Fprintln(w, "a `grep -r` misses what the symbol index already knows, whoever typed it.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Nothing is executed and nothing is prevented. This reports; you decide.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "A person passes the command as one quoted argument; a host pipes it on")
	fmt.Fprintln(w, "stdin, as plain text or as the JSON envelope it already writes. Same")
	fmt.Fprintln(w, "command, same verdict, same exit codes. A program that is not a host")
	fmt.Fprintln(w, "pipes a step record ({\"api_version\": \"magus.step/v1alpha1\", \"kind\": ...});")
	fmt.Fprintln(w, "a step of kind tool is judged as the envelope it stands for.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintln(w, "  magus shell 'go test ./...'")
	fmt.Fprintln(w, "  magus shell 'grep -rn HandleFoo internal/'")
	fmt.Fprintln(w, "  magus shell --path MAGUS.md")
	// Fprintf with %% : vet rejects a Printf directive inside an Fprintln, and the
	// example is worth more than the convenience. hookUsage did the same.
	fmt.Fprintf(w, "  printf '%%s' 'go build ./...' | magus shell\n")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Flags:")
	fmt.Fprintln(w, "  --path                judge the input as a file path an edit is about to")
	fmt.Fprintln(w, "                        write, not as a shell command")
	fmt.Fprintln(w, "  --observe             record the path as one the agent REACHED and judge")
	fmt.Fprintln(w, "                        nothing; wire it to the tools that only look")
	fmt.Fprintln(w, "  --lease <id>          the job row this call acts as; the lease-scoped")
	fmt.Fprintln(w, "                        rules are graded against it, and the verdict names")
	fmt.Fprintln(w, "                        it. Defaults to magus.lease in $BAGGAGE; an id this")
	fmt.Fprintln(w, "                        workspace's job store does not declare is an error")
	fmt.Fprintln(w, "  --agent-name <name>   agent host this invocation came from (attribution")
	fmt.Fprintln(w, "                        only; the verdict never reads it)")
	fmt.Fprintln(w, "  --session <id>        the host's own session id, recorded on the event")
	fmt.Fprintln(w, "  --transcript <path>   the host's own log of this session, recorded as a")
	fmt.Fprintln(w, "                        pointer; magus never opens it")
	fmt.Fprintln(w, "  --event <name>        the host's hook event name (e.g. PreToolUse)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Exit codes: 2 is a deny, everything else is allowed. An advisory exits 0")
	fmt.Fprintln(w, "on purpose. Unreadable input is 2, so a host that blocks on 2 fails closed")
	fmt.Fprintln(w, "when bytes were lost on the way in.")
	fmt.Fprintln(w, "")
	fmt.Fprintf(w, "A verdict names the rule that produced it, in brackets. `%s`\n", hint.DescribeRules.String())
	fmt.Fprintf(w, "lists every rule this workspace enforces; `%s` details one.\n", hint.DescribeRule.With("<name>"))
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Global display flags (-o, -s, -q, -v, --tee) are accepted; see `magus -h`.")
}

// shellCommand implements `magus shell`: evaluate one shell command or file path and emit a
// verdict. The caller owns extraction from its own event shape; magus owns only the
// host-neutral policy.
//
// An EMPTY input fails OPEN: a wrapper that hands this nothing must not have every tool
// call blocked. An input that fails to READ is the opposite case and fails CLOSED,
// because bytes were on their way and were lost, so the guard has judged nothing and the
// command it never saw must not be reported as cleared.
func shellCommand(ctx context.Context, args []string) error {
	return shellCommandWithErrorWriter(ctx, os.Stdin, os.Stdout, os.Stderr, args)
}

// shellCommandWithErrorWriter is shellCommand's transport seam. The CLI reports a deny reason on
// stderr for machine-readable output, while a host adapter has already carried that
// reason in its host reply and must not leak a second, non-protocol message into the
// host's hook stream.
func shellCommandWithErrorWriter(ctx context.Context, in io.Reader, out, errOut io.Writer, args []string) error {
	fset := flag.NewFlagSet("shell", flag.ContinueOnError) //nolint:hostvocab // TestEveryLeafRendersEveryFormatOrRefusesThem requires the leaf name shell
	// --observe is observation, not policy. A wrapper sets it for a tool that only
	// LOOKS: no rule judges a read, so running the write rules over one would only
	// ever manufacture a false advisory about editing a file the agent opened
	// read-only. Which of a host's tools merely look is the wrapper's knowledge,
	// never magus's: see the tool-label constants and the hostagnostic linter.
	//
	// The attribution flags name WHO produced the observation, and the guard's
	// verdict never reads what they say. The host name is an opaque label rather
	// than a set magus knows, because a magus that enumerated hosts would need a
	// release per host. A wrapper that cannot extract a session id must still get a
	// verdict. The one requirement is that installed glue (--transport) names its
	// host at all; guard.Judge refuses it otherwise (MGS3024).
	sf := flaggen.BindShell(fset)
	// --lease has no environment default: the guard reads BAGGAGE itself and ranks it below
	// the spawn record and the marker, and a default here would pass that claim in as a
	// flag that outranks both.
	// The whole display set, not a hand-rolled -o: this command used to define
	// its own output flag and so silently lacked -s, -q, -v and --tee. That gap
	// is the reason for the rule: a flag accepted on most commands teaches
	// callers it is unreliable everywhere.
	bindDisplayFlags(fset)
	fset.Usage = func() { shellUsage(fset.Output()) }
	if err := command.Parse(fset, reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	// --transport is how installed glue says a host called; a person typing this names none.
	if sf.Transport != "" {
		forceQuietDisplay()
	}
	opts, err := output.Parse(global.output)
	if err != nil {
		return err
	}
	call := guard.HookCall{
		Flags:    *sf,
		Output:   opts,
		Operands: fset.Args(),
		Quiet:    global.quiet || global.silent,
		Window:   terminalWindow(),
	}
	return hookEnv().RunHook(ctx, call, in, out, errOut)
}

// terminalWindow names the terminal window this process writes to, or "" when stdout is
// not a terminal. It keys fire-once notices for a caller no host gave a session, and is
// never recorded as one.
func terminalWindow() string {
	return hint.WindowFromTerminal(os.Getenv, os.Getppid(),
		termkit.IsTerminalWriter(os.Stdout, termkit.SystemProbe))
}

// guardCacheDir resolves a workspace's cache directory for the guard: root's own, read from
// its magus.yaml, or for "" the workspace found from rootOverride, whose config is loaded.
func guardCacheDir(rootOverride string) func(root string) (string, error) {
	return func(root string) (string, error) {
		if root != "" {
			return magus.ResolveCacheDir(root, cliOptions()...)
		}
		found, err := workspace.FindRoot(rootOverride)
		if err != nil {
			return "", err
		}
		return magus.ResolveCacheDir(found, workspace.WithLoadedConfig(globalCfg))
	}
}

// guardRoot finds the workspace whose rules the guard enforces, searching from override,
// or from the cwd when it is "". A variable so the hook's own tests judge by the built-in
// rules alone rather than by the policy of whichever checkout they happen to run in.
var guardRoot = workspace.FindRoot

// hookBinaryPath is selfupdate.RunningBinary, a variable so tests can place the
// running binary in a checkout.
var hookBinaryPath = selfupdate.RunningBinary

// hookEnv binds the hook's guard to this binary: its version, its workspace openers and
// its output arms, read from the process globals at the call.
func hookEnv() guard.HookEnv {
	return guard.HookEnv{
		Version:        version,
		BinaryPath:     selfupdate.RunningBinary,
		HookBinaryPath: hookBinaryPath,
		Config:         globalCfg,
		FindRoot:       guardRoot,
		Inspect: func(ctx context.Context, rootOverride, root string) (types.WorkspaceRepository, error) {
			if root == "" {
				return inspectWorkspace(ctx, rootOverride)
			}
			return magus.Inspect(ctx, root,
				append(cliOptions(), workspace.WithLoadedConfig(globalCfg), magus.WithVersion(version))...)
		},
		CacheDir: guardCacheDir,
		LoadedCacheDir: func(root string) (string, error) {
			return magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
		},
		LazyWorkspace: func(root string, open func(context.Context) (types.WorkspaceRepository, error)) types.Inspector {
			return magus.NewLazyWorkspace(root, func(ctx context.Context) (*magus.Magus, error) {
				ws, err := open(ctx)
				if err != nil {
					return nil, err
				}
				return asMagus(ws)
			})
		},
		TierRule: magus.TierCeiling,
		Trace: func(ctx context.Context, phase string) func() {
			return observability.TraceFromContext(ctx).Phase(phase)
		},
		OpenJobs: func(ctx context.Context, root string) (*job.Store, error) {
			return openJobs(ctx, root, jobOpen{DryRun: true})
		},
		StaleIndexes: staleIndexes,
		IndexCause:   indexCause,
		Emit:         hookEmit,
	}
}

// guardDependencies is the hook's dependencies for the workspace found from rootOverride.
func guardDependencies(ctx context.Context, rootOverride string) guard.Dependencies {
	return hookEnv().Dependencies(ctx, rootOverride)
}

// loadGuardDependencies is guardDependencies that also returns the rules it loaded.
func loadGuardDependencies(ctx context.Context, rootOverride string) (guard.Dependencies, *guard.WorkspaceRules) {
	return hookEnv().LoadDependencies(ctx, rootOverride)
}

// loadGuardRules loads the guard rules of the workspace found from rootOverride.
func loadGuardRules(ctx context.Context, rootOverride string) (*guard.WorkspaceRules, error) {
	return hookEnv().LoadRules(ctx, rootOverride)
}

// loadWorkspaceShellRules loads the shell rules of the workspace found from rootOverride.
func loadWorkspaceShellRules(ctx context.Context, rootOverride string) ([]guard.WorkspaceShellRule, guard.Dialect) {
	return hookEnv().LoadWorkspaceShellRules(ctx, rootOverride)
}

// unpinnedBinary says why the session's job store does not pin own.
func unpinnedBinary(ctx context.Context, own, session string) string {
	return hookEnv().UnpinnedBinary(ctx, own, session)
}

// hookEmit renders a verdict in opts' structured format, or its decision under -o name.
func hookEmit(out io.Writer, opts output.Options, decision string, verdict any) error {
	if opts.Format == outputName {
		return emitNames(out, []string{decision})
	}
	return writeFormatted(out, opts, verdict)
}

// refuseUnjudgedHook answers `magus shell` or a glue, `magus buzz -s` with an event piped
// in, whose magus cannot start for root, since a host runs a failed hook's call.
func refuseUnjudgedHook(ctx context.Context, root, sub string, subArgs []string, cause error) (int, bool) {
	if sub == "shell" {
		return mapExitCode(shellCommand(guard.WithStaleConfig(ctx, cause), subArgs)), true
	}
	own, _, _ := strings.Cut(strings.Join(subArgs, "\x00"), "\x00--\x00")
	if sub != "buzz" || termkit.StdinIsTerminal() || !slices.Contains(strings.Split(own, "\x00"), "-s") {
		return 0, false
	}
	answer, reason := guard.UnjudgedHook(root, os.Stdin, cause)
	if reason != "" {
		fmt.Fprintln(os.Stderr, reason)
	}
	return answer.ExitCode(), true
}
