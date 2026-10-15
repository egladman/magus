package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/project"
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
	fmt.Fprintln(w, "command, same verdict, same exit codes.")
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
	fmt.Fprintln(w, "A verdict names the rule that produced it, in brackets. `"+hint.DescribeRules.String()+"`")
	fmt.Fprintln(w, "lists every rule this workspace enforces; `"+hint.DescribeRule.With("<name>")+"` details one.")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Global display flags (-o, -s, -q, -v, --tee) are accepted; see `magus -h`.")
}

// shellCmd implements `magus shell`: evaluate one shell command or file path and emit a
// verdict. The caller owns extraction from its own event shape; magus owns only the
// host-neutral policy.
//
// An EMPTY input fails OPEN: a wrapper that hands this nothing must not have every tool
// call blocked. An input that fails to READ is the opposite case and fails CLOSED,
// because bytes were on their way and were lost, so the guard has judged nothing and the
// command it never saw must not be reported as cleared.
func shellCmd(ctx context.Context, args []string) error {
	return shellCmdWithErrorWriter(ctx, os.Stdin, os.Stdout, os.Stderr, args)
}

type envRefusalKey struct{}

// withEnvRefusal hands shellCmd the MGS1046 error startup found, for it to answer as the
// verdict instead of judging the input.
func withEnvRefusal(ctx context.Context, err error) context.Context {
	return context.WithValue(ctx, envRefusalKey{}, err)
}

