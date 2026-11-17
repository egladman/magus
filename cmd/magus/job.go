package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/libs/text/plural"
	"github.com/egladman/magus/types/diff"

	"github.com/egladman/magus"
	"github.com/egladman/magus/broker"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/contract"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/job/evidence"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/internal/queue"
	"github.com/egladman/magus/internal/selfupdate"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// jobCommand implements `magus job`, the POSIX child lifecycle over delegated work: fork
// declares a job, exec takes the lease on it in this checkout, exit returns it with its
// result, and wait verifies that result. Listing is `magus ls jobs` and the terms are
// `magus describe job`, because enumerating and defining are those verbs' work everywhere
// else in this CLI.
//
// BOTH CHANNELS WRITE, and the store is what keeps the book honest. The client MCP
// tool (magus\job) is the agent's channel and this verb is the person's; they reach the same store and
// the same rules, so a job still has one author (internal/job.authorizeRow enforces it)
// without a capability existing for agents that a person does not have.
func jobCommand(ctx context.Context, root string, args []string) error {
	if len(args) == 0 {
		jobUsage()
		return exit.Usagef("magus job: requires a subcommand (fork, apply, exec, exit, wait, run, rm or prune)")
	}
	switch args[0] {
	case "-h", "--help", "help":
		jobUsage()
		return nil
	case hint.JobFork.Leaf():
		return jobFork(ctx, root, args[1:])
	case hint.JobExec.Leaf():
		return jobExec(ctx, root, args[1:])
	case hint.JobExit.Leaf():
		return jobExit(ctx, root, args[1:])
	case hint.JobWait.Leaf():
		return jobWait(ctx, root, args[1:])
	case hint.JobWatch.Leaf():
		return jobWatch(ctx, root, args[1:])
	case hint.JobRun.Leaf():
		return jobRunCatalog(ctx, args[1:])
	case hint.JobRm.Leaf():
		return jobDelete(ctx, root, args[1:])
	case "apply":
		return jobApply(ctx, root, args[1:])
	case "prune":
		return jobPrune(ctx, root, args[1:])
	case "graph":
		return jobGraphCommand(ctx, root, args[1:])
	case "pause":
		return jobPause(ctx, root, "pause", args[1:])
	case "resume":
		return jobPause(ctx, root, "resume", args[1:])
	case "grant":
		return jobGrant(ctx, root, args[1:])
	case "pin", "advance":
		return jobPinOrAdvance(ctx, root, args[0], args[1:])
	default:
		return exit.Usagef("magus job: unknown subcommand %q (want fork, apply, exec, exit, wait, watch, run, graph, rm, prune, pause, resume, grant, pin or advance; `%s` lists what is in flight)", args[0], hint.LsJobs)
	}
}

// jobCommandSpec is `magus job` as the command registry declares it.
func jobCommandSpec() command.Command {
	i := slices.IndexFunc(commands.Root.Spec.Children, func(c command.Command) bool { return c.Name == "job" })
	return commands.Root.Spec.Children[i]
}

// printRegisteredHelp prints fs's command help from its registry entry,
// so -h, the man page and the docs page read the same words.
// printOwnDefaults prints the entry's examples.
func printRegisteredHelp(fs *flag.FlagSet) {
	spec, _ := commandAt(fs.Name())
	fmt.Fprintf(fs.Output(), "Usage: %s\n\n%s\n\nFlags (global flags also accepted, see `magus -h`):\n", spec.Usage, spec.Long)
	printOwnDefaults(fs)
}

func jobUsage() {
	fmt.Fprintln(os.Stderr, "Usage: "+jobCommandSpec().Usage)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Delegated work, on the shell's own lifecycle. A job is the unit of work; a lease is")
	fmt.Fprintln(os.Stderr, "the grant one holder has on it: the paths it may write and read, plus the one check it runs.")
	fmt.Fprintln(os.Stderr, "A job is not a run: `magus run` executes a target with no job involved, while a job's")
	fmt.Fprintln(os.Stderr, "check and the server's maintenance each cause runs.")
	fmt.Fprintln(os.Stderr, "Kept per repository, so every worktree and clone reads one set of jobs.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	for _, c := range jobCommandSpec().Children {
		fmt.Fprintf(os.Stderr, "  %-6s %s\n", c.Name, c.Short)
	}
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "`%s` lists every job in flight, and `%s` prints one job's terms.\n", hint.LsJobs, hint.DescribeJob.With("<job>"))
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "One author per job, enforced by the store: a session holding a lease may release")
	fmt.Fprintf(os.Stderr, "paths and end its own job, and nothing else. %s (magus\\job) is the\n", hint.ToolClient)
	fmt.Fprintln(os.Stderr, "same store through an agent's channel.")
}

// pointAtConsole names where to WATCH a job (the Jobs view for an empty id), the one way
// to see how a worker is doing that leaves it alone. With no server it names no link: a
// page that never loads cannot say whether nothing is listening or the console is broken.
// The notice names that state and a next command rather than handing out a dead link.
// It prints only when asked: a caller that did not ask to watch gets silence.
func pointAtConsole(ctx context.Context, id string, asked bool) {
	if globalCfg.Console.Enabled != nil && !*globalCfg.Console.Enabled {
		return
	}
	serving, serverVersion := probeConsoleServer()
	if !serving {
		if asked {
			slog.InfoContext(ctx, "the console is not running; start it to watch this job",
				attr.Notice("note"), attr.Next(hint.ServerStart.String()))
		}
		return
	}
	host := mcpAddrString()
	link := console.JobLink(host, id)
	if id == "" {
		link = console.Link(console.LinkOpts{Host: host, App: console.JobApp})
	}
	slog.LogAttrs(ctx, slog.LevelInfo, link, attr.Notice("console"), attr.Next(console.OpenCommand(link)))
	if skew := job.ConsoleSkew(serverVersion, version); skew != "" {
		slog.WarnContext(ctx, skew, attr.Notice("warning"),
			attr.Next(fmt.Sprintf("%s && %s", hint.ServerStop, hint.ServerStart)))
	}
}

// probeConsoleServer reports whether the server is up, and its version. A per-process
// proc server answers a socket too and serves no console, and never binds the server's
// socket, so an answer there is the server.
//
// It probes the server's own address, never MAGUS_PROC_SOCKET: when the server refuses
// a mismatched build, startup points that variable at this process's own proc server, and
// asking it reported "nothing is serving" with the server up.
//
// It makes its OWN bounded context rather than taking the command's. The probe is a local
// socket round trip on the way to printing one line, `magus ls jobs` reaches it through a
// caller that has no context to pass, and a link nobody can build is not worth widening four
// signatures for.
func probeConsoleServer() (serving bool, serverVersion string) {
	ctx, cancel := context.WithTimeout(context.Background(), consoleProbeTimeout)
	defer cancel()
	st, err := proc.QueryStatus(ctx, resolveServerAddr(""))
	if err != nil || st == nil || st.Server == nil {
		return false, ""
	}
	return true, st.Version
}

// consoleProbeTimeout bounds that probe. A server on the same machine answers in
// milliseconds; anything slower is one that cannot serve a console page either.
const consoleProbeTimeout = 2 * time.Second

// jobOpen is how a command opens the job store.
type jobOpen struct {
	// DryRun makes the store's reads write nothing: no sweep and no cached footprint.
	DryRun bool
	// ModelTiers wires the workspace's model tiers, matrix and owners, for a write that
	// creates a row or changes its model, so the store assigns the row's tier.
	ModelTiers bool
}

// jobPlace places the job store and the checkout record of the workspace at root: its
// cache dir and the user state dir.
func jobPlace(root string) (job.Location, error) {
	cacheDir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
	if err != nil {
		return job.Location{}, err
	}
	stateBase, err := xdg.StateDir()
	if err != nil {
		return job.Location{}, fmt.Errorf("resolve the user state dir, set XDG_STATE_HOME to a writable absolute path: %w", err)
	}
	return job.Location{CacheDir: cacheDir, StateBase: stateBase, Root: root}, nil
}

// openJobs opens the job store of the workspace at root. With o.ModelTiers a workspace
// that will not load still opens the store, so the fork or apply that fixes the break can
// be declared: it assigns no tier and checks no tier limit, and says so on stderr with
// the load error, since a tier declaration that failed to load must not read as one that
// declared nothing. A root of "" is no workspace at all, and assigns nothing.
func openJobs(ctx context.Context, root string, o jobOpen) (*job.Store, error) {
	loc, err := jobPlace(root)
	if err != nil {
		return nil, err
	}
	loc.Outputs, loc.DryRun = outputsIn, o.DryRun
	loc.TrailBase = func(checkoutRoot string) (string, error) { return magus.ResolveCacheDir(checkoutRoot, cliOptions()...) }
	if root != "" {
		// Read once, and only when a job ends with a priceable trail, since it loads the
		// workspace's harness spells.
		table := sync.OnceValue(func() model.Table { return sessionModelTable(root) })
		loc.PriceContext = func(calls []types.AgentFacts) (int64, float64, bool) {
			floor, why := table().PriceContext(calls)
			return floor.Tokens, floor.USD, why == ""
		}
		// Lazy: the harness spells load only when a row names a model.
		loc.ModelRows = guard.HarnessModelRows(ctx, root, func(ctx context.Context) (types.WorkspaceRepository, error) {
			return inspectWorkspace(ctx, root)
		})
	}
	if o.ModelTiers && root != "" {
		m, err := loadMagus(ctx, root)
		if err != nil {
			slog.WarnContext(ctx, "the workspace did not load, so no model tier was assigned and no tier limit was checked; a row written now carries no tier until its model changes",
				attr.Unchecked(), attr.Error(err))
			return job.NewStore(loc), nil
		}
		user, err := config.LoadWithRoot("", root)
		if err != nil {
			return nil, err
		}
		loc.ModelTiers = m.ModelTiers(ctx)
		var userMatrix types.ModelTierMatrix
		if user.Jobs.ModelTierMatrix != "" {
			userMatrix = bindings.UserModelTierMatrix(user.Jobs.ModelTierMatrix, m.ModelTiers)
		}
		loc.ModelTierMatrix = job.ComposeModelTierMatrix(m.ModelTierMatrix(), userMatrix)
		loc.SpawnRule = viewInternals(m).CheckSpawn
		loc.SpawnCeiling = viewInternals(m).SpawnCeiling
		loc.Owners = func(ctx context.Context, files []string) ([]string, error) {
			res, err := m.AffectedFromPaths(ctx, files)
			if err != nil {
				return nil, err
			}
			return res.Seed, nil
		}
	}
	return job.NewStore(loc), nil
}

// reportForkFacts prints what a top-level fork leaves waiting. It never stops
// the fork: policy decides that, on magus\workspace().forkFacts().
func reportForkFacts(ctx context.Context, store *job.Store, root string) {
	rows, err := store.List()
	if err != nil {
		slog.InfoContext(ctx, "the work waiting off the base could not be counted", attr.Error(err), attr.Notice("note"))
		return
	}
	var last job.FullTest
	dir, dirErr := magus.ResolveCacheDir(root, cliOptions()...)
	if dirErr == nil {
		last, dirErr = cache.NewOutputStore(dir).LastFullTest()
	}
	facts := job.ForkFacts(ctx, root, rows, last)
	if dirErr != nil {
		facts.TestUnknown = fmt.Sprintf("the output store could not be read: %s", dirErr)
	}
	slog.InfoContext(ctx, job.ForkFactsLine(facts), attr.Notice("note"))
}

// lsJobs is `magus ls jobs`: every live job this repository carries, whoever holds it.
func lsJobs(root string, args []string) error {
	var all bool
	rest, err := commandParse("ls jobs", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&all, "all", false, "List the jobs that ended and every in-flight change, not only the live jobs and yours")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus ls jobs [--all] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print every live job as a tree, each with its state, model, write-path count, what")
			fmt.Fprintln(os.Stderr, "its fork could prove about its write paths (PROOF) and its check, followed by every")
			fmt.Fprintln(os.Stderr, "pair that claims the same path.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A job that ended (pass, fail or no_return) is left out and counted in a notice;")
			fmt.Fprintln(os.Stderr, "--all lists it too.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "PROOF is what the checkout looked like when the job was forked: alone (nothing")
			fmt.Fprintln(os.Stderr, "else live was bound there), disjoint, or overlapping.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Then the open changes in flight against the queue's base, as the last")
			fmt.Fprintln(os.Stderr, "`magus queue ls --provider <provider> --base <branch>` read them; this never")
			fmt.Fprintln(os.Stderr, "fetches. A change is yours when a job of your job tree works on its branch or")
			fmt.Fprintln(os.Stderr, "your forge login opened it; the others print as one count line unless --all.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Example:")
			fmt.Fprintln(os.Stderr, `  magus ls jobs -o template='{{range .jobs}}{{.id}}  {{.checkout_root}}{{"\n"}}{{end}}'`)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return exit.Usagef("magus ls jobs: takes no arguments (got %q)", rest[0])
	}
	ctx := context.Background()
	store, err := openJobs(ctx, root, jobOpen{})
	if err != nil {
		return err
	}
	// The store's report: magus\workspace().jobs() and ListJobs build the same.
	// Its overlaps load the workspace only for a pair across checkouts.
	list, err := store.Report(ctx, time.Now().Unix(), globalCfg.Jobs.StaleAfter)
	if err != nil {
		return err
	}
	list.Overlaps = job.ContendedOverlaps(list.Jobs, list.Overlaps, sync.OnceValues(func() ([]*types.Project, string) {
		ws := jobWorkspace(ctx, root)
		return ws.Projects, ws.Unavailable
	}))
	list, me, err := joinInflight(ctx, root, store, list)
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	// Before ended rows drop out: their work is what most often goes missing.
	list.Unmerged = unmergedReport(ctx, resolveRootOrEmpty(root), list.Jobs, types.NewJobAttempts(list.Attempts), opts.Format)
	if !all {
		var ended int
		list, ended = job.WithoutEnded(list)
		if ended > 0 {
			slog.InfoContext(ctx, fmt.Sprintf("%d ended %s left out; `%s` lists them", ended, plural.Word(ended, "job", "jobs"), hint.LsJobs.With("--all")),
				attr.Notice("jobs-ended"))
		}
	}
	// Nothing names a caller with no lease and no login, so none of the changes is theirs
	// and the list is everyone's.
	all = all || !me.Known()

	switch opts.Format {
	case outputName:
		ids := make([]string, len(list.Jobs))
		for i, one := range list.Jobs {
			ids[i] = one.ID
		}
		return emitNames(os.Stdout, ids)
	case outputText:
		job.PrintTree(os.Stdout, list)
		fmt.Fprintln(os.Stdout)
		for _, line := range list.Unmerged.Lines() {
			fmt.Fprintln(os.Stdout, line)
		}
		if len(list.Unmerged.Risks) > 0 {
			fmt.Fprintf(os.Stdout, "unresolved risks, which stop nothing: %d\n", len(list.Unmerged.Risks))
		}
		for _, risk := range list.Unmerged.Risks {
			fmt.Fprintf(os.Stdout, "  %s: %q\n", risk.Job, risk.Item)
		}
		job.PrintInflight(os.Stdout, list, all)
		pointAtConsole(context.Background(), "", false)
		return nil
	default:
		list.Changes = job.InflightScope(list.Changes, all)
		return emitFormatted(opts, list)
	}
}

// describeJob is `magus describe job`: one job's terms, which is what a holder reads on
// arrival. It prints the criteria, the write paths, the check, the model, the checkpoint, the
// dependencies, what the workspace itself puts out of reach, the graph's blast radius for
// each write path, and where each declared goal stands graded against the evidence magus
// holds now. It prints no procedure: taking the job is `magus job exec`'s work to DO,
// not a paragraph for somebody to follow by hand.
func describeJob(ctx context.Context, root string, args []string) error {
	pos, err := commandParse("describe job", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe job <job> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print one job's terms: its criteria, write paths, check and dependencies, the paths this")
			fmt.Fprintln(os.Stderr, "workspace puts out of reach, the graph's blast radius for each write path, and where each")
			fmt.Fprintln(os.Stderr, "declared goal stands graded against the evidence magus holds now.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "It renders context and never a status: magus assembles what it holds and you")
			fmt.Fprintln(os.Stderr, "hand it to whoever takes the job. Grading a goal is")
			fmt.Fprintln(os.Stderr, "still a READ: it records nothing, so asking never advances a job and never blocks the")
			fmt.Fprintf(os.Stderr, "holder still working on it. It is the same grading `%s` does, so the\n", hint.JobWait)
			fmt.Fprintln(os.Stderr, "two cannot disagree.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Example:")
			fmt.Fprintln(os.Stderr, "  magus describe job refactor/pricing")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return exit.Usagef("magus describe job: requires exactly one job")
	}
	root = resolveRootOrEmpty(root)
	store, err := openJobs(ctx, root, jobOpen{DryRun: globalCfg.DryRun})
	if err != nil {
		return err
	}
	leases, err := store.List()
	if err != nil {
		return err
	}
	leases = job.HardenWritten(ctx, store, leases)
	i := slices.IndexFunc(leases, func(lease types.Job) bool { return lease.ID == pos[0] })
	if i < 0 {
		return fmt.Errorf("magus describe job: there is no job %q (run `%s` to see them)", pos[0], hint.LsJobs)
	}
	row := leases[i]
	if err := jobRefusesTheGate(ctx, root, row); err != nil {
		return err
	}

	// leaseBoundary took 12 s warm and most of 22 s cold, printing nothing.
	waiting := time.AfterFunc(3*time.Second, func() {
		slog.InfoContext(ctx, fmt.Sprintf("loading the workspace and the knowledge graph for %s's terms, usually 10 to 25 s", row.ID), attr.Notice("note"))
	})
	defer waiting.Stop()

	// A read: the symbol goals, their delta, the deletion candidates and the graph evidence
	// take the graph as built, and none while a symbol index is stale (see evidence.Graph).
	graph := newJobGraph(root, false)
	facts := leaseBoundary(ctx, root, store, row, leases, graph)
	waiting.Stop()
	facts.Evidence, facts.GraphCold = evidence.LeaseGraphEvidence(ctx, graph, row.Paths.Write)
	tries, err := store.Attempts()
	if err != nil {
		return err
	}
	facts.Attempts = types.NewJobAttempts(tries)[row.ID]
	brief := job.NewTerms(row, facts)

	status, err := job.GradeGates(ctx, store, row.ID, jobObserver(root, graphsByTree(root, graph, false)))
	if err != nil {
		return exit.Usagef("magus describe job: %s", err)
	}
	if evidence.GradesSymbols(leases, row.ID) {
		status.StaleIndexes = types.StaleIndexProjects(graph.StaleIndexes(ctx))
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		return emitNames(os.Stdout, []string{row.ID})
	case outputText:
		fmt.Print(brief.String())
		job.PrintWorktree(os.Stdout, row, false)
		job.RenderGates(os.Stdout, status)
		job.RenderGoalCommands(os.Stdout, row)
		if running := guard.JobBackgroundCommands(root, row, time.Now()); running != "" {
			fmt.Printf("background commands its session still runs\n%s\n", running)
		}
		job.PrintEntries(os.Stdout, row.Entries)
		joined, _, err := joinInflight(ctx, root, store, types.JobList{Jobs: leases})
		if err != nil {
			return err
		}
		job.PrintChange(os.Stdout, joined.Changes, row.ID)
		pointAtConsole(ctx, row.ID, false)
		return nil
	default:
		return emitFormatted(opts, job.DescribeOutput{Terms: brief, Goals: status})
	}
}

// checkoutBaseToken is the base this checkout is on, in the one form the store compares
// against a job's checkpoint: `<rev>`, or `<rev>+<digest>` when the tree is dirty.
//
// Computed here rather than taken from the caller, which is what makes the divergence
// verdict a fact. It is the same value `magus vcs checkpoint -o name` prints, from the
// same function, so the token a person cites and the token exec records cannot differ.
//
// It reads magus.yaml's vcs keys and never the magusfiles, so a workspace whose load is
// broken still has a base: the fork that fixes the break needs one.
func checkoutBaseToken(ctx context.Context, root string) (string, error) {
	if root == "" {
		return "", errors.New("no workspace was found, so this checkout has no base to record")
	}
	root, err := workspace.FindRoot(root)
	if err != nil {
		return "", err
	}
	opts := configuredVCS()
	res, err := vcs.Resolve(ctx, root, "", opts)
	if err != nil {
		return "", err
	}
	cp, err := vcs.Checkpoint(ctx, root, res, false)
	if err != nil {
		return "", err
	}
	return cp.Token(), nil
}

// evidenceFlagDoc is `job wait --evidence`, spelled once for the flag set and the registry.
const evidenceFlagDoc = "Grade check goal `<goal>` against the run `<ref>` recorded in THIS checkout instead of the job's own; `" +
	types.PrimaryGoalID + "` names the job's check. The paths the job changed must read the same here as in the job's checkout," +
	" and as the run read them: at its revision for a run of a clean tree, else unchanged since it started; repeatable"

// evidenceFlag collects `--evidence <goal>=<ref>` pairs. A goal named twice is refused
// rather than resolved to whichever came last, since the two refs may be different runs.
// Set writes through the map it was made with, so it must not be nil.
type evidenceFlag map[string]string

func (e evidenceFlag) String() string {
	pairs := make([]string, 0, len(e))
	for _, goal := range slices.Sorted(maps.Keys(e)) {
		pairs = append(pairs, fmt.Sprintf("%s=%s", goal, e[goal]))
	}
	return strings.Join(pairs, ",")
}

func (e evidenceFlag) Set(value string) error {
	goal, ref, ok := strings.Cut(value, "=")
	if goal, ref = strings.TrimSpace(goal), strings.TrimSpace(ref); !ok || goal == "" || ref == "" {
		return fmt.Errorf("want <goal>=<ref>, got %q", value)
	}
	if _, dup := e[goal]; dup {
		return fmt.Errorf("goal %q is named twice", goal)
	}
	e[goal] = ref
	return nil
}

// jobFork declares one job, from flags or from a JSON record on stdin.
//
// THE PERSON'S WRITE. An orchestrating agent forks through the client tool (magus\job); this is the
// same store and the same rules for somebody at a terminal, which is what keeps the job
// store from being a thing only agents can write. The flags cover the one-job case so that
// declaring work does not require composing a JSON document, and --stdin takes the record
// when a script already has one.
//
// It REPLACES the job it names rather than merging into it, unlike the tool's put: a
// person typing a job is declaring what it is, while an agent advancing one field of a
// live job must not erase the rest.
func jobFork(ctx context.Context, root string, args []string) error {
	var (
		row  types.Declaration
		spec *flaggen.JobForkFlags
		// fromFlags is a fork whose record the flags declare, built once the
		// store can read the --model word.
		fromFlags bool
		// retry is a fork naming an id and no spec flag, which forks a stored job again as declared.
		retry bool
		flags *flag.FlagSet
	)
	pos, err := commandParse("job fork", args, func(fs *flag.FlagSet) {
		flags = fs
		spec = flaggen.BindJobFork(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job fork <job> [flags]")
			fmt.Fprintln(os.Stderr, "       magus job fork --stdin < job.json")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Declare one job: what its holder is handed, where it may write, and the one")
			fmt.Fprintln(os.Stderr, "check it runs. It replaces any job with the same id; an id with no spec flag")
			fmt.Fprintln(os.Stderr, "forks that job again with the spec it was declared with.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Goals are data: the record's `goals` declares what done means (a check that")
			fmt.Fprintln(os.Stderr, "passed; paths or symbols changed, present, absent or unreferenced), and")
			fmt.Fprintf(os.Stderr, "`%s` grades them. `--schema` prints the record. A job that\n", hint.JobWait)
			fmt.Fprintln(os.Stderr, "writes is refused without a check or a goal, and one naming no write paths must")
			fmt.Fprintln(os.Stderr, "be --read-only. A read-only job passes only by a script check, `\"check\":")
			fmt.Fprintln(os.Stderr, "{\"script\": \"<probe>.buzz\"}` in a --stdin record, whose result cites the ref")
			fmt.Fprintf(os.Stderr, "`%s` prints; without one its holder ends it with\n", hint.Buzz.With("--record", "<probe>.buzz"))
			fmt.Fprintf(os.Stderr, "`%s`, recorded %s.\n", hint.JobExit, types.StateNoReturn)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A session holding a lease may only fork a CHILD of its own job, inside its own")
			fmt.Fprintln(os.Stderr, "paths; widening a boundary is the forking session's.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The whole record is validated at once, and a fork with problems lists every one,")
			fmt.Fprintln(os.Stderr, "each with its code and fix, and writes nothing. The global --dry-run does the")
			fmt.Fprintln(os.Stderr, "same without forking: it prints the problems, the record as it would be stored,")
			fmt.Fprintln(os.Stderr, "the permitted problems and the checks that could not run, and exits 1 while any")
			fmt.Fprintln(os.Stderr, "problem remains. A deviation in the record keeps a broad write path, an output a")
			fmt.Fprintln(os.Stderr, "target regenerates outside the write paths, or a ci target the job does not run.")
			fmt.Fprintln(os.Stderr, "--fix makes the edits those problems name (the output added to the write paths,")
			fmt.Fprintln(os.Stderr, "or permitted for the integrator; the gate's goal added), validating again until no")
			fmt.Fprintln(os.Stderr, "edit is left, says each one it made, and lists only the problems that need a")
			fmt.Fprintln(os.Stderr, "person. With --checkout it also moves a checkpoint this checkout cannot place to")
			fmt.Fprintln(os.Stderr, "its HEAD. Without it, the list says how many --fix would apply.")
			fmt.Fprintln(os.Stderr, "A fork also notes a check whose last real run took longer than a wired agent host")
			fmt.Fprintln(os.Stderr, "lets a foreground call wait, which its holder cannot wait on.")
			fmt.Fprintln(os.Stderr, "When this machine's build budget is full it says how full and which run has held")
			fmt.Fprintln(os.Stderr, "it longest: wait before starting the job's worker, which never waits for a seat.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A --stdin record carrying only id and `enter` ENTERS a live job instead: it")
			fmt.Fprintln(os.Stderr, "acknowledges one write into that path by somebody other than its holder, and the")
			fmt.Fprintln(os.Stderr, "job records it. The guard lets that one write through once the holder has been")
			fmt.Fprintln(os.Stderr, "idle for a minute. A job takes two entries; past that, resume its holder.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "--checkout <dir> makes a new checkout of this repository at dir (a git worktree,")
			fmt.Fprintln(os.Stderr, "an hg share, a jj workspace) at the job's checkpoint, or at this checkout's")
			fmt.Fprintf(os.Stderr, "revision when it names none. When this checkout runs its own ./magus, a checkout\n")
			fmt.Fprintln(os.Stderr, "holding the hook its magus.yaml declares as checkout.bootstrap builds its own")
			fmt.Fprintln(os.Stderr, "with that hook, run from this checkout, from its own sources")
			fmt.Fprintln(os.Stderr, "before the fork lands, and any other gets a copy. The job is taken there and")
			fmt.Fprintln(os.Stderr, "nowhere else, and once it ends magus removes the checkout if it is clean and")
			fmt.Fprintln(os.Stderr, "every commit in it is on a branch, bookmark, remote or its base; otherwise it")
			fmt.Fprintln(os.Stderr, "keeps it and records why. A dir that already stands as a linked checkout is")
			fmt.Fprintln(os.Stderr, "adopted instead, and magus never removes it. It is the one job flag --stdin")
			fmt.Fprintln(os.Stderr, "takes, for a record that names no checkout; the record holds the rest.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "There is no --state: this declares a NEW job, and one nobody has taken is")
			fmt.Fprintf(os.Stderr, "%s. A holder moves its own job with `%s` and `%s`.\n", types.StateDeclared, hint.JobExec, hint.JobExit)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Example:")
			fmt.Fprintln(os.Stderr, "  magus job fork refactor/pricing --write-paths pricing/total.sh --check 'test .'")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if spec.Schema {
		fmt.Print(job.DeclarationSchema)
		return nil
	}

	if spec.Checkout != "" {
		if spec.Checkout, err = filepath.Abs(spec.Checkout); err != nil {
			return exit.Usagef("magus job fork: --checkout: %s", err)
		}
	}
	switch {
	case spec.Stdin:
		if len(pos) > 0 {
			return exit.Usagef("magus job fork: --stdin reads the id from the record, so %q is one id too many", pos[0])
		}
		var dropped []string
		flags.Visit(func(f *flag.Flag) {
			if slices.Contains(forkSpecFlags, f.Name) {
				dropped = append(dropped, "--"+f.Name)
			}
		})
		if len(dropped) > 0 {
			return exit.Usagef("magus job fork: --stdin takes the job from the record, so %s would be ignored: put it in the record (`%s` prints its fields)",
				strings.Join(dropped, ", "), hint.JobFork.With("--schema"))
		}
		var defaulted bool
		if row, defaulted, err = job.DecodeStdinDeclaration(os.Stdin); err != nil {
			return exit.Usagef("magus job fork: %s (`%s` prints the schema it must satisfy)", err, hint.JobFork.With("--schema"))
		}
		if defaulted {
			slog.InfoContext(ctx, fmt.Sprintf("the record names no api_version, so it was read as %s", types.JobAPIVersion), attr.Notice("note"))
		}
		if row.Checkout, err = recordCheckout(row.Checkout, spec.Checkout); err != nil {
			return err
		}
	case len(pos) != 1:
		return exit.Usagef("magus job fork: requires exactly one job id, or --stdin with a job on it")
	default:
		fromFlags = true
		retry = true
		flags.Visit(func(f *flag.Flag) { retry = retry && !slices.Contains(forkSpecFlags, f.Name) })
	}

	root = resolveRootOrEmpty(root)
	if row.Enter != "" {
		store, err := openJobs(ctx, root, jobOpen{})
		if err != nil {
			return err
		}
		return jobEnter(ctx, store, row.ID, row.Enter)
	}
	store, err := openJobs(ctx, root, jobOpen{ModelTiers: true})
	if err != nil {
		return err
	}
	jobWS := jobWorkspace(ctx, root)
	store = store.InWorkspace(jobWS).WithGrader(guard.NewForkGrader(forkHeldWorkspace(ctx, root, jobWS), loadGuardDependencies).Grade)
	if fromFlags {
		if row, err = job.FlagDeclaration(pos[0], *spec, store.ModelRequest); err != nil {
			return err
		}
		if err := row.Validate(); err != nil {
			return exit.Usagef("magus job fork: %s", err)
		}
	}
	if retry {
		if row, err = job.RetriedDeclaration(ctx, store, row); err != nil {
			return err
		}
	}
	if row, err = job.StackedHead(ctx, root, row, configuredVCS()); err != nil {
		return exit.Usagef("magus job fork: %w", err)
	}
	if len(row.Integrates) > 0 {
		if len(row.Paths.Write) > 0 {
			return exit.Usagef("magus job fork: an integration job's write paths are the files its branches changed, computed here, so the record names none: drop paths.write")
		}
		// A checkout that already stands is where the merge happens, so its revision is the
		// one the branches are compared with, not the forker's.
		at, from := root, row.Checkpoint
		if info, statErr := os.Stat(row.Checkout); row.Checkout != "" && statErr == nil && info.IsDir() {
			if at, err = filepath.Abs(row.Checkout); err != nil {
				return exit.Usagef("magus job fork: checkout %s: %s", row.Checkout, err)
			}
			// From an older checkpoint, every commit the checkout
			// gained since would read as a branch's change.
			from = ""
		}
		paths, base, err := job.IntegrationWritePaths(ctx, at, from, row.Integrates)
		if err != nil {
			return exit.Usagef("magus job fork: integrates: %s", err)
		}
		if len(paths) == 0 {
			if paths, err = job.UnfinishedIntegration(ctx, store, row, jobObserver(root, graphsByTree(root, newJobGraph(root, false), false))); err != nil {
				return err
			}
			if len(paths) == 0 {
				return exit.Usagef("magus job fork: integrates: %s change nothing since %s, so there is nothing to merge", strings.Join(row.Integrates, ", "), base)
			}
			slog.InfoContext(ctx, fmt.Sprintf("%s changes nothing left to merge, and %s's goals are unmet, so it is forked again on the paths it held", strings.Join(row.Integrates, ", "), row.ID), attr.Notice("note"))
		}
		switch {
		case row.Checkpoint == "":
			slog.InfoContext(ctx, fmt.Sprintf("the record names no checkpoint, so the branches were compared with the revision %s of %s, and the job starts there", base, at), attr.Notice("note"))
			row.Checkpoint = base
		case from == "" && !job.CheckpointNames(row.Checkpoint, base):
			slog.InfoContext(ctx, fmt.Sprintf("checkout %s stands at %s, so the branches were compared with that revision instead of the checkpoint %s, and the job starts there", at, base, row.Checkpoint), attr.Notice("note"))
			row.Checkpoint = base
		}
		row.Paths.Write, row.Paths.Soft = job.IntegrationOutputs(jobWS, paths)
		if jobWS.Unavailable != "" {
			slog.InfoContext(ctx, fmt.Sprintf("the workspace did not load (%s), so the job holds only the files its branches changed, and no declared output", jobWS.Unavailable), attr.Notice("note"))
		}
	}
	if row, err = job.ForkCheckout(ctx, store, root, row); err != nil {
		return err
	}
	// Claimed after job.ForkCheckout and released before the worktree is provisioned: a build
	// there claims its own seat, and a step behind its own process's claim may never seat.
	seated, err := admitForkValidation(ctx, processBrokerClient(), root)
	if err != nil {
		return err
	}
	releaseSeat := sync.OnceFunc(seated)
	defer releaseSeat()
	// The graph loads only when a goal names a symbol, the footprint could extend by its
	// referrers, or the report lists deletion candidates, and they share one load.
	graph := newJobGraph(root, true)
	reader := evidence.GraphSymbolReader(graph.Load)
	row, notes := evidence.ExtendedFootprint(ctx, store, graph, root, row, jobWS)
	for _, n := range notes {
		slog.InfoContext(ctx, n, attr.Notice("note"))
	}
	if spec.Fix {
		if row, err = fixFork(ctx, store, row, reader); err != nil {
			return err
		}
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	// Judged before the worktree is made, so a refusal leaves
	// nothing behind.
	slog.InfoContext(ctx, fmt.Sprintf("judging %s's goals; a goal naming a symbol loads the symbol graph", row.ID), attr.Notice("note"))
	judging := time.Now()
	forkPlan, err := job.PlanFork(ctx, store, row.ID, job.JudgedMerge(row, globalCfg.Jobs.DefaultTimeout), globalCfg.Jobs, reader, globalCfg.DryRun)
	if err != nil {
		return err
	}
	validation := forkPlan.Validation
	slog.InfoContext(ctx, fmt.Sprintf("judged %s's goals in %s", row.ID, cache.FormatDuration(time.Since(judging))), attr.Notice("note"))
	releaseSeat()
	judgedLive := job.NoteForkJudgedAsUpdate(ctx, store, row.ID)
	if row.Parent == "" && len(row.Integrates) == 0 && len(validation.Problems) == 0 {
		reportForkFacts(ctx, store, root)
	}
	if globalCfg.DryRun {
		return forkDryRun(opts, validation)
	}
	if len(validation.Problems) > 0 {
		return job.RefuseFork(validation)
	}
	var (
		worktree types.JobWorktree
		amend    func(*types.Job)
	)
	made, built := false, false
	if row.Checkout != "" {
		exe, _ := os.Executable()
		cache.KickDiskTick(exe, root, globalCfg.Disk.SampleEvery)
		if err := job.RefuseForkOnFullDisk(ctx, diskProbe, exe, root, row.Checkout, int64(globalCfg.Disk.CriticalFreeMB)<<20); err != nil {
			return err
		}
		mirrored := job.MirroredBinary(exe, root)
		priorHead := job.CheckoutHead(ctx, row.Checkout)
		if worktree, made, err = store.ForkWorktree(ctx, row.ID, row.Checkout, row.Checkpoint, mirrored); err != nil {
			return err
		}
		if err := checkoutReadsRow(worktree.Root); err != nil {
			return job.FailedProvision(ctx, store, worktree, made, err)
		}
		followed := !made && priorHead != "" && priorHead != worktree.Base
		stale := !made && !readsTree(ctx, worktree.Binary, worktree.Root)
		// Only a binary this fork placed is pinned: one an adopted
		// checkout already holds may be anyone's build.
		placed := made && worktree.Binary != ""
		if mirrored != "" && (followed || stale || made && worktree.Binary == "") && job.BuildsItsOwnMagus(worktree.Root) {
			reused, err := job.ProvisionCheckout(ctx, store, jobWS, diskProbe, mirrored, root, row, worktree)
			if err != nil {
				return job.FailedProvision(ctx, store, worktree, made, err)
			}
			worktree.Binary, built, placed = filepath.Join(worktree.Root, "magus"), !reused, true
		}
		if _, err := os.Stat(worktree.Binary); placed && errors.Is(err, fs.ErrNotExist) {
			slog.InfoContext(ctx, fmt.Sprintf("%s holds no %s after its bootstrap, so the row records no pin and the hook hands it no call", row.ID, worktree.Binary), attr.Notice("note"))
			placed = false
		}
		if placed {
			if worktree.BinaryDigest, err = job.BinaryDigest(worktree.Binary); err != nil {
				return job.FailedProvision(ctx, store, worktree, made, fmt.Errorf("pinning %s: %w", worktree.Binary, err))
			}
		}
		rows, err := store.List()
		if err != nil {
			return err
		}
		// The job starts from what its checkout holds, not from the forker's tree.
		start, note := job.StartCheckpoint(ctx, rows, row.ID, row.Checkpoint, worktree)
		if note != "" {
			slog.InfoContext(ctx, note, attr.Notice("note"))
		}
		amend = func(u *types.Job) {
			// The validated merge expanded the declared checkpoint already.
			if start != row.Checkpoint {
				u.Checkpoint = start
			}
			u.Worktree = &worktree
		}
	}
	stored, err := job.WriteFork(ctx, store, forkPlan, amend, globalCfg.Jobs)
	switch {
	case err != nil && made:
		err = errors.Join(err, store.DiscardWorktree(ctx, worktree))
	case err == nil && row.Checkout != "":
		if stored, err = store.RecordWorktree(ctx, stored.ID, worktree); err != nil && made {
			err = errors.Join(err, store.DiscardWorktree(ctx, worktree))
		}
	}
	var refused *job.RefusedError
	switch {
	case errors.As(err, &refused):
		return err
	case err != nil:
		job.NoteForkRowEnded(ctx, store, row.ID, judgedLive)
		return exit.Usagef("magus job fork: %w", err)
	}
	var asked types.Job
	row.Apply(&asked)
	derived := slices.DeleteFunc(slices.Clone(stored.Paths.Write), func(p string) bool { return slices.Contains(asked.Paths.Write, p) })
	wholeHeld := map[string][]string{}
	for _, claim := range derived {
		file, decl := types.SplitClaim(claim)
		if slices.ContainsFunc(asked.Paths.Write, func(entry string) bool {
			p, d := types.SplitClaim(entry)
			return d == "" && !strings.ContainsAny(p, "*?[{") && filepath.ToSlash(filepath.Clean(p)) == file
		}) {
			wholeHeld[file] = append(wholeHeld[file], decl)
		}
	}
	var narrowedLines []string
	for _, file := range slices.Sorted(maps.Keys(wholeHeld)) {
		narrowed, _, err := job.NarrowHold(ctx, store, stored.ID, file, wholeHeld[file], "")
		if err != nil {
			slog.InfoContext(ctx, fmt.Sprintf("%s stays whole", file), attr.Notice("note"), attr.Error(err))
			continue
		}
		stored = narrowed
		claims := make([]string, len(wholeHeld[file]))
		for i, decl := range wholeHeld[file] {
			claims[i] = fmt.Sprintf("%s#%s", file, decl)
		}
		narrowedLines = append(narrowedLines, fmt.Sprintf("narrowed %s to %s; a write to another declaration of %s claims it", file, strings.Join(claims, ", "), file))
	}

	if opts.Format == outputName {
		return emitNames(os.Stdout, []string{stored.ID})
	}
	// The notes compare the row with its peers, which the merge read and did not return.
	plan, err := store.List()
	if err != nil {
		return err
	}
	var overrun *job.ForegroundOverrun
	if !stored.ReadOnly && len(stored.Paths.Write) > 0 {
		overrun, err = forkOverrun(ctx, root, stored)
		switch {
		case err != nil:
			warnWorkspaceUnloaded(ctx, err)
		case jobWS.Unavailable != "":
			warnWorkspaceUnloaded(ctx, errors.New(jobWS.Unavailable))
		}
	}
	referenced, referencedGap := evidence.ReferencedOutside(ctx, graph.LoadScoped(stored.Paths.Write), root, stored, jobWS.Projects)
	switch opts.Format {
	case outputText:
		fmt.Printf("forked %s, %s, with %d write path(s). Its holder reads the terms with `%s` and takes it with `%s`\n",
			stored.ID, orDash(string(stored.State)), len(stored.Paths.Write),
			hint.DescribeJob.With(stored.ID), hint.JobExec.With(stored.ID))
		if stored.ModelTier != nil {
			fmt.Printf("model tier: %s\n", stored.ModelTier)
		}
		job.PrintWorktree(os.Stdout, stored, built)
		if stored.Worktree == nil {
			job.PrintLandingBootstrap(ctx, os.Stdout, store, stored)
		}
		job.PrintClaimNotes(os.Stdout, slices.Concat(job.ForkNotes(ctx, store, plan, stored), job.IntegrationOverlaps(plan, stored), job.ReleasedUnderPattern(stored)))
		for _, line := range narrowedLines {
			fmt.Println(line)
		}
		if len(derived) > 0 {
			fmt.Printf("its symbol goals claimed %d declaration(s) its write paths did not cover: -o json lists each\n", len(derived))
		}
		job.RenderDeletionCandidates(os.Stdout, evidence.DeletionCandidates(ctx, graph.LoadScoped(stored.Paths.Write), stored))
		job.RenderReferencedOutside(os.Stdout, referenced, referencedGap)
		job.RenderForegroundOverrun(os.Stdout, overrun)
		// The forker waits for the budget so the holder never does.
		// A seatless row has no holder to hold back.
		if globalCfg.Broker.Resolved() != types.BrokerOff && !stored.HoldsNoBuildSeat() {
			if capacity, err := queryCapacity(ctx); err == nil {
				if full := guard.BudgetFull(capacity, time.Now()); full != "" {
					fmt.Println(full)
				}
			}
		}
		pointAtConsole(ctx, stored.ID, false)
		return nil
	default:
		return emitFormatted(opts, job.ForkReport{
			ForkOutput: job.ForkOutput{
				Job:                  stored,
				DeletionCandidates:   evidence.DeletionCandidates(ctx, graph.LoadScoped(stored.Paths.Write), stored),
				ReferencedOutside:    referenced,
				ReferencedOutsideGap: referencedGap,
				ForegroundOverrun:    overrun,
				ClaimNotes:           slices.Concat(job.ForkNotes(ctx, store, plan, stored), job.IntegrationOverlaps(plan, stored), job.ReleasedUnderPattern(stored)),
			},
			DerivedClaims: derived,
		})
	}
}

// forkSpecFlags are the job fork flags that declare the spec, which a --stdin record carries
// instead and a fork by id alone takes from the stored job.
var forkSpecFlags = []string{"instructions", "new-worker", "timeout", "parent", "checkpoint", "write-paths", "deny-paths", "read-paths", "depends-on", "check", "model", "brief-format", "delivery", "read-only"}

// jobEnter records an entry into job id's path rel, the --stdin record's other shape.
func jobEnter(ctx context.Context, store *job.Store, id, rel string) error {
	stored, err := store.Enter(ctx, id, rel)
	if err != nil {
		return exit.Usagef("magus job fork: %s", err)
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		return emitNames(os.Stdout, []string{stored.ID})
	case outputText:
		fmt.Println(job.EntryAdvice(stored, rel))
		pointAtConsole(ctx, stored.ID, false)
		return nil
	default:
		return emitFormatted(opts, stored)
	}
}

// jobExec takes a job in THIS checkout: it records the base this tree actually landed on
// beside the checkpoint the job was handed, and the checkout it landed in.
//
// It binds nobody. A binding is a fact about WHO is acting, and only the guard hook reads
// the host's session and agent ids, so the guard records it when it lets this command
// through (see guard.bindOnExec). A CLI process knows neither: a binding it wrote could only
// be keyed on the checkout, and one subagent's exec there graded its parent as that
// subagent from the parent's next call.
//
// The base is READ FROM THE CHECKOUT, and no flag passes one in (see job.Store.Exec). A
// holder typing the token it believes it is on is a holder reporting a belief; the
// divergence this records is only worth anything if the value comes from the tree.
func jobExec(ctx context.Context, root string, args []string) error {
	var (
		timeout time.Duration
		slots   int
	)
	var command []string
	sep := slices.Index(args, "--")
	if sep >= 0 {
		args, command = args[:sep], args[sep+1:]
	}
	pos, err := commandParse("job exec", args, func(fs *flag.FlagSet) {
		fs.DurationVar(&timeout, "timeout", 0, "Run the command after -- and stop its whole process group once this long has passed (exit 124)")
		fs.IntVar(&slots, "slots", 1, "The machine budget slots the command after -- takes while it runs")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job exec <job> [flags]")
			fmt.Fprintln(os.Stderr, "       magus job exec --timeout <duration> [--slots N] -- <command> [args...]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Take a job here: read the base this tree is on from version control, as")
			fmt.Fprintln(os.Stderr, "`magus vcs checkpoint -o name` prints it, and record it beside the checkpoint the")
			fmt.Fprintln(os.Stderr, "job was handed, with the divergence between them as a fact rather than a refusal,")
			fmt.Fprintln(os.Stderr, "and the checkout it was taken in. A declared job starts running.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A job another checkout holds is refused: wait grades the holder's checkout, so")
			fmt.Fprintln(os.Stderr, "it moves only once its holder gives it up with `magus job exit` and whoever")
			fmt.Fprintln(os.Stderr, "forked it declares it again with `magus job apply`. Taking it again in the")
			fmt.Fprintln(os.Stderr, "same checkout records the new base.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The guard hook binds the caller to the job when it lets this command through,")
			fmt.Fprintln(os.Stderr, "keyed on the session and subagent its agent host names, so every lease-scoped")
			fmt.Fprintln(os.Stderr, "rule then grades that caller and nobody else. A host that names neither binds")
			fmt.Fprintln(os.Stderr, "this checkout. A subagent whose spawn the guard attributed to the job is bound")
			fmt.Fprintln(os.Stderr, "already, and exec only records its base.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Taking a different job is refused while the one held is still declared or")
			fmt.Fprintln(os.Stderr, "running; once it has exited or ended, exec takes the next one. A job that ended")
			fmt.Fprintln(os.Stderr, "because its holder's session did is taken back by exec in the same checkout.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "With --timeout and a command after --, exec takes no job: it runs the command in")
			fmt.Fprintln(os.Stderr, "its own process group, owned by your lease (BAGGAGE magus.lease or this checkout's")
			fmt.Fprintln(os.Stderr, "binding) or by nobody, and stops the whole group once the timeout passes. The")
			fmt.Fprintln(os.Stderr, "broker records the command with its owner, deadline and slots for `magus server status`,")
			fmt.Fprintln(os.Stderr, "stops the group if this process dies first, and files one attention row for")
			fmt.Fprintln(os.Stderr, "each command it stops. The exit status is the command's, or 124 at the deadline.")
			fmt.Fprintln(os.Stderr, "A magus the command starts runs under those slots and never waits on them.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Examples:")
			fmt.Fprintln(os.Stderr, "  magus job exec refactor/pricing   # in the worktree you will edit")
			fmt.Fprintln(os.Stderr, "  magus job exec --timeout 20m -- rg -l TODO ~/src")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if sep >= 0 || timeout != 0 {
		return jobExecCommand(ctx, root, pos, timeout, slots, command)
	}
	if len(pos) != 1 {
		return exit.Usagef("magus job exec: requires exactly one job (`%s` lists them)", hint.LsJobs)
	}
	root = resolveRootOrEmpty(root)
	if root == "" {
		return errors.New("magus job exec: no workspace here: run from inside one or pass --root <path>")
	}
	store, err := openJobs(ctx, root, jobOpen{})
	if err != nil {
		return err
	}
	if err := refusePaused(store, pos[0]); err != nil {
		return err
	}
	stored, err := store.Exec(ctx, pos[0])
	if err != nil {
		return fmt.Errorf("magus job exec: %w", err)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		return emitNames(os.Stdout, []string{stored.ID})
	case outputText:
		fmt.Println(job.BaseAdvice(stored))
		fmt.Printf("took %s in %s; read the spec with `%s`\n", stored.ID, root, hint.DescribeJob.With(stored.ID))
		return nil
	default:
		return emitFormatted(opts, stored)
	}
}

// jobExecGrace is how long a command asked to stop at its deadline has before its group
// is killed.
const jobExecGrace = 10 * time.Second

// jobExecCommand is `magus job exec --timeout D -- cmd`: it runs cmd as the leader of its
// own process group, owned by the caller's lease, and stops the whole group at the
// deadline. The broker is told what runs. `magus server status` shows it by
// owner, and the broker stops the group if this process dies first. This
// process enforces the deadline too, so it holds with no broker answering.
//
// Nothing about it is for agents only: a person bounds a long search the same way.
func jobExecCommand(ctx context.Context, root string, pos []string, timeout time.Duration, slots int, command []string) error {
	switch {
	case len(pos) > 0:
		return exit.Usagef("magus job exec: a command after -- runs under your own lease and takes no job; drop %q",
			strings.Join(pos, " "))
	case timeout <= 0:
		return exit.Usagef("magus job exec: a command after -- needs --timeout, a positive duration such as 10m: nothing else ends it")
	case len(command) == 0:
		return exit.Usagef("magus job exec: --timeout bounds the command after --, and none follows it")
	case slots < 1:
		return exit.Usagef("magus job exec: --slots is %d; a running command takes at least 1", slots)
	}
	root = resolveRootOrEmpty(root)
	caller := trail.SpawnFromEnv()
	lease := caller.Lease
	if root != "" {
		if loc, err := jobPlace(root); err == nil {
			lease, _ = job.ActingLease(loc, caller.Lease)
			if lease != "" {
				if err := refusePaused(job.NewStore(loc), lease); err != nil {
					return err
				}
			}
		}
	}
	ancestors := types.InvocationAncestorsFromContext(ctx)
	self := types.InvocationAncestorsFromContext(types.AppendInvocationAncestor(ctx, os.Getpid(), types.WrapperInvocation(journal.NewInvocationID())))

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// A magus under the command runs under the hold's claim. Ancestry
	// alone: MAGUS_LEVEL would make a gate under the command act nested.
	cmd.Env = append(os.Environ(), fmt.Sprintf("%s=%s", spawn.AncestorsEnvVar, strings.Join(self, ",")))
	broker.InOwnGroup(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("magus job exec: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	h := broker.Hold{
		Lease: lease, Spawner: caller.Spawner, PGID: cmd.Process.Pid, Command: strings.Join(command, " "),
		Root: root, Slots: slots, Deadline: time.Now().Add(timeout),
		Invocation: self[len(self)-1], Ancestors: ancestors,
	}
	release := holdWithBroker(ctx, h)
	defer release()

	timer := time.NewTimer(time.Until(h.Deadline))
	defer timer.Stop()
	select {
	case err := <-exited:
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return err
		}
		// A command a signal ended has no status of its own; it failed.
		return types.ExitError{Code: max(exit.ExitCode(), 1)}
	case <-ctx.Done():
		stopGroup(h.PGID, exited)
		return ctx.Err()
	case <-timer.C:
		stopGroup(h.PGID, exited)
		job.NoteEnded(ctx, root, lease, broker.EndedMessage(h, h.DeadlineReason()))
		slog.ErrorContext(ctx, fmt.Sprintf("`%s` ran past its %s timeout, so its process group was stopped", h.Command, timeout),
			attr.Notice(""), attr.Component("magus job exec"))
		return types.ExitError{Code: 124}
	}
}

// holdWithBroker hands h to the broker and returns what releases it. A broker that is
// off or does not answer leaves the deadline to this process alone, and says so once.
func holdWithBroker(ctx context.Context, h broker.Hold) func() {
	if globalCfg.Broker.Resolved() == types.BrokerOff {
		return func() {}
	}
	hctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	client := processBrokerClient()
	id, err := client.Hold(hctx, h)
	if err != nil {
		slog.WarnContext(ctx, "the broker is not watching this command, so `magus server status` will not show it; this process still stops it at its deadline",
			attr.Error(err))
		return func() {}
	}
	return func() {
		rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.ReleaseHold(rctx, id); err != nil {
			slog.WarnContext(ctx, "the broker may still hold this command's process group, which ended", attr.Error(err))
		}
	}
}

// stopGroup asks group pgid to stop, kills it if its leader has not exited within
// jobExecGrace, and returns once the leader has.
func stopGroup(pgid int, exited <-chan error) {
	_ = broker.SignalGroup(pgid, false)
	select {
	case <-exited:
	case <-time.After(jobExecGrace):
		_ = broker.SignalGroup(pgid, true)
		<-exited
	}
}

// ledgerAccept grades one worker's report against its row and records the verdict.
//
// THE ENFORCEMENT POINT. Everything else about a lease is a declaration: the ledger
// records, the guard grades writes as they happen, and acceptance was the root agent
// reading a paragraph. A report that ran a filtered subset, wrote outside its boundary, or
// cited evidence from an unrelated run reads exactly like one that did the work, and this
// is where that stops being true.
//
// THE GRADER IS NEVER THE GRADED. A session bound to a lease is refused before anything is
// read: a worker that can accept its own row is the loop's one remaining self-assessment,
// and the store would refuse the resulting write anyway, so it is refused here where the
// message can say why.
//
// Two failing statuses, because the caller is a shell step and the two failures send it
// somewhere different: 2 for a report that could not be decoded (fix the report), 1 for
// one that was decoded and rejected (the work is not accepted).
func jobExit(ctx context.Context, root string, args []string) error {
	var schema, stdin bool
	pos, err := commandParse("job exit", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&schema, "schema", false, "Print the JSON schema a result must satisfy, and exit")
		fs.BoolVar(&stdin, "stdin", false, "Read this job's result from stdin; without it the job ends with no result filed")
		command.NegatedBoolVar(fs, &schema, "schema")
		command.NegatedBoolVar(fs, &stdin, "stdin")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job exit <job> --stdin < result.json")
			fmt.Fprintln(os.Stderr, "       magus job exit <job>...")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Return a job you hold, with the result of the work, filed onto the job itself so")
			fmt.Fprintln(os.Stderr, "whoever waits on it reads the same record from any checkout of this repository.")
			fmt.Fprintln(os.Stderr, "The run behind the result's output ref is resolved HERE and its record is filed")
			fmt.Fprintln(os.Stderr, "alongside, because the output store belongs to this checkout and nobody else can")
			fmt.Fprintln(os.Stderr, "reopen it.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Do the work first, then file this. It is a record of what happened, not a form to")
			fmt.Fprintln(os.Stderr, "fill in while you are still deciding what to do.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A read-only job declaring no check or goal has nothing `magus job wait` could")
			fmt.Fprintf(os.Stderr, "grade, so filing its result records it %s and ends it.\n", types.StatePass)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintf(os.Stderr, "With no --stdin each named job is ended and recorded %s, which is\n", types.StateNoReturn)
			fmt.Fprintln(os.Stderr, "not the same as returning it and failing. Its end_reason says which end it was: a")
			fmt.Fprintln(os.Stderr, "read-only row with nothing to grade, a result nobody verified, a holder that")
			fmt.Fprintln(os.Stderr, "abandoned it, or someone else ending it. Every job named is tried, and the command")
			fmt.Fprintln(os.Stderr, "fails at the end if any could not be ended. To end every job nobody is working in")
			fmt.Fprintln(os.Stderr, "one call, use `magus job prune`.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Ending a job ends every live job below it, so a job with one is refused, naming")
			fmt.Fprintln(os.Stderr, "each, until each is named in the same call. One holding uncommitted work or a")
			fmt.Fprintln(os.Stderr, "running check also waits on a person to dispose the attention request the")
			fmt.Fprintln(os.Stderr, "refusal names. --stdin is refused while a job below is still working.")
			fmt.Fprintln(os.Stderr, "Once a job ends, magus restores what it wrote in the job's checkout and removes")
			fmt.Fprintln(os.Stderr, "the checkout unless it holds work, naming the uncommitted paths that keep it.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Examples:")
			fmt.Fprintln(os.Stderr, "  magus job exit refactor/pricing --stdin < result.json")
			fmt.Fprintln(os.Stderr, "  magus job exit scout-a scout-b scout-c")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if schema {
		fmt.Print(job.ResultSchema)
		return nil
	}
	switch {
	case len(pos) == 0:
		return exit.Usagef("magus job exit: name the job to end")
	case stdin && len(pos) > 1:
		return exit.Usagef("magus job exit: --stdin files one result onto one job, and %d are named", len(pos))
	}
	root = resolveRootOrEmpty(root)
	store, err := openJobs(ctx, root, jobOpen{DryRun: globalCfg.DryRun})
	if err != nil {
		return err
	}

	if rows, listErr := store.List(); listErr == nil {
		for _, row := range rows {
			if !slices.Contains(pos, row.ID) {
				continue
			}
			if running := guard.JobBackgroundCommands(root, row, time.Now()); running != "" {
				slog.InfoContext(ctx, fmt.Sprintf("%s's session still runs these in the background, and ending the job does not stop them:\n%s", row.ID, running), attr.Notice("note"))
			}
		}
	}

	if !stdin {
		return abandonJobs(ctx, store, pos)
	}

	result, err := job.DecodeResult(os.Stdin)
	if err != nil {
		return exit.Usagef("magus job exit: %s\nFile this shape:\n%s", err, job.FiledShape(store, pos[0]))
	}
	// Exit owns the portable evidence snapshot for every declared gate. Keeping the
	// CLI as a decoder/renderer avoids a second lifecycle that silently files only
	// the historical primary check.
	stored, err := job.Exit(ctx, store, pos[0], &result, storedAttempts(root))
	if err != nil {
		return exit.Usagef("magus job exit: %s", err)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	out := io.Writer(os.Stderr)
	switch opts.Format {
	case outputName:
		err = emitNames(os.Stdout, []string{stored.ID})
	case outputText:
		out = os.Stdout
		if stored.State.Terminal() {
			fmt.Printf("exited %s, recorded %s: %s\n", stored.ID, stored.State, stored.EndReason)
		} else {
			fmt.Printf("exited %s, recorded %s, with its result filed. Whoever forked it verifies with `%s`\n",
				stored.ID, stored.State, hint.JobWait.With(stored.ID))
			slog.InfoContext(ctx, job.ExitedBindingNote, attr.Notice("note"))
		}
		job.RenderFiled(os.Stdout, stored)
	default:
		err = emitFormatted(opts, stored)
	}
	// A row the result ended (a read-only one nothing else grades) gives its checkout back now.
	if err == nil && stored.State.Terminal() {
		err = endNamedWorktrees(ctx, out, store, stored.ID)
	}
	return err
}

// abandonJobs ends every named job as no_return (see [job.Abandon]), printing one line per
// job with the end_reason the store recorded for it. A job that cannot be ended does not
// stop the rest: each is tried, and the command fails afterwards naming every refusal.
func abandonJobs(ctx context.Context, store *job.Store, ids []string) error {
	ended, already, refused := job.Abandon(ctx, store, ids)
	endedIDs := make([]string, len(ended))
	for i, stored := range ended {
		endedIDs[i] = stored.ID
		fmt.Printf("ended %s, recorded %s: %s\n", stored.ID, stored.State, stored.EndReason)
	}
	for _, over := range already {
		fmt.Println(over.Error())
	}
	removed := endNamedWorktrees(ctx, os.Stdout, store, endedIDs...)
	if len(refused) > 0 {
		reasons := make([]string, len(refused))
		for i, err := range refused {
			reasons[i] = err.Error()
		}
		return errors.Join(exit.Usagef("magus job exit: %d of %d jobs not ended:\n  %s",
			len(ids)-len(ended)-len(already), len(ids), strings.Join(reasons, "\n  ")), removed)
	}
	return removed
}

// recordCheckout is the checkout a --stdin record runs in, made absolute.
// The --checkout flag fills in when the record names none.
func recordCheckout(fromRecord, fromFlag string) (string, error) {
	if fromRecord == "" {
		return fromFlag, nil
	}
	abs, err := filepath.Abs(fromRecord)
	if err != nil {
		return "", exit.Usagef("magus job fork: the record's checkout: %s", err)
	}
	if fromFlag != "" && abs != fromFlag {
		return "", exit.Usagef("magus job fork: --checkout %s and the record's checkout %s name two checkouts: give one", fromFlag, fromRecord)
	}
	return abs, nil
}

// jobWait collects a returned job's result and verifies it against the job's own terms.
//
// THE ENFORCEMENT POINT. Everything else about a job is a declaration: the store records,
// the guard grades writes as they happen, and verification used to be the forking agent
// reading a paragraph. A result that ran a filtered subset, wrote outside its write paths, or
// cited a run from somewhere else reads exactly like one that did the work, and this is
// where that stops being true.
//
// THE VERIFIER IS NEVER THE VERIFIED. A session holding a lease is refused before the
// result is read unless the job sits below that lease: a holder that can verify its own
// job, a sibling's or an ancestor's is the loop's one remaining self-assessment. Waiting
// on a child it forked is the orchestrator's seat, one level down.
//
// Two failing statuses, because the caller is a shell step and the two send it somewhere
// different: 2 for a result magus could not read at all (fix the result), 1 for one that
// was read and rejected (the work is not verified).
func jobWait(ctx context.Context, root string, args []string) error {
	var schema, stdin, integration bool
	evidence := evidenceFlag{}
	pos, err := commandParse("job wait", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&schema, "schema", false, "Print the JSON schema a result must satisfy, and exit")
		fs.BoolVar(&stdin, "stdin", false, "Read the result from stdin instead of from the job, for one that was never filed")
		fs.BoolVar(&integration, "integration", false, "Grade only the job's check goals against the runs the --stdin result names in THIS checkout, and record that beside its state")
		command.NegatedBoolVar(fs, &schema, "schema")
		command.NegatedBoolVar(fs, &stdin, "stdin")
		command.NegatedBoolVar(fs, &integration, "integration")
		fs.Var(evidence, "evidence", evidenceFlagDoc)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job wait <job>")
			fmt.Fprintln(os.Stderr, "       magus job wait <job> --stdin < result.json")
			fmt.Fprintln(os.Stderr, "       magus job wait <job> --evidence <goal>=<ref> [--evidence <goal>=<ref> ...]")
			fmt.Fprintln(os.Stderr, "       magus job wait <job> --integration --stdin < evidence.json")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Verify the result a job was exited with, against the job it was handed: every")
			fmt.Fprintln(os.Stderr, "changed path inside its write paths and outside its denied ones, a change set")
			fmt.Fprintln(os.Stderr, "that is not empty, descendants the store carries, and every goal held: PASSING")
			fmt.Fprintln(os.Stderr, "output for a check, the diff and the graph for the rest. Evidence must be newer")
			fmt.Fprintln(os.Stderr, "than this job's declaration; each target keeps its own execution timeout. A job")
			fmt.Fprintf(os.Stderr, "that verifies is recorded %s.\n", types.StatePass)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Exit 1 is a status: the result was read and rejected, and every rule that failed")
			fmt.Fprintln(os.Stderr, "is named. Exit 2 is magus unable to answer: nothing was filed and nothing was")
			fmt.Fprintln(os.Stderr, "piped in, the result would not decode, or the job would not write.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "It checks what is mechanical. Whether the work is GOOD, and whether the job's")
			fmt.Fprintln(os.Stderr, "acceptance criteria are met, stay the reading of whoever forked it.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "--evidence <goal>=<ref> grades a check goal against a run recorded in THIS checkout")
			fmt.Fprintf(os.Stderr, "instead of the one the job was exited with; the job's own check is `%s`.\n", types.PrimaryGoalID)
			fmt.Fprintln(os.Stderr, "It is for a goal that failed where the work was written but passes when run here,")
			fmt.Fprintln(os.Stderr, "such as a build the author's machine could not complete. Repeat it for each goal.")
			fmt.Fprintln(os.Stderr, "The ref is looked up in this checkout only and must be a fresh, passing run of")
			fmt.Fprintln(os.Stderr, "that goal's own check. It is refused for a goal the job does not hold, one that is")
			fmt.Fprintln(os.Stderr, "not a check, and one the job's own evidence already passes. Every changed path")
			fmt.Fprintln(os.Stderr, "the result names must read byte for byte the same here as in the job's checkout,")
			fmt.Fprintln(os.Stderr, "and as the run read them: a run of a clean tree read its recorded revision, so they")
			fmt.Fprintln(os.Stderr, "must read now as they do there; any other run is held to file times, and nothing")
			fmt.Fprintln(os.Stderr, "under or above them may have changed since it started. Only then does the run here")
			fmt.Fprintln(os.Stderr, "cover the work it stands in for. The goals that took a run from")
			fmt.Fprintln(os.Stderr, "here are recorded on the job, beside the runs it was exited with.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "--integration asks whether the work still passes in THIS tree, such as a branch")
			fmt.Fprintln(os.Stderr, "it was merged into: the job's check and check goals are graded against the runs")
			fmt.Fprintln(os.Stderr, "the result's validation.output_ref and verification_evidence name here, and the grade is")
			fmt.Fprintln(os.Stderr, "recorded as the job's integration. Its state does not move, and an ended job is")
			fmt.Fprintln(os.Stderr, "graded too.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Example:")
			fmt.Fprintln(os.Stderr, "  magus job wait refactor/pricing")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if schema {
		fmt.Print(job.ResultSchema)
		return nil
	}
	if len(pos) != 1 {
		return exit.Usagef("magus job wait: requires exactly one job")
	}
	root = resolveRootOrEmpty(root)

	store, err := openJobs(ctx, root, jobOpen{DryRun: globalCfg.DryRun})
	if err != nil {
		return err
	}
	store = store.InWorkspace(jobWorkspace(ctx, root))
	rows, err := store.List()
	if err != nil {
		return err
	}
	if actor := store.Actor(); !actor.Verifies(rows, pos[0]) {
		return fmt.Errorf("magus job wait: this checkout holds the lease on %s, and a holder does not verify its own work,"+
			" exit the job with what you changed and what you ran, and let whoever forked it wait on you", actor.Lease)
	}

	var result *types.JobResult
	if stdin {
		decoded, derr := job.DecodeResult(os.Stdin)
		if derr != nil {
			return exit.Usagef("magus job wait: %s (`%s` prints the schema it must satisfy)", derr, hint.JobWait.With("--schema"))
		}
		result = &decoded
	}
	if integration {
		if result == nil {
			return exit.Usagef("magus job wait: --integration grades runs in this checkout, and only a --stdin result can name them")
		}
		return jobWaitIntegration(ctx, store, root, pos[0], *result)
	}
	if len(evidence) > 0 && (stdin || integration) {
		return exit.Usagef("magus job wait: --evidence starts from the result filed with the job, so it takes neither --stdin nor --integration")
	}
	status, err := job.Wait(ctx, store, pos[0], result, storedAttempts(root), jobObserver(root, graphsByTree(root, newJobGraph(root, true), true)), evidence)
	if err != nil {
		// An ended job that filed nothing still owns its checkout, and its waiter is the one
		// caller left to remove it.
		return errors.Join(exit.Usagef("magus job wait: %s", err), endWorktrees(ctx, os.Stdout, store))
	}
	if rows, err := store.List(); err == nil {
		status.Entries = job.Entries(rows, status.Job)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	// A job's checkout is told of in text alone; the row records it for every format.
	told := io.Discard
	switch opts.Format {
	case outputName:
		err = emitNames(os.Stdout, []string{status.Job})
	case outputText:
		job.PrintStatus(os.Stdout, status)
		pointAtConsole(ctx, status.Job, false)
		told = os.Stdout
	default:
		err = emitFormatted(opts, status)
	}
	err = errors.Join(err, endWorktrees(ctx, told, store))
	if err != nil || status.Verified {
		return err
	}
	return exit.Reported(exit.Failure)
}

// jobWaitIntegration is `job wait --integration`: the job's check goals graded against
// runs recorded in this checkout, with the same exit statuses as a wait.
func jobWaitIntegration(ctx context.Context, store *job.Store, root, id string, result types.JobResult) error {
	if root == "" {
		return errors.New("magus job wait: --integration grades runs recorded in a checkout, and there is no workspace here: run from inside one or pass --root <path>")
	}
	checkout, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	// A merge the driver settled by keeping one side of a generated file leaves the tree
	// stale until the regeneration runs, so a grade read now would pass a tree nobody built.
	owed, err := vcs.OwedRegenerations(ctx, checkout)
	if err != nil {
		return exit.Usagef("magus job wait: %s", err)
	}
	if len(owed) > 0 {
		runs := make([]string, len(owed))
		for i, o := range owed {
			runs[i] = fmt.Sprintf("%s %s", o.Target, o.Project)
		}
		return exit.Usagef("magus job wait: %s owes %d regeneration(s) a merge left: %s; run `%s`, commit what it stages, then wait again",
			checkout, len(owed), strings.Join(runs, ", "), hint.VCSResolve.With())
	}
	grade, err := job.WaitIntegration(ctx, store, id, result, storedAttempts(root), checkout, jobSymbolReader(root, true))
	if err != nil {
		return exit.Usagef("magus job wait: %s", err)
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		err = emitNames(os.Stdout, []string{id})
	case outputText:
		verdict := "fails"
		if grade.Verified {
			verdict = "passes"
		}
		fmt.Printf("integration: %s %s in %s; its state is unchanged\n", id, verdict, checkout)
		job.RenderGates(os.Stdout, types.JobStatus{Job: id, Gates: grade.Gates})
	default:
		err = emitFormatted(opts, grade)
	}
	if err != nil || grade.Verified {
		return err
	}
	return exit.Reported(exit.Failure)
}

// jobWatch follows one job's feed in this terminal, one line per event, until interrupted.
//
// THE POINT IS THAT IT ASKS THE HOLDER NOTHING. The three sources are the guard's trail,
// the job's recorded runs, and the filesystem under the job's declared write paths, and none
// of them needs the worker to cooperate or even to notice. Messaging a worker to ask how it
// is going costs it the turn it was in the middle of.
//
// It reads LOCALLY rather than through the server's WatchActivityEvents, over the same
// internal/job cursors that RPC follows with, so the two cannot disagree about what has
// happened since you last looked. Local because this verb has to work in a checkout with no
// server running, which is the same tree the holder is working in: requiring a server to
// answer "what is that worker doing" would put the question out of reach exactly when
// somebody is at a terminal wondering.
func jobWatch(ctx context.Context, root string, args []string) error {
	pos, err := commandParse("job watch", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job watch <job>")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Follow what a job's holder is doing, one line per event, until interrupted.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Three sources, merged in time order: files changed under the job's declared write")
			fmt.Fprintln(os.Stderr, "paths, tool calls the guard observed under its lease, and the runs magus recorded")
			fmt.Fprintln(os.Stderr, "against it. None of them asks the holder anything, so watching costs it nothing.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintf(os.Stderr, "`%s` grades what it has finished; this shows what it is doing.\n", hint.DescribeJob.With("<job>"))
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Example:")
			fmt.Fprintln(os.Stderr, "  magus job watch refactor/pricing")
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return exit.Usagef("magus job watch: requires exactly one job")
	}
	id := pos[0]
	root = resolveRootOrEmpty(root)
	store, err := openJobs(ctx, root, jobOpen{})
	if err != nil {
		return err
	}
	rows, err := store.List()
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(rows, func(r types.Job) bool { return r.ID == id }) {
		return fmt.Errorf("magus job watch: there is no job %q (run `%s` to see them)", id, hint.LsJobs)
	}
	cacheDir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
	if err != nil {
		return err
	}

	out := os.Stdout
	pointAtConsole(ctx, id, true)
	fmt.Fprintf(out, "watching %s in %s; interrupt to stop\n", id, root)

	// The job plan is re-read per batch rather than captured: a holder releases paths as it
	// goes, and a boundary frozen here would keep attributing a file it gave up.
	plan := func() []types.Job {
		live, lerr := store.List()
		if lerr != nil {
			return nil
		}
		return live
	}
	changes := jobWatchFiles(ctx, out, root, plan)
	cursor := job.CursorAt(job.Ascending(recentTrail(cacheDir)))
	runs := job.NewRunCursor()
	runs.Prime(rows)

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case e, ok := <-changes:
			if !ok {
				changes = nil // the watcher ended; the trail half keeps reporting
				continue
			}
			// A contested path is attributed to nobody and is still this reader's business:
			// their job declared it, somebody wrote it, and which of them did is precisely
			// what nothing can say. Filtering on Job alone hid it.
			if e.Job == id || slices.Contains(e.Contested, id) {
				job.PrintFeedLine(out, e)
			}
		case <-tick.C:
			for _, e := range job.ToolEvents(cursor.Next(job.Ascending(recentTrail(cacheDir)))) {
				if e.Job == id {
					job.PrintFeedLine(out, e)
				}
			}
			for _, e := range runs.Next(plan()) {
				if e.Job == id {
					job.PrintFeedLine(out, e)
				}
			}
		}
	}
}

// jobWatchFiles starts this checkout's own file watcher and returns the attributed changes.
// A watcher that will not start is REPORTED and then done without: the other two sources
// still answer, and a silently missing third is how a feed comes to show a quiet tree.
func jobWatchFiles(ctx context.Context, out io.Writer, root string, plan func() []types.Job) <-chan job.FeedEvent {
	w, err := watch.New(ctx, watch.WithRoot(root), watch.WithIgnore(watch.BuiltinIgnore))
	if err != nil {
		fmt.Fprintf(out, "note: no file watcher here (%s), so this shows tool calls and runs only\n", err)
		return nil
	}
	go func() {
		<-ctx.Done()
		_ = w.Close()
	}()
	return console.NewJobFeed(ctx, w, root, plan).Subscribe(ctx)
}

// recentTrail is what this checkout's activity trail still holds, newest first. An
// unreadable trail reads as empty: a watcher that refused to start because nothing had been
// recorded yet would refuse exactly when a job has only just begun.
func recentTrail(cacheDir string) []trail.Event {
	events, err := trail.ReadRecent(cacheDir, jobWatchWindow)
	if err != nil {
		return nil
	}
	return events
}

// jobWatchWindow bounds one read of the trail. It is a follower's window, not a history:
// every tick re-reads it and the cursor drops what it has already printed, so this only has
// to be wider than one second of a very busy machine.
const jobWatchWindow = 2000

// refusePaused refuses work under job id while a person has it paused.
func refusePaused(store *job.Store, id string) error {
	rows, err := store.List()
	if err != nil {
		return fmt.Errorf("magus job exec: read whether %s is paused: %w", id, err)
	}
	if i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id }); i >= 0 && rows[i].PausedAt(time.Now().Unix()) {
		return types.ExitError{Code: cache.ExitCodeMachineBusy, Err: fmt.Errorf("magus job exec: %s", job.PausedLine(rows[i]))}
	}
	return nil
}

// jobPause is `magus job pause` and `magus job resume`, which verb says.
func jobPause(ctx context.Context, root, verb string, args []string) error {
	spec := jobCommandSpec()
	child := spec.Children[slices.IndexFunc(spec.Children, func(c command.Command) bool { return c.Name == verb })]
	var reason, until string
	ids, err := commandParse("job "+verb, args, func(fs *flag.FlagSet) {
		if verb == "pause" {
			fs.StringVar(&reason, "reason", "", "")
			fs.StringVar(&until, "until", "", "")
		}
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: "+child.Usage)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, cmp.Or(child.Description, child.Short))
		}
	})
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return exit.Usagef("magus job %s: requires at least one job (`%s` lists them)", verb, hint.LsJobs)
	}
	lapse, err := types.ParsePauseUntil(until, time.Now())
	if err != nil {
		return exit.Usagef("magus job pause: %v", err)
	}
	if verb == "pause" && strings.TrimSpace(reason) == "" {
		return exit.Usagef("magus job pause: requires --reason, which the holder's refusal and the row show")
	}
	store, err := openJobs(ctx, resolveRootOrEmpty(root), jobOpen{})
	if err != nil {
		return err
	}
	var rows []types.Job
	if verb == "resume" {
		rows, err = store.Resume(ctx, ids...)
	} else {
		rows, err = store.Pause(ctx, reason, lapse, ids...)
	}
	if err != nil {
		return fmt.Errorf("magus job %s: %w", verb, err)
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		return emitNames(os.Stdout, ids)
	case outputText:
		for _, row := range rows {
			switch {
			case row.Paused == 0:
				fmt.Printf("resumed %s\n", row.ID)
			case row.PausedUntil != 0:
				fmt.Printf("paused %s until %s; its holder's next magus run is refused until then or `%s job resume %s`\n",
					row.ID, time.Unix(row.PausedUntil, 0).UTC().Format(time.RFC3339), hint.BinaryName(), row.ID)
			default:
				fmt.Printf("paused %s; its holder's next magus run is refused until `%s job resume %s`\n", row.ID, hint.BinaryName(), row.ID)
			}
		}
		return nil
	default:
		return emitFormatted(opts, rows)
	}
}

// jobDelete is `magus job rm <job>`: take one row out of the plan.
//
// Named rm rather than delete because that is the verb a person types for this everywhere
// else, and the commands it sits beside (fork, exec, exit, wait) are the shell's own
// vocabulary.
//
// It is NOT how a job ends. `job exit` records what happened and leaves the row as the
// account of it; this removes a row that should not exist -- a demo, a typo, a plan
// abandoned before it began. A terminal row is refused without --force for exactly that
// reason: deleting the record of work that ran destroys the only account of it.
func jobDelete(ctx context.Context, root string, args []string) error {
	var force bool
	pos, err := commandParse("job rm", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&force, "force", false, "Remove a row that already ended, destroying the record of what happened")
		command.NegatedBoolVar(fs, &force, "force")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job rm <job> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Remove ONE job from the plan. The rows it leaves alone are the difference from")
			fmt.Fprintf(os.Stderr, "`%s` (magus\\workspace().clearJobs), which drops every row in the repository.\n", hint.ToolClient)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintf(os.Stderr, "This is not how a job ENDS. `%s` records what happened and\n", hint.JobExit)
			fmt.Fprintln(os.Stderr, "leaves the row as the account of it; rm is for a row that should never have been")
			fmt.Fprintln(os.Stderr, "written. A row that already ended is refused unless --force, because deleting it")
			fmt.Fprintln(os.Stderr, "destroys the only record that the work ran.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Rows left over from finished work want `magus job prune`, which ENDS every job")
			fmt.Fprintln(os.Stderr, "nobody is working and keeps each row as the record.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The dropped rows are archived beside the plan first, so this is recoverable.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Example:")
			fmt.Fprintln(os.Stderr, "  magus job rm refactor/pricing-draft")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return exit.Usagef("magus job rm: requires exactly one job")
	}
	store, err := openJobs(ctx, resolveRootOrEmpty(root), jobOpen{})
	if err != nil {
		return err
	}
	dropped, err := store.Delete(ctx, pos[0], force)
	if err != nil {
		return exit.Usagef("magus job rm: %s", err)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		return emitNames(os.Stdout, []string{dropped.ID})
	case outputText:
		fmt.Printf("removed %s (%s); the plan it was part of is archived beside it\n", dropped.ID, orDash(string(dropped.State)))
		return nil
	default:
		return emitFormatted(opts, dropped)
	}
}

// jobApply is `magus job apply`. Each record is a job's whole spec,
// upserted in order. Nothing magus records about the job moves.
// A new id creates the job.
func jobApply(ctx context.Context, root string, args []string) error {
	var flags *flaggen.JobApplyFlags
	pos, err := commandParse("job apply", args, func(fs *flag.FlagSet) {
		flags = flaggen.BindJobApply(fs)
		fs.Usage = func() { printRegisteredHelp(fs) }
	})
	if err != nil {
		return err
	}
	addPaths, err := job.ClaimPaths(flaggen.FlagJobApplyAddWritePath, flags.AddWritePath)
	if err != nil {
		return err
	}
	releasePaths, err := job.ClaimPaths(flaggen.FlagJobApplyReleaseWritePath, flags.ReleaseWritePath)
	if err != nil {
		return err
	}
	editing := len(addPaths) > 0 || len(releasePaths) > 0
	var (
		records []types.Declaration
		store   *job.Store
	)
	root = resolveRootOrEmpty(root)
	switch {
	case editing && (flags.F != "" || flags.Stdin):
		return exit.Usagef("magus job apply: --add-write-path and --release-write-path edit the job they name, and -f or --stdin applies whole records: name one or the other")
	case flags.F != "" && flags.Stdin:
		return exit.Usagef("magus job apply: -f reads a file and --stdin reads stdin: name one")
	case flags.F == "-":
		return exit.Usagef("magus job apply: stdin is --stdin, as on job fork, not -f -")
	case editing && len(pos) != 1:
		return exit.Usagef("magus job apply: --add-write-path and --release-write-path edit one job: name its id as the one argument")
	case !editing && len(pos) > 0:
		return exit.Usagef("magus job apply: the records name their jobs, so %q is one argument too many", pos[0])
	case !editing:
		var in io.Reader
		switch {
		case flags.Stdin:
			in = os.Stdin
		case flags.F == "":
			return exit.Usagef("magus job apply: name the records with -f <file>, or --stdin")
		default:
			f, err := os.Open(flags.F)
			if err != nil {
				return fmt.Errorf("magus job apply: %w", err)
			}
			defer f.Close()
			in = f
		}
		if records, err = job.DecodeDeclarations(in); err != nil {
			return exit.Usagef("magus job apply: %s (`%s` prints the schema each record must satisfy)", err, hint.JobFork.With("--schema"))
		}
		if slices.ContainsFunc(records, func(r types.Declaration) bool { return len(r.Claim) > 0 }) {
			return applyClaim(ctx, root, records)
		}
	}
	// A dry run assigns no tier, so it runs no matrix.
	store, err = openJobs(ctx, root, jobOpen{DryRun: globalCfg.DryRun, ModelTiers: !globalCfg.DryRun})
	if err != nil {
		return err
	}
	if editing {
		edited, err := job.WidenedDeclaration(store, pos[0], addPaths)
		if err == nil {
			edited, err = job.NarrowedDeclaration(edited, pos[0], releasePaths)
		}
		if err != nil {
			return fmt.Errorf("magus job apply: %w", err)
		}
		records = []types.Declaration{edited}
	}
	jobWS := jobWorkspace(ctx, root)
	store = store.InWorkspace(jobWS)
	if slices.ContainsFunc(records, types.Declaration.Settles) {
		settled, err := store.Settle(ctx, records, globalCfg.DryRun)
		if err != nil {
			return fmt.Errorf("magus job apply: %w", err)
		}
		opts, err := outputOptionsOrDefault()
		if err != nil {
			return err
		}
		if opts.Format != outputText {
			names := make([]string, len(settled))
			for i, row := range settled {
				names[i] = row.ID
			}
			return emitRecord(opts, settled, names)
		}
		for _, row := range settled {
			fmt.Printf("settled %s: %d deferred item(s) answered, left_unmerged %q\n", row.ID, len(row.Deferred), row.LeftUnmerged)
		}
		return nil
	}
	checkpoint := func() string {
		token, _ := checkoutBaseToken(ctx, root)
		return token
	}
	graph := newJobGraph(root, true)
	for i := range records {
		if records[i], err = job.StackedHead(ctx, root, records[i], configuredVCS()); err != nil {
			return fmt.Errorf("magus job apply: %w", err)
		}
		var notes []string
		records[i], notes = evidence.ExtendedFootprint(ctx, store, graph, root, records[i], jobWS)
		for _, n := range notes {
			slog.InfoContext(ctx, fmt.Sprintf("%s: %s", records[i].ID, n), attr.Notice("note"))
		}
	}
	if flags.Fix {
		records = fixApply(ctx, store, records, evidence.GraphSymbolReader(graph.Load), checkpoint)
	}
	applied, err := job.Apply(ctx, store, records, globalCfg.Jobs, evidence.GraphSymbolReader(graph.Load), checkpoint, globalCfg.DryRun)
	if err != nil {
		ids := make([]string, len(records))
		for i, rec := range records {
			ids[i] = rec.ID
		}
		validations, ok := job.ApplyValidations(err, ids)
		if !ok {
			return fmt.Errorf("magus job apply: %w", err)
		}
		return job.RefuseApply(validations, err)
	}
	if jobWS.Unavailable != "" && slices.ContainsFunc(applied, func(a job.Applied) bool { return !a.Next.ReadOnly && len(a.Next.Paths.Write) > 0 }) {
		warnWorkspaceUnloaded(ctx, errors.New(jobWS.Unavailable))
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format == outputName {
		ids := make([]string, len(applied))
		for i, a := range applied {
			ids[i] = a.Next.ID
		}
		return emitNames(os.Stdout, ids)
	}
	claims := applyClaimNotes(ctx, store, applied)
	if opts.Format == outputText {
		job.PrintApply(os.Stdout, applied, claims, globalCfg.DryRun)
		if len(applied) == 1 && !globalCfg.DryRun {
			pointAtConsole(ctx, applied[0].Next.ID, false)
		}
		return nil
	}
	report := job.ApplyReport{DryRun: globalCfg.DryRun}
	for _, a := range applied {
		report.Jobs = append(report.Jobs, job.ApplyEntry{Job: a.Next, Created: a.Created, Changed: a.Changed, ClaimNotes: claims[a.Next.ID]})
	}
	return emitFormatted(opts, report)
}

// applyClaimNotes is job.ForkNotes for each applied row, keyed by job id, against the plan
// as the apply leaves it, so two rows of one apply note each other. A dry run's rows are
// read as they would be written.
func applyClaimNotes(ctx context.Context, store *job.Store, applied []job.Applied) map[string][]string {
	rows, err := store.List()
	if err != nil {
		return nil
	}
	for _, a := range applied {
		if i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == a.Next.ID }); i >= 0 {
			rows[i] = a.Next
		} else {
			rows = append(rows, a.Next)
		}
	}
	out := map[string][]string{}
	for _, a := range applied {
		notes := job.ForkNotes(ctx, store, rows, a.Next)
		if notes = append(notes, job.ReleasedUnderPattern(a.Next)...); len(notes) > 0 {
			out[a.Next.ID] = notes
		}
	}
	return out
}

// jobPrune is `magus job prune`: end every job nobody is working, the way `job exit`
// abandons one, so rows left over from finished work stop refusing new forks.
//
// --all is `docker system prune -a`'s knob and `ls jobs --all`'s word: the wider set.
func jobPrune(ctx context.Context, root string, args []string) error {
	var all bool
	pos, err := commandParse("job prune", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&all, "all", false, "Also end a job a holder took and nobody touched within jobs.stale_after")
		command.NegatedBoolVar(fs, &all, "all")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job prune [--all] [<job>...] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintf(os.Stderr, "End, as %s, every job the store can show nobody is working:\n", types.StateNoReturn)
			fmt.Fprintf(os.Stderr, "exited and never collected with `%s`, overdue, orphaned by an\n", hint.JobWait)
			fmt.Fprintln(os.Stderr, "ancestor that ended, or declared and never taken past jobs.stale_after. Each row")
			fmt.Fprintf(os.Stderr, "keeps its end_reason, as `%s` would leave it.\n", hint.JobExit)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A job a holder took and touched within jobs.stale_after is never ended, nor is")
			fmt.Fprintln(os.Stderr, "its parent. --all also ends a taken job nobody touched within jobs.stale_after.")
			fmt.Fprintln(os.Stderr, "A job that would end only because an ancestor this prune ends does is named and")
			fmt.Fprintln(os.Stderr, "the prune refused, until each such <job> is restated as an argument; one holding")
			fmt.Fprintln(os.Stderr, "uncommitted work or a running check also waits on a person to dispose the")
			fmt.Fprintln(os.Stderr, "attention request the refusal names.")
			fmt.Fprintln(os.Stderr, "The global --dry-run lists what would end and ends nothing.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Example:")
			fmt.Fprintln(os.Stderr, "  magus job prune --dry-run")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	store, err := openJobs(ctx, resolveRootOrEmpty(root), jobOpen{DryRun: globalCfg.DryRun})
	if err != nil {
		return err
	}
	ended, err := store.Prune(ctx, job.PruneOptions{All: all, DryRun: globalCfg.DryRun}, pos...)
	if err != nil {
		return fmt.Errorf("magus job prune: %w", err)
	}
	endedIDs := make([]string, len(ended))
	for i, e := range ended {
		endedIDs[i] = e.ID
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		return emitNames(os.Stdout, endedIDs)
	case outputText:
		job.PrintPruned(os.Stdout, ended, globalCfg.DryRun)
		return endWorktrees(ctx, os.Stdout, store, endedIDs...)
	default:
		if ended == nil {
			ended = []job.Ending{}
		}
		return emitFormatted(opts, job.PruneReport{Ended: ended, DryRun: globalCfg.DryRun})
	}
}

// outputsIn resolves a ref against the output store of the checkout at root. That
// checkout's own magus.yaml places its cache dir, and loadMagus cannot open a second root.
func outputsIn(root string) job.AttemptResolver {
	return func(_ context.Context, ref string) (types.JobAttempt, error) {
		if strings.TrimSpace(ref) == "" {
			return types.JobAttempt{}, nil
		}
		dir, err := magus.ResolveCacheDir(root, cliOptions()...)
		if err != nil {
			return types.JobAttempt{}, err
		}
		return attemptIn(dir, ref)
	}
}

// unmergedReport is nil for the format that prints only ids, which cannot show
// a report that costs up to the read's whole budget.
func unmergedReport(ctx context.Context, root string, rows []types.Job, tries types.JobAttempts, format output.Format) *types.JobUnmergedReport {
	if format == outputName {
		return nil
	}
	report := job.Unmerged(ctx, root, rows, tries)
	return &report
}

// attemptIn is what the output store under cacheDir recorded about the run behind ref,
// and only that store: an empty attempt for a ref it does not hold.
func attemptIn(cacheDir, ref string) (types.JobAttempt, error) {
	switch d, err := cache.NewOutputStore(cacheDir).DescriptorByRef(ref); {
	case err == nil:
		return d.JobAttempt(ref), nil
	case errors.Is(err, fs.ErrNotExist):
		return types.JobAttempt{}, nil
	default:
		return types.JobAttempt{}, err
	}
}

// joinInflight joins list to the queue snapshot as the caller sees it: the job it acts
// under, and the forge login this process has already learned.
func joinInflight(ctx context.Context, root string, store *job.Store, list types.JobList) (types.JobList, job.Identity, error) {
	me := job.Identity{Lease: store.Actor().Lease, Login: bindings.ReviewViewer()}
	if root == "" {
		return list, me, nil
	}
	joined, err := queue.JoinInflight(ctx, root, list, me)
	return joined, me, err
}

// checkoutReadsRow refuses a checkout of magus whose tree builds a binary
// that cannot read the row this fork writes. A rebuild there leaves a
// checkout whose ./magus cannot read its own job.
func checkoutReadsRow(root string) error {
	reads, err := contract.ReadsFormat(root, types.JobAPIVersion)
	switch {
	case errors.Is(err, contract.ErrNotCarried):
		return nil
	case err != nil:
		return err
	case !reads:
		return fmt.Errorf("the checkout %s carries a tree older than the job store, so a magus built there reads no %s rows: "+
			"move it to the job's checkpoint, or fork the job into a new checkout", root, types.JobAPIVersion)
	}
	return nil
}

// readsTree reports whether the magus at bin was built from the contract
// the checkout at root carries. A tree that carries none takes any binary.
func readsTree(ctx context.Context, bin, root string) bool {
	tree, err := contract.Tree(root)
	if errors.Is(err, contract.ErrNotCarried) {
		return bin != ""
	}
	return err == nil && job.BinaryContract(ctx, bin) == tree
}

// storedAttempts resolves a ref against the output store of THIS checkout, root, alone:
// which command produced the run, and whether it failed. An empty ref, and one that aged
// out of the cache, both report MISSING rather than an error: nobody can reopen either,
// and that is the fact verification turns on.
//
// Never another checkout's store, which `magus query output` falls back to: --evidence and
// --integration certify runs made here, and a ref the holder recorded would otherwise
// resolve as one. The fallback a filed result needs is the job's own checkouts, which
// [job.Exit] and [job.Wait] add through Location.Outputs.
//
// The DESCRIPTOR, not the bytes. Verification reads the run's identity and its exit status,
// both of which are metadata, and a captured log is as large as the target was noisy.
func storedAttempts(root string) job.AttemptResolver {
	return func(_ context.Context, ref string) (types.JobAttempt, error) {
		if strings.TrimSpace(ref) == "" {
			return types.JobAttempt{}, nil
		}
		dir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
		if err != nil {
			return types.JobAttempt{}, err
		}
		return attemptIn(dir, ref)
	}
}

// configuredVCS is the VCS selection magus.yaml and the flags made.
func configuredVCS() types.VCSOptions {
	return types.VCSOptions{Enabled: globalCfg.VCS.Enabled, Name: globalCfg.VCS.Name, BaseRef: globalCfg.VCS.BaseRef}
}

// jobWorkspace is the workspace at root as a job record's validation reads it, loaded once.
func jobWorkspace(ctx context.Context, root string) job.Workspace {
	var loaded job.LoadedWorkspace
	m, err := loadMagus(ctx, root)
	if err == nil {
		loaded = m
	}
	return job.WorkspaceOf(ctx, root, loaded, err, func(err error) string {
		running, pathErr := selfupdate.RunningBinary()
		if pathErr != nil {
			return err.Error()
		}
		return workspaceLoadFailure(err, root, running)
	})
}

// workspaceLoadFailure is err's text, naming root's own ./magus when running, a binary
// too old for root, is not that one.
func workspaceLoadFailure(err error, root, running string) string {
	return job.LoadFailureText(err, guard.CheckoutBinary(root, running))
}

// diskProbe is the cache package's disk probes, which internal/job cannot import.
var diskProbe = job.Disk{
	Below: cache.DiskBelow,
	FullCause: func(output, dir string) string {
		return cache.DiskFullCause(output, dir, cache.DiskFullFloorBytes)
	},
	ReclaimSnippet: cache.ReclaimNowSnippet,
}

// writtenInCheckout reports which of paths the workspace at checkout wrote itself: its
// generated outputs, its maintained files and the tool rows a wired harness installs.
func writtenInCheckout(ctx context.Context, checkout string, paths []string) ([]string, error) {
	ws, err := magus.Inspect(ctx, checkout, append(cliOptions(), workspace.WithLoadedConfig(globalCfg), magus.WithVersion(version))...)
	if err != nil {
		return nil, job.UnloadableMagusfile(err)
	}
	files, err := ws.ClassifyFiles(ctx, paths)
	if err != nil {
		return nil, err
	}
	wired, err := workspace.WiredHarnesses(ws)
	if err != nil {
		return nil, err
	}
	hostCtx := agent.ContextWithWiredHarnesses(ctx, wired)
	var toolRows []types.HarnessTools
	for _, id := range wired {
		d, _, err := agent.LoadHarness(hostCtx, ws.Root(), id)
		if err != nil {
			return nil, fmt.Errorf("loading wired harness %s for the tool rows it installs: %w", id, err)
		}
		if d.Tools != nil {
			toolRows = append(toolRows, *d.Tools)
		}
	}
	var out []string
	for _, f := range files {
		installed := slices.ContainsFunc(toolRows, func(t types.HarnessTools) bool { return t.Holds(f.Path) })
		if installed || f.Role == diff.RoleOutput || f.Role == diff.RoleMaintained {
			out = append(out, f.Path)
		}
	}
	return out, nil
}

// endWorktrees is [job.EndCheckouts] for every ended job.
func endWorktrees(ctx context.Context, out io.Writer, store *job.Store, ended ...string) error {
	return job.EndCheckouts(ctx, out, store, writtenInCheckout, globalCfg.DryRun, false, ended...)
}

// endNamedWorktrees is [endWorktrees] for the jobs in ended alone, as `job exit` ends them.
func endNamedWorktrees(ctx context.Context, out io.Writer, store *job.Store, ended ...string) error {
	return job.EndCheckouts(ctx, out, store, writtenInCheckout, globalCfg.DryRun, true, ended...)
}