// shellCmdWithErrorWriter is shellCmd's transport seam. The CLI reports a deny reason on
// stderr for machine-readable output, while a host adapter has already carried that
// reason in its host reply and must not leak a second, non-protocol message into the
// host's hook stream.
func shellCmdWithErrorWriter(ctx context.Context, in io.Reader, out, errOut io.Writer, args []string) error {
	// Started here rather than in Judge, so loading the rules spends the same budget.
	ctx, cancel := guard.WithHookDeadline(ctx)
	defer cancel()
	fset := flag.NewFlagSet("magus shell", flag.ContinueOnError)
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
	sf := gen.BindShell(fset)
	// --lease has no environment default: the guard reads BAGGAGE itself and ranks it below
	// the spawn record and the marker, and a default here would pass that claim in as a
	// flag that outranks both.
	// The whole display set, not a hand-rolled -o: this command used to define
	// its own output flag and so silently lacked -s, -q, -v and --tee. That gap
	// is the reason for the rule: a flag accepted on most commands teaches
	// callers it is unreliable everywhere.
	bindDisplayFlags(fset)
	fset.Usage = func() { shellUsage(fset.Output()) }
	if err := fset.Parse(reorderFlagsFirst(fset, args)); err != nil {
		return err
	}
	// --transport is how installed glue says a host called; a person typing this names none.
	if sf.Transport != "" {
		forceQuietDisplay()
	}
	opts, err := ResolveOutput(global.output)
	if err != nil {
		return err
	}

	// Answered as a deny, never as the exit a startup refusal would be: a hook reads a
	// failed guard as no verdict and fails open, so an environment magus knows is wrong
	// would disarm every rule for the session. --observe and --message carry no verdict to
	// deny.
	if refusal, _ := ctx.Value(envRefusalKey{}).(error); refusal != nil && !sf.Observe && !sf.Message {
		verdict := types.GuardJudgment{
			SchemaVersion: agent.GuardSchemaVersion,
			Decision:      "deny",
			Reason: refusal.Error() + "\n" +
				"Nothing was judged, so this call is blocked rather than cleared. Fix the environment the agent host runs in.",
		}
		if err := writeGuardVerdict(out, opts, verdict); err != nil {
			return err
		}
		return enforceVerdictTo(errOut, opts, verdict)
	}

	input, readErr := shellInput(in, fset.Args())
	if err, ok := readErr.(errUsage); ok { //nolint:errorlint // a usage error is this function's own, never wrapped
		return err
	}
	// A failed read is not an empty input, and collapsing the two cleared every
	// command whose payload arrived truncated. Answered as a deny so the exit is 2,
	// which is what the manpage promises: deny and unreadable input share the code,
	// so a host that blocks on 2 fails closed in both cases. --observe and --message are
	// exempt because they carry no verdict: there is nothing to fail closed about, and
	// the documented contract is that they always exit 0.
	if readErr != nil && !sf.Observe && !sf.Message {
		verdict := types.GuardJudgment{
			SchemaVersion: agent.GuardSchemaVersion,
			Decision:      "deny",
			// "from stdin" is accurate whichever way this command is called: an operand
			// is read off argv and cannot fail, so a read error is always the piped path.
			Reason: "magus shell could not read its input from stdin: " + readErr.Error() + "\n" +
				"Nothing was judged, so this call is blocked rather than cleared. Retry it.",
		}
		if err := writeGuardVerdict(out, opts, verdict); err != nil {
			return err
		}
		return enforceVerdictTo(errOut, opts, verdict)
	}
	// A hook's stdout is the host's pipe; a terminal on it means someone typed this.
	window := terminalWindow()
	entry := types.EntryPointHook
	if window != "" {
		entry = types.EntryPointCLI
	}
	req := guard.Request{
		Input:      input,
		IsPath:     sf.Path,
		Observe:    sf.Observe,
		Message:    sf.Message,
		Lease:      sf.Lease,
		Host:       sf.AgentName,
		Form:       sf.Transport,
		Session:    strings.TrimSpace(sf.Session),
		Agent:      strings.TrimSpace(sf.Agent),
		Window:     window,
		Transcript: sf.Transcript,
		Event:      sf.Event,
		// Declared by the wiring, because only a host that reports skill loads can
		// honestly say it does. See guard.denySpawnWithoutBrief.
		ReportsSkills: sf.ReportsSkills,
		// Declared by the wiring for the same reason: only a glue that renders the host's
		// approval prompt can say so, and one that predates ask renders it as an allow.
		RendersAsk: sf.RendersAsk,
		// The call's own word on how the host runs it, and the host's foreground limits
		// as the wiring declares them; a flag left out leaves the rules that read it silent.
		CallBackground:        sf.CallBackground,
		CallTimeout:           time.Duration(sf.CallTimeoutMs) * time.Millisecond,
		HostForegroundDefault: time.Duration(sf.ForegroundDefaultMs) * time.Millisecond,
		HostForegroundMax:     time.Duration(sf.ForegroundMaxMs) * time.Millisecond,
	}
	// Marked before the rules load, so a hook its host kills while loading them is still
	// reported to the session's next judgment.
	ctx = guard.BeginJudgment(ctx, guardCacheDir(""), req)
	ctx = trail.ContextWithEntryPoint(ctx, entry)
	// The spell catalog is compiled in, so the raw-tool rule judges a lapsed call too.
	base := guard.Dependencies{CacheDir: guardCacheDir(""), BinaryVersion: version, Spells: project.DefaultSpellRegistry().All}
	base.Binary, _ = runningBinaryPath()
	// The rules load inside the bound too: a magusfile's imports parse to the end however
	// long they take.
	verdict := guard.JudgeWithin(ctx, base, req, func(ctx context.Context) types.GuardJudgment {
		// The hook ignores --root and loads no workspace up front: the one it judges in is
		// wherever the host runs it.
		deps := guardDependencies(ctx, "")
		stopJudge := traceFromContext(ctx).phase("guard.judge")
		defer stopJudge()
		return guard.Judge(ctx, deps, req)
	})
	// -q and -s mean the exit code IS the answer, which this command can honor exactly
	// because its whole output is one verdict. They bound a run's progress chatter
	// everywhere else; here there is no progress, so suppressing the verdict is the only
	// reading that leaves them meaning anything at all. A script asking "would this be
	// refused" wants the status and nothing on its stdout.
	if global.quiet || global.silent {
		return enforceVerdictTo(io.Discard, opts, verdict)
	}
	if err := writeGuardVerdict(out, opts, verdict); err != nil {
		return err
	}
	return enforceVerdictTo(errOut, opts, verdict)
}

// terminalWindow names the terminal window this process writes to, or "" when stdout is
// not a terminal. It keys fire-once notices for a caller no host gave a session, and is
// never recorded as one.
func terminalWindow() string {
	return hint.WindowFromTerminal(os.Getenv, os.Getppid(),
		tty.IsTerminalWriter(os.Stdout, tty.SystemProbe))
}

// shellInput resolves the command from the operand a person typed, or from stdin when
// they typed none.
//
// The operand wins, and stdin is not consulted when one is present: a person running this
// from a terminal has stdin attached to their keyboard, and reading it would hang on a
// command that was already supplied. Several operands means the quotes were left off,
// which is worth saying rather than judging the first word alone, because judging `go`
// and clearing it would report a pass for a command nobody ran.
func shellInput(in io.Reader, operands []string) (string, error) {
	switch len(operands) {
	case 0:
		return readGuardInput(in)
	case 1:
		return operands[0], nil
	default:
		return "", usagef("magus shell: quote the whole command as one argument: magus shell '%s'",
			strings.Join(operands, " "))
	}
}

// guardDependencies hands the guard the workspace facts its rules cannot resolve for
// themselves: what guard.NewDependencies builds from the CLI's workspace open, cache dir
// and loaded config, plus the graph and symbol index lookups and the model facts, which
// only the CLI holds.
//
// rootOverride is the --root the command loaded its workspace with, "" for the cwd. A rule
// asking for "the workspace" gets that one: resolving the cwd instead names a second
// workspace whenever the command ran with --root from inside another checkout.
func guardDependencies(ctx context.Context, rootOverride string) guard.Dependencies {
	deps, _ := loadGuardDependencies(ctx, rootOverride)
	return deps
}

// loadGuardDependencies is guardDependencies that also returns the rules it loaded, nil
// when none loaded, for a caller that must know which files they came from.
func loadGuardDependencies(ctx context.Context, rootOverride string) (guard.Dependencies, *guard.WorkspaceRules) {
	root, err := guardRoot(rootOverride)
	if err != nil {
		root = ""
	}
	inspect := func(ctx context.Context, root string) (types.WorkspaceRepository, error) {
		if root == "" {
			return inspectWorkspace(ctx, rootOverride)
		}
		return magus.Inspect(ctx, root,
			magus.WithLoadedConfig(globalCfg), magus.WithVersion(version))
	}
	// An unresolvable path leaves the trail row without one; the version still records.
	binary, _ := runningBinaryPath()
	deps, rules := guard.NewDependencies(ctx, guard.Wiring{
		Root:            root,
		Config:          globalCfg,
		BinaryVersion:   version,
		Binary:          binary,
		Inspect:         inspect,
		HumanOnlyRecord: guardHumanOnlyRecord(rootOverride, inspect),
		CacheDir:        guardCacheDir(rootOverride),
		Trace: func(ctx context.Context, phase string) func() {
			return traceFromContext(ctx).phase(phase)
		},
	})
	index := newGuardLookups(rootOverride)
	deps.GraphStaleAdvice = index.staleGraphAdvice
	deps.SymbolDefined = index.symbolDefined
	deps.IndexCause = index.indexCause
	deps.SymbolSites = index.symbolSites
	deps.GraphIDs = index.graphIDs
	deps.IndexedIDs = index.indexedIDs
	deps.ModelRows = guardModelRows(ctx, rootOverride)
	deps.ModelTiers = guardModelTiers(ctx, rootOverride)
	return deps, rules
}

// guardHumanOnlyRecord answers what the workspace at root, "" for the one found from
// rootOverride, declares human_only from the knowledge store's record of it (see
// magus.RecordedHumanOnly). Each call holds a lazy workspace of its own, whose one walk of
// the tree is taken now: a daemon grading checks keeps these dependencies for hours, and
// a walk kept from an earlier call would vouch for a record the tree has since moved past.
// The record is read without opening the workspace; open is there only because a lazy
// workspace must say how it would be opened.
func guardHumanOnlyRecord(rootOverride string, inspect func(context.Context, string) (types.WorkspaceRepository, error)) func(context.Context, string) (types.HumanOnlyIndex, bool) {
	return func(ctx context.Context, root string) (types.HumanOnlyIndex, bool) {
		ask := root
		if root == "" {
			found, err := magus.FindRoot(rootOverride)
			if err != nil {
				return types.HumanOnlyIndex{}, false
			}
			root = found
		}
		lw := magus.NewLazyWorkspace(root, func(ctx context.Context) (*magus.Magus, error) {
			ws, err := inspect(ctx, ask)
			if err != nil {
				return nil, err
			}
			return asMagus(ws)
		})
		return magus.RecordedHumanOnly(ctx, lw, globalCfg)
	}
}

// onceInspected is the lazy reader of what read takes from the workspace inspect opens. The
// first call opens it and the answer is kept for the rest, so a judgment that never asks
// pays for nothing. A workspace that does not open yields T's zero value.
func onceInspected[T any](ctx context.Context, inspect func(context.Context) (types.WorkspaceRepository, error), read func(context.Context, types.WorkspaceRepository) T) func() T {
	return sync.OnceValue(func() T {
		ws, err := inspect(ctx)
		if err != nil {
			var zero T
			return zero
		}
		return read(ctx, ws)
	})
}

// harnessModelRows is the lazy reader of the model rows the harnesses wired in the
// workspace at root publish through harness_models; the first call loads each harness
// spell. A workspace that does not load, wires no harness or wires none that publishes rows
// yields no rows, which the guard reads as no facts about a model rather than an error.
func harnessModelRows(ctx context.Context, root string, inspect func(context.Context) (types.WorkspaceRepository, error)) func() []types.ModelPricing {
	return onceInspected(ctx, inspect, func(ctx context.Context, ws types.WorkspaceRepository) []types.ModelPricing {
		wired := workspaceHarnessNames(ws)
		ctx = agent.ContextWithWiredHarnesses(types.WithWorkspace(ctx, ws), wired)
		var rows []types.ModelPricing
		for _, id := range wired {
			if d, _, err := agent.LoadHarness(ctx, root, id); err == nil {
				rows = append(rows, d.Models...)
			}
		}
		return rows
	})
}

// guardModelRows is harnessModelRows for the workspace found from rootOverride, opened as
// the guard's other rules open it, so one load serves them all. nil outside a workspace.
func guardModelRows(ctx context.Context, rootOverride string) func() []types.ModelPricing {
	root, err := magus.FindRoot(rootOverride)
	if err != nil {
		return nil
	}
	return harnessModelRows(ctx, root, func(ctx context.Context) (types.WorkspaceRepository, error) {
		return inspectWorkspace(ctx, rootOverride)
	})
}

// guardModelTiers is the lazy reader of the model-tier declaration of the workspace found
// from rootOverride, nil outside a workspace. It opens the workspace whole, as guardModelRows
// does, and shares that one load: the guard's own rule load keeps only the magus\guard
// registrations and what they reach, so it holds neither a magus\job.modelTiers call nor
// the harness spells whose aliases map each tier to a model.
func guardModelTiers(ctx context.Context, rootOverride string) func() *types.ModelTierSet {
	if _, err := magus.FindRoot(rootOverride); err != nil {
		return nil
	}
	return workspaceModelTiers(ctx, func(ctx context.Context) (types.WorkspaceRepository, error) {
		return inspectWorkspace(ctx, rootOverride)
	})
}

// workspaceModelTiers is the lazy reader of the model tiers the workspace inspect opens
// declares. A workspace that does not load or declares no tiers yields nil.
func workspaceModelTiers(ctx context.Context, inspect func(context.Context) (types.WorkspaceRepository, error)) func() *types.ModelTierSet {
	return onceInspected(ctx, inspect, func(ctx context.Context, ws types.WorkspaceRepository) *types.ModelTierSet {
		tiered, ok := ws.(interface {
			ModelTiers(ctx context.Context) *types.ModelTierSet
		})
		if !ok {
			return nil
		}
		return tiered.ModelTiers(ctx)
	})
}

// guardCacheDir resolves a workspace's cache directory for the guard: root's own, read from
// its magus.yaml, or for "" the workspace found from rootOverride, whose config is loaded.
func guardCacheDir(rootOverride string) func(root string) (string, error) {
	return func(root string) (string, error) {
		if root != "" {
			return magus.ResolveCacheDir(root)
		}
		found, err := magus.FindRoot(rootOverride)
		if err != nil {
			return "", err
		}
		return magus.ResolveCacheDir(found, magus.WithLoadedConfig(globalCfg))
	}
}

// guardRoot finds the workspace whose rules the guard enforces, searching from override,
// or from the cwd when it is "". A variable so the hook's own tests judge by the built-in
// rules alone rather than by the policy of whichever checkout they happen to run in.
var guardRoot = magus.FindRoot

// loadGuardRules loads the guard rules of the workspace found from rootOverride (the cwd's
// when it is "") from its root magusfile alone, nil with the reason when that file does not load, and nil with no
// error outside any workspace. Unloadable is not fatal for the reason
// loadWorkspaceShellRules gives: a magusfile typo must not take down every hook.
//
// Not the full workspace load: that opens every project and costs an order of magnitude
// more, on every agent tool call, for facts no guard rule reads. The rules that do need
// the workspace open it through Dependencies.Inspect, and only when they apply.
func loadGuardRules(ctx context.Context, rootOverride string) (*guard.WorkspaceRules, error) {
	root, err := guardRoot(rootOverride)
	if err != nil {
		return nil, nil //nolint:nilnil,nilerr // outside a workspace there are no rules and nothing failed
	}
	return guard.LoadWorkspaceRules(ctx, root, types.VCSOptions{
		Enabled: globalCfg.VCS.Enabled, Name: globalCfg.VCS.Name, BaseRef: globalCfg.VCS.BaseRef,
	})
}

// guardLookupBudget bounds every graph-backed guard answer. The hook runs before every
// agent command under a host timeout that fails open, so an answer that arrives late is
// worth less than none: past the budget the lookup is non-definitive and the rule stays
// silent.
const guardLookupBudget = 150 * time.Millisecond

// withinBudget runs lookup and gives up on it after budget. The lookup cannot be
// cancelled (it stats the tree), so it is left to finish on its own goroutine, which the
// hook process's exit reclaims.
func withinBudget[T any](budget time.Duration, lookup func() T) (T, bool) {
	done := make(chan T, 1)
	go func() { done <- lookup() }()
	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case v := <-done:
		return v, true
	case <-timer.C:
		var zero T
		return zero, false
	}
}

// guardLookups answers the guard's questions about one workspace's graph index: the
// workspace found from rootOverride, the --root a command loaded as given, or from the cwd
// when it is "".
type guardLookups struct {
	rootOverride string
	// index is the index `magus graph build` wrote for the workspace, read on the first
	// question, or nil when there is none. The graph itself is never loaded here: that
	// costs seconds, and the index answers the guard's questions from one file.
	index func() *knowledge.GuardIndex
}

func newGuardLookups(rootOverride string) guardLookups {
	return guardLookups{rootOverride: rootOverride, index: sync.OnceValue(func() *knowledge.GuardIndex {
		root, err := guardRoot(rootOverride)
		if err != nil {
			return nil
		}
		cacheDir, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
		if err != nil {
			return nil
		}
		idx, err := knowledge.ReadGuardIndex(cacheDir, root)
		if err != nil {
			return nil
		}
		return idx
	})}
}

// symbolDefined answers the one question that lets the guard deny a symbol search: does
// refs return the same sites this grep is reaching for. Definitive only when the index's
// symbols were fresh when it was written and no source has moved since; a deny resting on
// anything less would take grep away on a lookup that may be wrong.
func (l guardLookups) symbolDefined(ident string) (defined, definitive bool) {
	type answer struct{ defined, definitive bool }
	a, ok := withinBudget(guardLookupBudget, func() answer {
		idx := l.index()
		if idx == nil {
			return answer{}
		}
		return answer{idx.Has(knowledge.GuardSymbol, ident), idx.Fresh(knowledge.GuardSymbol)}
	})
	return a.defined, ok && a.definitive
}

// symbolSites reads ident's sites from the refs file beside the guard index, on the same
// freshness terms as symbolDefined.
func (l guardLookups) symbolSites(ident string) ([]types.KnowledgeRefSite, bool) {
	type answer struct {
		sites      []types.KnowledgeRefSite
		definitive bool
	}
	a, ok := withinBudget(guardLookupBudget, func() answer {
		idx := l.index()
		if idx == nil {
			return answer{}
		}
		sites, err := idx.RefSites(ident)
		if err != nil {
			return answer{}
		}
		return answer{sites, idx.Fresh(knowledge.GuardSymbol)}
	})
	return a.sites, ok && a.definitive
}

// graphIDs lists kind's ids from the guard index, definitive only while the sources they
// came from are unchanged.
func (l guardLookups) graphIDs(_ context.Context, kind string) ([]string, bool) {
	type answer struct {
		ids   []string
		fresh bool
	}
	a, ok := withinBudget(guardLookupBudget, func() answer {
		idx := l.index()
		if idx == nil {
			return answer{}
		}
		return answer{idx.IDs(kind), idx.Fresh(kind)}
	})
	if !ok || !a.fresh {
		return nil, false
	}
	return a.ids, true
}

// indexedIDs lists kind's ids from the guard index whether or not its sources have moved
// since; the rules that call it prove their answer against the disk.
func (l guardLookups) indexedIDs(_ context.Context, kind string) ([]string, bool) {
	type answer struct {
		ids   []string
		found bool
	}
	a, ok := withinBudget(guardLookupBudget, func() answer {
		idx := l.index()
		if idx == nil {
			return answer{}
		}
		return answer{idx.IDs(kind), true}
	})
	return a.ids, ok && a.found
}

// loadWorkspaceShellRules returns additive rules the root magusfile of the workspace found
// from rootOverride declared via magus\guard.shell, and the last non-empty dialect among
// them for outer parse. Missing or unloadable is empty so a magusfile typo cannot take
// down every shell hook (built-ins still apply).
func loadWorkspaceShellRules(ctx context.Context, rootOverride string) ([]guard.WorkspaceShellRule, guard.Dialect) {
	rules, _ := loadGuardRules(ctx, rootOverride)
	if rules == nil {
		return nil, ""
	}
	return rules.ShellRules()
}

// guardDenyExitCode is what a denied command exits with.
//
// A hook that reports a deny and exits 0 blocks NOTHING: the host sees success
// and runs the command anyway, so the guard looks enforced and is not. 2 rather
// than 1 is what the dominant host reads as "block and show the reason to the
// model". The collision with the usage code is harmless: a guard that could not
// parse its input has not judged the command either.
const guardDenyExitCode = 2

// enforceVerdict turns a deny or an ask into a blocking exit. Applies to every format: an
// `-o json` caller with a zero status would be told the same lie in a different
// shape. An ask blocks too because the exit status cannot carry the person's approval: a
// glue that reads only the status must stop, and one that renders the host's prompt reads
// the decision off stdout.
//
// The reason reaches stderr only when stdout does not already carry it as prose,
// i.e. every format but text. The guard templates read the verdict off stdout and
// one discards stderr outright, so an unconditional copy printed a kilobyte-plus
// reason twice to an audience with a context budget.
func enforceVerdict(opts OutputOptions, verdict types.GuardJudgment) error {
	return enforceVerdictTo(os.Stderr, opts, verdict)
}

func enforceVerdictTo(errOut io.Writer, opts OutputOptions, verdict types.GuardJudgment) error {
	if verdict.Decision != "deny" && verdict.Decision != "ask" {
		return nil
	}
	if opts.Format != FormatText {
		fmt.Fprintln(errOut, verdict.Reason)
	}
	return errSilent{exitCode: guardDenyExitCode}
}

// writeGuardVerdict renders a verdict through the standard output arm.
func writeGuardVerdict(out io.Writer, opts OutputOptions, verdict types.GuardJudgment) error {
	switch opts.Format {
	case FormatText:
		switch verdict.Decision {
		case "deny":
			fmt.Fprintln(out, decisionLabel("deny", verdict.Rule)+" "+verdict.Reason)
		case "ask":
			fmt.Fprintln(out, decisionLabel("ask", verdict.Rule)+" "+verdict.Reason)
		case "advise":
			fmt.Fprintln(out, decisionLabel("advise", verdict.Rule)+" "+verdict.Context)
		default:
			fmt.Fprintln(out, "pass")
		}
		return nil
	case FormatName:
		fmt.Fprintln(out, verdict.Decision)
		return nil
	}
	return writeFormatted(out, opts, verdict)
}

// decisionLabel opens a text verdict with the decision and, where the rule can identify
// itself, its name: `deny [stage-all]`.
//
// The name was reachable only through `-o json` before this, which meant a person could
// read a refusal and still have nothing to look up, grep a trail for, or cite when
// reporting it as a false positive. Every other verdict magus reaches carries a code a
// reader can take somewhere; this was the exception.
//
// Bracketed rather than appended so it survives a line-wrap next to the decision it
// qualifies, and omitted entirely when the rule is anonymous: an empty `[]` would promise
// an identifier that does not exist.
func decisionLabel(decision, rule string) string {
	if rule == "" {
		return decision + ":"
	}
	return decision + " [" + rule + "]:"
}

// guardInputLimit bounds the payload one hook call may carry.
//
// An MCP params object is caller-controlled and can be megabytes, the raw-event wiring
// forwards the WHOLE event rather than one extracted command string, and the template
// holds a second copy of it under the host's hook timeout. Overflow is a read FAILURE
// rather than a truncation, for the reason readGuardInput gives: a truncated payload is
// exactly the "not the command the guard was shown" case.
const guardInputLimit = 1 << 20

// readGuardInput reports the payload, and the read failure separately from an empty one.
// "No input" is a host that sent nothing and "the read broke" is a payload magus was
// supposed to receive, and only the second one means the command about to run is not the
// command the guard was shown.
func readGuardInput(in io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(in, guardInputLimit+1))
	if err != nil {
		return "", err
	}
	if len(b) > guardInputLimit {
		return "", fmt.Errorf("the payload is larger than the %d-byte limit, so it was not read whole", guardInputLimit)
	}
	return strings.TrimSpace(string(b)), nil
}
