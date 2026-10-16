package main

import (
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
	"text/tabwriter"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/queue"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// jobCmd implements `magus job`, the POSIX child lifecycle over delegated work: fork
// declares a job, exec takes the lease on it in this checkout, exit returns it with its
// result, and wait verifies that result. Listing is `magus ls jobs` and the terms are
// `magus describe job`, because enumerating and defining are those verbs' work everywhere
// else in this CLI.
//
// BOTH CHANNELS WRITE, and the store is what keeps the book honest. The client MCP
// tool (magus\job) is the agent's channel and this verb is the person's; they reach the same store and
// the same rules, so a job still has one author (internal/job.authorizeRow enforces it)
// without a capability existing for agents that a person does not have.
func jobCmd(ctx context.Context, root string, args []string) error {
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
	default:
		return exit.Usagef("magus job: unknown subcommand %q (want fork, apply, exec, exit, wait, watch, run, rm or prune; `%s` lists what is in flight)", args[0], hint.LsJobs)
	}
}

func jobUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus job <fork|apply|exec|exit|wait|run|rm|prune> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Delegated work, on the shell's own lifecycle. A job is the unit of work; a lease is")
	fmt.Fprintln(os.Stderr, "the grant one holder has on it: the paths it may write and read, plus the one check it runs.")
	fmt.Fprintln(os.Stderr, "A job is not a run: `magus run` executes a target with no job involved, while a job's")
	fmt.Fprintln(os.Stderr, "check and the server's maintenance each cause runs.")
	fmt.Fprintln(os.Stderr, "Kept per repository, so every worktree and clone reads one set of jobs.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  fork  declare a job, from flags or a JSON record on stdin")
	fmt.Fprintln(os.Stderr, "  apply upsert jobs' specs from -f <file|->, keeping each job's state")
	fmt.Fprintln(os.Stderr, "  exec  take the lease on a job here, and record the base this checkout landed on")
	fmt.Fprintln(os.Stderr, "  exit  return a job with its result, or abandon it")
	fmt.Fprintln(os.Stderr, "  wait  collect a returned job's result and verify it")
	fmt.Fprintln(os.Stderr, "  watch follow what its holder is doing, until interrupted")
	fmt.Fprintln(os.Stderr, "  run   submit one of the server's own jobs and return")
	fmt.Fprintln(os.Stderr, "  rm    remove one job from the plan; a row that already ended needs --force")
	fmt.Fprintln(os.Stderr, "  prune end every job nobody is working, each with the reason; --all adds idle taken ones")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "`"+hint.LsJobs.String()+"` lists every job in flight, and `"+hint.DescribeJob.With("<job>")+"` prints one job's terms.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "One author per job, enforced by the store: a session holding a lease may release")
	fmt.Fprintln(os.Stderr, "paths and end its own job, and nothing else. "+hint.ToolClient.String()+" (magus\\job) is the")
	fmt.Fprintln(os.Stderr, "same store through an agent's channel.")
}

// pointAtConsole names where to WATCH a job (the Jobs view for an empty id), the one way
// to see how a worker is doing that leaves it alone. With no server it names no link: a
// page that never loads cannot say whether nothing is listening or the console is broken,
// and the record carries no next command, so a quiet display has nothing to print.
func pointAtConsole(ctx context.Context, id string) {
	if globalCfg.Console.Enabled != nil && !*globalCfg.Console.Enabled {
		return
	}
	serving, serverVersion := probeConsoleServer()
	if !serving {
		slog.InfoContext(ctx, "nothing is serving it; `"+hint.ServerStart.String()+"` to watch this job without interrupting its holder",
			attr.Notice("console"))
		return
	}
	host := mcpAddrString()
	link := console.JobLink(host, id)
	if id == "" {
		link = console.Link(console.LinkOpts{Host: host, App: console.JobApp})
	}
	slog.LogAttrs(ctx, slog.LevelInfo, link, attr.Notice("console"), attr.Next(console.OpenCommand(link)))
	if skew := consoleSkew(serverVersion, version); skew != "" {
		slog.WarnContext(ctx, skew, attr.Notice("console"))
	}
}

// consoleSkew names a server running a different build from this binary, or "" when they
// match or either side is unstamped. The console that server serves is its own build, so
// what the link opens may not know what this binary just wrote.
func consoleSkew(serverVersion, cliVersion string) string {
	if serverVersion == "" || cliVersion == "" || serverVersion == cliVersion {
		return ""
	}
	return fmt.Sprintf("that console is from server %s, this binary is %s; `%s && %s` serves this build",
		serverVersion, cliVersion, hint.ServerStop, hint.ServerStart)
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
	}
	if o.ModelTiers && root != "" {
		m, err := loadMagus(ctx, root)
		if err != nil {
			slog.WarnContext(ctx, "the workspace did not load, so no model tier was assigned and no tier limit was checked; a row written now carries no tier until its model changes",
				attr.Notice("unchecked"), attr.Error(err))
			return job.NewStore(loc), nil
		}
		loc.ModelTiers, loc.ModelTierMatrix = m.ModelTiers(ctx), m.ModelTierMatrix()
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

// newCheckGrader grades a declared check against the guard's rules for the workspace at the
// root it is handed. Each root's rules load on the first check graded there, so a fork
// that declares none pays nothing, and are reused until a file they were read from
// changes: a grader lives as long as the Store, or the daemon, it was made for, and
// grading against rules an edit has since replaced would admit or refuse the wrong check.
// runCLI installs one with job.SetCheckGrader, and it is the only grader: every fork door
// (`magus job fork`, magus\job.put over MCP, the daemon) opens a Store that names none.
func newCheckGrader() job.CheckGrader {
	var (
		mu     sync.Mutex
		loaded = map[string]gradingRules{}
	)
	currentRules := func(ctx context.Context, root string) guard.Dependencies {
		mu.Lock()
		defer mu.Unlock()
		if g, ok := loaded[root]; ok && g.unchanged() {
			return g.deps
		}
		// The rules outlive this grade, so this caller's cancellation must not decide them.
		g := loadGradingRules(context.WithoutCancel(ctx), root)
		loaded[root] = g
		return g.deps
	}
	return func(ctx context.Context, root string, row types.Job) job.CheckVerdict {
		verdict := gradeCheck(ctx, currentRules(ctx, root), root, row)
		if verdict.NotGraded != "" {
			slog.WarnContext(ctx, "job "+row.ID+"'s check was not graded against the guard",
				attr.Notice(""), attr.Why(verdict.NotGraded), attr.Component("magus job"))
		}
		return verdict
	}
}

// gradingRules is a workspace's guard dependencies and the files its rules were read from.
type gradingRules struct {
	deps guard.Dependencies
	// sources are the files the load read, with the id of the bytes it saw. Empty when no
	// rules loaded: nothing says which file to watch, so the next grade loads again.
	sources []interp.SourceFile
}

func loadGradingRules(ctx context.Context, root string) gradingRules {
	deps, rules := loadGuardDependencies(ctx, root)
	if rules == nil {
		return gradingRules{deps: deps}
	}
	return gradingRules{deps: deps, sources: rules.Policy().Sources}
}

// unchanged reports whether every file the rules were read from still holds the bytes the
// load saw. A file that cannot be read counts as changed, so the next load decides.
func (g gradingRules) unchanged() bool {
	if len(g.sources) == 0 {
		return false
	}
	for _, s := range g.sources {
		data, err := os.ReadFile(s.Path)
		if err != nil || interp.ContentID(data) != s.ContentID {
			return false
		}
	}
	return true
}

// ownBinaryPrefix is how a holder types a check: with the ./magus of its own checkout,
// not whichever binary the PATH holds.
const ownBinaryPrefix = "./"

// gradeCheck judges row's check as the guard would judge its holder typing it in the
// checkout at root, with the row standing in for a lease the store does not carry yet.
//
// Only a deny of the check itself refuses. A deny that holds just as well for the
// compiling check describes the binary or the workspace (a stale hook binary, rules that
// would not load) and not the command, so it is not graded and the verdict's NotGraded
// says why: a refusal has to come from a verdict on the check.
func gradeCheck(ctx context.Context, deps guard.Dependencies, root string, row types.Job) job.CheckVerdict {
	if deps.LoadFailure != nil {
		return job.CheckVerdict{NotGraded: "the workspace's rules did not load: " + deps.LoadFailure.Error()}
	}
	judge := func(pending types.Job) types.GuardJudgment {
		return guard.Judge(ctx, deps, guard.Request{
			Input:      ownBinaryPrefix + pending.Check.String(),
			DryRun:     true,
			Lease:      pending.ID,
			PendingRow: &pending,
			Dir:        root,
		})
	}
	declared := judge(row)
	if declared.Decision != string(types.GuardDeny) {
		return job.CheckVerdict{}
	}
	baseline := row
	compiling := job.CompilingCheck()
	baseline.Check = &compiling
	if baselineVerdict := judge(baseline); baselineVerdict.Decision == string(types.GuardDeny) {
		first, _, _ := strings.Cut(strings.TrimSpace(baselineVerdict.Reason), "\n")
		return job.CheckVerdict{NotGraded: "the guard denies this worker `" + compiling.String() + "` as well, so the deny describes the binary or the workspace and not the check: " + first}
	}
	return job.CheckVerdict{Denied: true, Rule: declared.Rule, Reason: declared.Reason}
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

// lsJobs is `magus ls jobs`: every live job this repository carries, whoever holds it.
func lsJobs(root string, args []string) error {
	var all bool
	rest, err := cmdParse("ls jobs", args, func(fs *flag.FlagSet) {
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
	// The store's report, not the bare rows: magus\job.list and JobService's ListJobs
	// build the same one, so the three doors cannot disagree about a row.
	list, err := store.Report(ctx, time.Now().Unix(), globalCfg.Jobs.StaleAfter)
	if err != nil {
		return err
	}
	list, me, err := joinInflight(ctx, root, store, list)
	if err != nil {
		return err
	}
	if !all {
		var ended int
		list, ended = withoutEnded(list)
		if ended > 0 {
			slog.InfoContext(ctx, fmt.Sprintf("%d ended %s left out; `%s` lists them", ended, plural(ended, "job", "jobs"), hint.LsJobs.With("--all")),
				attr.Notice("jobs-ended"))
		}
	}
	// Nothing names a caller with no lease and no login, so none of the changes is theirs
	// and the list is everyone's.
	all = all || !me.Known()

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		ids := make([]string, len(list.Jobs))
		for i, one := range list.Jobs {
			ids[i] = one.ID
		}
		return emitNames(os.Stdout, ids)
	case outputText:
		printJobTree(os.Stdout, list)
		fmt.Fprintln(os.Stdout)
		printInflight(os.Stdout, list, all)
		pointAtConsole(context.Background(), "")
		return nil
	default:
		list.Changes = inflightScope(list.Changes, all)
		return emitFormatted(opts, list)
	}
}

// withoutEnded is list less the rows that ended, and how many that was. The reports derived
// from the rows (overlaps, blocked, stale) name live rows only, so only the read-only
// section can name an ended one.
func withoutEnded(list types.JobList) (types.JobList, int) {
	ended := map[string]bool{}
	list.Jobs = slices.DeleteFunc(slices.Clone(list.Jobs), func(row types.Job) bool {
		if row.State.Terminal() {
			ended[row.ID] = true
		}
		return ended[row.ID]
	})
	list.ReadOnly = slices.DeleteFunc(slices.Clone(list.ReadOnly), func(r types.JobReadOnly) bool { return ended[r.Job] })
	return list, len(ended)
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

// inflightScope keeps the caller's changes, or every change when all is set. Never nil,
// so an empty scope encodes as [].
func inflightScope(changes []types.InflightChange, all bool) []types.InflightChange {
	if all {
		return append([]types.InflightChange{}, changes...)
	}
	mine := []types.InflightChange{}
	for _, c := range changes {
		if c.Mine {
			mine = append(mine, c)
		}
	}
	return mine
}

// printInflight renders the in-flight changes grouped by whose turn each is, the ones
// waiting on the reader first. Unless all is set it lists the reader's changes and
// counts the rest on one line.
func printInflight(out io.Writer, list types.JobList, all bool) {
	f := list.Fetched
	if f == nil {
		fmt.Fprintf(out, "in flight: never fetched; `%s queue ls --provider <provider> --base <branch>` reads the open changes\n", hint.BinaryName())
		return
	}
	at := f.Base
	if f.Tip != "" {
		at += " at " + shortRev(f.Tip)
	}
	fmt.Fprintf(out, "in flight on %s, as of %s (%s, %s, %dms)\n",
		at, time.Unix(f.At, 0).UTC().Format("2006-01-02 15:04 UTC"), f.Provider, orDash(f.Host), f.ElapsedMS)

	shown := inflightScope(list.Changes, all)
	partitions := 0
	for _, c := range list.Changes {
		partitions = max(partitions, c.Partition)
	}
	needs := "needs me"
	if all {
		needs = "needs the author"
	}
	groups := []struct {
		title     string
		attention types.InflightAttention
	}{
		{needs, types.AttentionAuthor},
		{"waiting on review", types.AttentionReview},
		{"queued, in plan order", types.AttentionQueue},
		{"not queued", types.AttentionNone},
	}
	for _, g := range groups {
		var rows []types.InflightChange
		for _, c := range shown {
			if c.Attention == g.attention {
				rows = append(rows, c)
			}
		}
		if len(rows) == 0 {
			continue
		}
		if g.attention == types.AttentionQueue {
			slices.SortStableFunc(rows, queueOrder)
		}
		fmt.Fprintf(out, "%s (%d)\n", g.title, len(rows))
		var table strings.Builder
		w := tabwriter.NewWriter(&table, 0, 0, 2, ' ', 0)
		for _, c := range rows {
			fmt.Fprintf(w, "  #%s\t%s\t%s\t%s\n", c.ID, clipTitle(c.Title, 48), inflightDetail(c, partitions), inflightJobs(c.Jobs))
		}
		_ = w.Flush()
		// An empty last column leaves the padding before it at the end of the line.
		for line := range strings.Lines(table.String()) {
			fmt.Fprintln(out, strings.TrimRight(line, " \n"))
		}
	}
	if len(list.Unproposed) > 0 {
		fmt.Fprintf(out, "jobs without a change (%d)\n", len(list.Unproposed))
		for _, id := range list.Unproposed {
			fmt.Fprintf(out, "  %s\n", id)
		}
	}
	switch {
	case len(shown) > 0 || len(list.Unproposed) > 0:
	case all:
		fmt.Fprintln(out, "nothing in flight")
	default:
		fmt.Fprintln(out, "nothing of yours in flight")
	}
	if !all {
		printInflightOthers(out, list.Changes)
	}
	fmt.Fprintf(out, "refetch: %s queue ls --provider %s --base %s\n", hint.BinaryName(), f.Provider, f.Base)
}

// printInflightOthers counts the changes that are not the reader's on one line.
func printInflightOthers(out io.Writer, changes []types.InflightChange) {
	counts := map[types.InflightAttention]int{}
	for _, c := range changes {
		if !c.Mine {
			counts[c.Attention]++
		}
	}
	var parts []string
	for _, a := range []struct {
		attention types.InflightAttention
		word      string
	}{
		{types.AttentionQueue, "queued"},
		{types.AttentionAuthor, "kicked back"},
		{types.AttentionReview, "waiting on review"},
		{types.AttentionNone, "not queued"},
	} {
		if n := counts[a.attention]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, a.word))
		}
	}
	if len(parts) > 0 {
		fmt.Fprintf(out, "others: %s\n", strings.Join(parts, ", "))
	}
}

// queueOrder puts planned changes first, by partition then position, and keeps the
// provider's order among the rest.
func queueOrder(a, b types.InflightChange) int {
	switch {
	case a.Position == 0 && b.Position == 0:
		return 0
	case a.Position == 0:
		return 1
	case b.Position == 0:
		return -1
	case a.Partition != b.Partition:
		return a.Partition - b.Partition
	}
	return a.Position - b.Position
}

// inflightDetail is why a change is where it is: its place in the plan, and the
// verdict's code and reason or the mark it shows.
func inflightDetail(c types.InflightChange, partitions int) string {
	var parts []string
	if c.Position > 0 {
		place := fmt.Sprintf("pos %d", c.Position)
		if partitions > 1 {
			place = fmt.Sprintf("partition %d pos %d", c.Partition, c.Position)
		}
		parts = append(parts, place)
	}
	switch {
	case c.Code != "" && c.Reason != "":
		parts = append(parts, c.Code+": "+c.Reason)
	case c.Code != "":
		parts = append(parts, c.Code)
	case c.Mark != "":
		parts = append(parts, strings.ReplaceAll(c.Mark, "_", " "))
	}
	return strings.Join(parts, "  ")
}

// printJobChange names the change job id's checkout carries, whose turn it is, and the
// changes it touches.
func printJobChange(out io.Writer, changes []types.InflightChange, id string) {
	turns := map[types.InflightAttention]string{
		types.AttentionAuthor: "its author's turn",
		types.AttentionReview: "waiting on review",
		types.AttentionQueue:  "the queue's turn",
		types.AttentionNone:   "not queued",
	}
	for _, c := range changes {
		if !slices.Contains(c.Jobs, id) {
			continue
		}
		line := fmt.Sprintf("\nchange: #%s %s, %s", c.ID, clipTitle(c.Title, 48), turns[c.Attention])
		if d := inflightDetail(c, 0); d != "" {
			line += ", " + d
		}
		fmt.Fprintln(out, line)
		for _, n := range c.Neighbours {
			touched := slices.Concat(n.Paths, n.Declarations, n.Units)
			fmt.Fprintf(out, "  touches #%s: %s %s\n", n.ID, n.Evidence, strings.Join(touched, ", "))
		}
	}
}

func inflightJobs(ids []string) string {
	switch len(ids) {
	case 0:
		return ""
	case 1:
		return "job " + ids[0]
	}
	return "jobs " + strings.Join(ids, ", ")
}

// clipTitle cuts s to at most n bytes at a word boundary, marking the cut.
func clipTitle(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := strings.LastIndexByte(s[:n], ' ')
	if cut <= 0 {
		cut = n
	}
	return strings.TrimRight(s[:cut], " ") + "..."
}

func printJobTree(out io.Writer, report types.JobList) {
	if len(report.Jobs) == 0 {
		fmt.Fprintln(out, "No jobs. Declare one with `"+hint.JobFork.With("<job>")+"`, or with the `"+
			hint.ToolClient.String()+"` MCP tool (magus\\job.put), and every worktree of this repository reads it here.")
		return
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "JOB\tHOLDER\tSTATE\tMODEL\tPATHS\tPROOF\tCHECK")
	marks := map[string][]string{}
	for word, ids := range map[string][]string{"overdue": report.Overdue, "orphan": report.Orphans, "stale": report.Stale} {
		for _, id := range ids {
			marks[id] = append(marks[id], word)
		}
	}
	for _, row := range jobTreeOrder(report.Jobs) {
		state := orDash(string(row.lease.State))
		if m := marks[row.lease.ID]; len(m) > 0 {
			slices.Sort(m)
			state += " (" + strings.Join(m, ", ") + ")"
		}
		fmt.Fprintf(w, "%s%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			strings.Repeat("  ", row.depth), row.lease.ID,
			jobHolderCell(row.lease), state, jobModelCell(row.lease),
			len(row.lease.WritePaths), orDash(string(row.lease.WriteProof)), orDash(row.lease.Validation))
	}
	_ = w.Flush()

	for _, section := range []struct {
		title string
		ids   []string
	}{
		{"overdue: past the deadline their timeout set, so their writes are denied", report.Overdue},
		{"orphans: live under a job that has ended", report.Orphans},
		{"stale: not updated within jobs.stale_after", report.Stale},
	} {
		if len(section.ids) == 0 {
			continue
		}
		fmt.Fprintf(out, "\n%s\n", section.title)
		for _, id := range section.ids {
			fmt.Fprintf(out, "  %s: if nobody holds it, `%s`\n", id, hint.JobExit.With(id))
		}
	}
	if len(report.Blocked) > 0 {
		fmt.Fprintln(out, "\nblocked: own no paths until every dependency passes")
		for _, b := range report.Blocked {
			fmt.Fprintf(out, "  %s: %s\n", b.Job, b.String())
		}
	}
	if len(report.ReadOnly) > 0 {
		fmt.Fprintln(out, "\nread-only: require what this magus lacks, so it will not write them; update magus")
		for _, r := range report.ReadOnly {
			fmt.Fprintf(out, "  %s: requires %s\n", r.Job, strings.Join(r.Lacks, ", "))
		}
	}

	if len(report.Overlaps) == 0 {
		return
	}
	fmt.Fprintln(out, "\noverlaps")
	for _, o := range report.Overlaps {
		fmt.Fprintf(out, "  %s and %s claim common ground\n", o.JobA, o.JobB)
		fmt.Fprintf(out, "    %s: %s\n", o.JobA, strings.Join(o.PathsA, ", "))
		fmt.Fprintf(out, "    %s: %s\n", o.JobB, strings.Join(o.PathsB, ", "))
		if o.Claims == types.ClaimsDisjoint {
			fmt.Fprintln(out, "    claims: disjoint (different declarations of one file: an integration order, not a wait)")
		} else if o.Claims != "" {
			fmt.Fprintf(out, "    claims: %s\n", o.Claims)
		}
		if line := overlapFootprintLine(o.Footprint); line != "" {
			fmt.Fprintf(out, "    %s\n", line)
		}
	}
}

// jobModelCell is the model the job names, else the model tier the store assigned it.
func jobModelCell(row types.Job) string {
	if row.Model == "" && row.ModelTier != nil {
		return row.ModelTier.Name
	}
	return orDash(row.Model)
}

// jobHolderCell is who holds row as the tree names it: the server for its own jobs, the
// checkout a session job was taken in, and "-" for one nobody has taken. The checkout is
// the one fact that tells holders apart: every person and agent on a machine runs as the
// same OS account, and a CLI exec sees no other identity.
func jobHolderCell(row types.Job) string {
	switch {
	case row.Holder.OrSession() == types.HolderServer:
		return string(types.HolderServer)
	case row.CheckoutRoot != "":
		return filepath.Base(row.CheckoutRoot)
	}
	return "-"
}

// overlapFootprintLine is the footprint verdict under an overlapping pair, or "" when
// nobody computed one.
func overlapFootprintLine(f *types.JobOverlapFootprint) string {
	switch {
	case f == nil:
		return ""
	case f.Verdict == types.FootprintShared:
		return "footprints: shared (" + strings.Join(f.Shared, ", ") + ")"
	case f.Verdict == types.FootprintUnknown:
		return "footprints: unknown (" + f.Reason + ")"
	}
	return "footprints: " + f.Verdict
}

// jobTreeLine is one printed line: the row plus how deep its parent chain runs.
type jobTreeLine struct {
	lease types.Job
	depth int
}

// jobTreeOrder flattens the jobs parent-first, each one followed by the jobs it forked,
// preserving store order among siblings.
//
// Every job reaches the output. One whose parent was cleared, and one caught in a parent
// cycle, print at the top level instead of disappearing: a job nobody can see is worse
// than one shown without its indentation, and both cases mean the plan is already damaged.
func jobTreeOrder(leases []types.Job) []jobTreeLine {
	children := map[string][]types.Job{}
	for _, lease := range leases {
		children[lease.Parent] = append(children[lease.Parent], lease)
	}
	out := make([]jobTreeLine, 0, len(leases))
	emitted := map[string]bool{}
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		for _, lease := range children[parent] {
			if emitted[lease.ID] {
				continue
			}
			emitted[lease.ID] = true
			out = append(out, jobTreeLine{lease: lease, depth: depth})
			walk(lease.ID, depth+1)
		}
	}
	walk("", 0)
	for _, lease := range leases {
		if !emitted[lease.ID] {
			emitted[lease.ID] = true
			out = append(out, jobTreeLine{lease: lease})
		}
	}
	return out
}

// describeJob is `magus describe job`: one job's terms, which is what a holder reads on
// arrival. It prints the criteria, the write paths, the check, the model, the checkpoint, the
// dependencies, what the workspace itself puts out of reach, the graph's blast radius for
// each write path, and where each declared goal stands graded against the evidence magus
// holds now. It prints no procedure: taking the job is `magus job exec`'s work to DO,
// not a paragraph for somebody to follow by hand.
func describeJob(ctx context.Context, root string, args []string) error {
	pos, err := cmdParse("describe job", args, func(fs *flag.FlagSet) {
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
			fmt.Fprintln(os.Stderr, "holder still working on it. It is the same grading `"+hint.JobWait.String()+"` does, so the")
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
	i := slices.IndexFunc(leases, func(lease types.Job) bool { return lease.ID == pos[0] })
	if i < 0 {
		return fmt.Errorf("magus describe job: there is no job %q (run `%s` to see them)", pos[0], hint.LsJobs)
	}
	row := leases[i]
	if err := jobRefusesTheGate(ctx, root, row); err != nil {
		return err
	}

	// A read: the symbol goals, their delta, the deletion candidates and the graph evidence
	// take the graph as built, and none while a symbol index is stale (see jobGraph).
	graph := newJobGraph(root, false)
	facts := leaseBoundary(ctx, root, store, row, leases, graph)
	facts.Evidence, facts.GraphCold = leaseGraphEvidence(ctx, graph, row.WritePaths)
	brief := job.NewTerms(row, facts)

	status, err := job.GradeGates(ctx, store, row.ID, jobObserver(root, graph))
	if err != nil {
		return exit.Usagef("magus describe job: %s", err)
	}
	if gradesSymbols(leases, row.ID) {
		status.StaleIndexes = types.StaleIndexProjects(graph.staleIndexes(ctx))
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
		printJobWorktree(os.Stdout, row)
		job.RenderGates(os.Stdout, status)
		printJobEntries(os.Stdout, row.Entries)
		joined, _, err := joinInflight(ctx, root, store, types.JobList{Jobs: leases})
		if err != nil {
			return err
		}
		printJobChange(os.Stdout, joined.Changes, row.ID)
		pointAtConsole(ctx, row.ID)
		return nil
	default:
		return emitFormatted(opts, describeJobOutput{Terms: brief, Goals: status})
	}
}

// describeJobOutput is a job's terms plus where its declared goals stand, which is what
// `-o json`/`-o yaml` for `describe job` renders. json and yaml flatten the embedded
// terms, so the wire shape is the terms plus one `goals` key, the same
// queryWithNext/explainWithNext pattern uses to add a field without changing the type
// underneath it.
type describeJobOutput struct {
	job.Terms `yaml:",inline"`
	// Goals is where each of the job's declared goals stands, graded against the evidence
	// magus holds right now. Grading is still a READ: it records nothing, so asking never
	// advances a job and never blocks the holder still working on it. It is the same
	// grading `job wait` does, so the two cannot disagree.
	Goals types.JobStatus `json:"goals" yaml:"goals"`
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
	opts := types.VCSOptions{Enabled: globalCfg.VCS.Enabled, Name: globalCfg.VCS.Name, BaseRef: globalCfg.VCS.BaseRef}
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

// listFlag accumulates one repeatable, comma-separated flag, on the same rule --skip
// follows (cmd/magus/run.go): an empty segment is refused rather than dropped, so a
// trailing comma cannot silently shrink a lease's write paths.
//
// Not named for paths: --depends-on holds job ids and the gate flags hold symbols, and a
// type called pathList holding neither is a name that has to be read past.
type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }

// SplitSeq rather than Split: the intermediate slice is never used for anything but this
// loop, and this file already had three copies of the same split before they were folded
// back into this one.
func (l *listFlag) Set(value string) error {
	for part := range strings.SplitSeq(value, ",") {
		if part = strings.TrimSpace(part); part == "" {
			return errors.New("empty entry")
		}
		*l = append(*l, part)
	}
	return nil
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
		pairs = append(pairs, goal+"="+e[goal])
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

// forkFlags is the one-job case as flags, and job is the same record --stdin decodes.
// One conversion rather than two paths into the store, so a job typed at a terminal and a
// job piped in are the same declaration.
type forkFlags struct {
	criteria, parent, check, model, checkpoint string
	timeout, checkout                          string
	writePaths, readPaths, denyPaths           listFlag
	dependsOn                                  listFlag
	readOnly                                   bool
}

// row names no state: the store stores a declaration that names none as declared, so a
// flag fork and a record fork land the same.
func (f forkFlags) row(id string) types.Declaration {
	return types.Declaration{
		Schema:     types.Schema{Version: types.JobSchemaVersion},
		ID:         id,
		Parent:     f.parent,
		Criteria:   f.criteria,
		Checkpoint: f.checkpoint,
		WritePaths: f.writePaths,
		DenyPaths:  f.denyPaths,
		ReadPaths:  f.readPaths,
		DependsOn:  f.dependsOn,
		Check:      f.declaredCheck(),
		Model:      f.model,
		ReadOnly:   f.readOnly,
		Timeout:    f.timeout,
		Checkout:   f.checkout,
	}
}

// declaredCheck is --check as the record the row carries, or nil when the flag is absent.
// A value that does not parse reaches types.Declaration.Validate, which is the one place a
// declaration is refused.
func (f forkFlags) declaredCheck() *types.LeaseCheck {
	if strings.TrimSpace(f.check) == "" {
		return nil
	}
	parsed, err := types.ParseLeaseCheck(f.check)
	if err != nil {
		// Carried through unparsed so Validate names the rule, rather than being dropped
		// here and leaving the row silently checkless.
		return &types.LeaseCheck{Target: f.check}
	}
	return &parsed
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
		row           types.Declaration
		declared      forkFlags
		schema, stdin bool
		flags         *flag.FlagSet
	)
	pos, err := cmdParse("job fork", args, func(fs *flag.FlagSet) {
		flags = fs
		fs.BoolVar(&schema, "schema", false, "Print the JSON schema a job must satisfy, and exit")
		fs.BoolVar(&stdin, "stdin", false, "Read one job as JSON on stdin instead of taking it from flags")
		fs.StringVar(&declared.criteria, "criteria", "", "What this job is for and what done means, as prose; the machine-checkable half is --check, and the goals a --stdin record declares")
		fs.StringVar(&declared.timeout, "timeout", "", "Deny this job's writes once this long has passed since the fork (e.g. 45m, 2h); unset means no bound, unless magus.yaml sets jobs.default_timeout")
		fs.StringVar(&declared.parent, "parent", "", "The job this one is forked from")
		fs.StringVar(&declared.checkpoint, "checkpoint", "", "The working state this job is handed, as `magus vcs checkpoint -o name` prints it")
		fs.Var(&declared.writePaths, "write-paths", "A path this job may write, or `<file>#<declaration>` to claim one declaration of a file (a path holding a literal # is spelled `./a#b.md` or `a\\#b.md`); repeatable or comma-separated")
		fs.Var(&declared.denyPaths, "deny-paths", "A path this job may not write, carved out of its write paths; repeatable or comma-separated")
		fs.Var(&declared.readPaths, "read-paths", "A path whose projects this job may read; repeatable or comma-separated (additive: the written paths are readable already)")
		fs.Var(&declared.dependsOn, "depends-on", "A job this one waits on; repeatable or comma-separated")
		fs.StringVar(&declared.check, "check", "", "The one check this job runs, as `<target> <project> [-- args]` (the `magus run` is implied)")
		fs.StringVar(&declared.model, "model", "", "The model tier, or the model, the work was matched to; unset, the workspace's matrix or default chooses the tier")
		fs.BoolVar(&declared.readOnly, "read-only", false, "A job that gathers evidence and writes nothing")
		fs.StringVar(&declared.checkout, "checkout", "", "Make a new checkout of this repository for the job at this directory, outside the workspace; the job owns it, and magus removes it once the job ends unless it holds work")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job fork <job> [flags]")
			fmt.Fprintln(os.Stderr, "       magus job fork --stdin < job.json")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Declare one job: what its holder is handed, where it may write, and the one")
			fmt.Fprintln(os.Stderr, "check it runs. It replaces any job with the same id.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Goals are data: the record's `goals` declares what done means (a check that")
			fmt.Fprintln(os.Stderr, "passed; paths or symbols changed, present, absent or unreferenced), and")
			fmt.Fprintln(os.Stderr, "`"+hint.JobWait.String()+"` grades them. `--schema` prints the record. A job that")
			fmt.Fprintln(os.Stderr, "writes is refused without a check or a goal, and one naming no write paths must")
			fmt.Fprintln(os.Stderr, "be --read-only. A read-only job passes only by a script check, `\"check\":")
			fmt.Fprintln(os.Stderr, "{\"script\": \"<probe>.buzz\"}` in a --stdin record, whose result cites the ref")
			fmt.Fprintln(os.Stderr, "`"+hint.Buzz.With("--record", "<probe>.buzz")+"` prints; without one its holder ends it with")
			fmt.Fprintln(os.Stderr, "`"+hint.JobExit.String()+"`, recorded "+string(types.StateNoReturn)+".")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A session holding a lease may only fork a CHILD of its own job, inside its own")
			fmt.Fprintln(os.Stderr, "paths; widening a boundary is the forking session's.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The whole record is validated at once, and a fork with problems lists every one,")
			fmt.Fprintln(os.Stderr, "each with its code and fix, and writes nothing. The global --dry-run does the")
			fmt.Fprintln(os.Stderr, "same without forking: it prints the problems, the record as it would be stored,")
			fmt.Fprintln(os.Stderr, "the waived problems and the checks that could not run, and exits 1 while any")
			fmt.Fprintln(os.Stderr, "problem remains. A waiver in the record keeps a broad write path, an output a")
			fmt.Fprintln(os.Stderr, "target regenerates outside the write paths, or a ci target the job does not run.")
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
			fmt.Fprintln(os.Stderr, "revision when it names none. A ./magus this checkout runs is copied into it. The")
			fmt.Fprintln(os.Stderr, "job is taken there and nowhere else, and once it ends magus removes the checkout")
			fmt.Fprintln(os.Stderr, "if it is clean and every commit in it is on a branch, bookmark, remote or its")
			fmt.Fprintln(os.Stderr, "base; otherwise it keeps it and records why. It is the one job flag --stdin")
			fmt.Fprintln(os.Stderr, "takes, for a record that names no checkout; the record holds the rest.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "There is no --state: this declares a NEW job, and one nobody has taken is")
			fmt.Fprintln(os.Stderr, string(types.StateDeclared)+". A holder moves its own job with `"+hint.JobExec.String()+"` and `"+hint.JobExit.String()+"`.")
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
	if schema {
		fmt.Print(job.DeclarationSchema)
		return nil
	}

	if declared.checkout != "" {
		if declared.checkout, err = filepath.Abs(declared.checkout); err != nil {
			return exit.Usagef("magus job fork: --checkout: %s", err)
		}
	}
	switch {
	case stdin:
		if len(pos) > 0 {
			return exit.Usagef("magus job fork: --stdin reads the id from the record, so %q is one id too many", pos[0])
		}
		var dropped []string
		flags.Visit(func(f *flag.Flag) {
			if slices.Contains([]string{"criteria", "timeout", "parent", "checkpoint", "write-paths", "deny-paths", "read-paths", "depends-on", "check", "model", "read-only"}, f.Name) {
				dropped = append(dropped, "--"+f.Name)
			}
		})
		if len(dropped) > 0 {
			return exit.Usagef("magus job fork: --stdin takes the job from the record, so %s would be ignored: put it in the record (`%s` prints its fields)",
				strings.Join(dropped, ", "), hint.JobFork.With("--schema"))
		}
		if row, err = job.DecodeDeclaration(os.Stdin); err != nil {
			return exit.Usagef("magus job fork: %s (`%s` prints the schema it must satisfy)", err, hint.JobFork.With("--schema"))
		}
		// --checkout says where the job runs, so it rides beside a record that names none.
		switch {
		case row.Checkout == "":
			row.Checkout = declared.checkout
		case declared.checkout != "" && filepath.Clean(row.Checkout) != declared.checkout:
			return exit.Usagef("magus job fork: --checkout %s and the record's checkout %s name two checkouts: give one", declared.checkout, row.Checkout)
		}
	case len(pos) != 1:
		return exit.Usagef("magus job fork: requires exactly one job id, or --stdin with a job on it")
	default:
		row = declared.row(pos[0])
		if err := row.Validate(); err != nil {
			return exit.Usagef("magus job fork: %s", err)
		}
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
	store = store.InWorkspace(jobWS)
	// The reader loads the graph only when a gate names a symbol, or when the deletion
	// candidates ask for it, and the two share one load.
	graph := newJobGraph(root, true)
	reader := graphSymbolReader(graph.load)
	merge := job.Declare(row, globalCfg.Jobs.DefaultTimeout)
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	// A job that works in its own checkout shares none, which the standing placeholder says.
	// Validating before the worktree is made leaves nothing behind for a record with problems.
	judged := merge
	if row.Checkout != "" {
		judged = func(u *types.Job) {
			merge(u)
			u.Worktree = &types.JobWorktree{Root: row.Checkout}
		}
	}
	if globalCfg.DryRun || row.Checkout != "" {
		validation, err := job.ValidateFork(ctx, store, row.ID, judged, globalCfg.Jobs, reader)
		if err != nil {
			return err
		}
		if globalCfg.DryRun {
			return forkDryRun(opts, validation)
		}
		if len(validation.Problems) > 0 {
			return refuseFork(validation)
		}
	}
	var worktree types.JobWorktree
	made := false
	if row.Checkout != "" {
		exe, _ := os.Executable()
		if worktree, made, err = store.ForkWorktree(ctx, row.ID, row.Checkout, row.Checkpoint, job.MirroredBinary(exe, root)); err != nil {
			return err
		}
		declare := merge
		merge = func(u *types.Job) {
			declare(u)
			// The job starts from what its checkout holds, not from the forker's tree.
			if u.Checkpoint == "" {
				u.Checkpoint = worktree.Base
			}
			u.Worktree = &worktree
		}
	}
	stored, err := job.ForkMerge(ctx, store, row.ID, merge, globalCfg.Jobs, reader)
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
		// The fork stored nothing, so the validation is read again for the list it joined.
		if v, verr := job.ValidateFork(ctx, store, row.ID, merge, globalCfg.Jobs, reader); verr == nil && len(v.Problems) > 0 {
			return refuseFork(v)
		}
		return exit.Usagef("magus job fork: %s", err)
	}
	var asked types.Job
	row.Apply(&asked)
	derived := slices.DeleteFunc(slices.Clone(stored.WritePaths), func(p string) bool { return slices.Contains(asked.WritePaths, p) })

	if opts.Format == outputName {
		return emitNames(os.Stdout, []string{stored.ID})
	}
	// The notes compare the row with its peers, which the merge read and did not return.
	plan, err := store.List()
	if err != nil {
		return err
	}
	var overrun *job.ForegroundOverrun
	if !stored.ReadOnly && len(stored.WritePaths) > 0 {
		overrun, err = forkOverrun(ctx, root, stored)
		switch {
		case err != nil:
			warnWorkspaceUnloaded(ctx, err)
		case jobWS.Unavailable != "":
			warnWorkspaceUnloaded(ctx, errors.New(jobWS.Unavailable))
		}
	}
	referenced, indexed := referencedOutside(ctx, graph.load, root, stored, jobWS.Projects)
	switch opts.Format {
	case outputText:
		fmt.Printf("forked %s, %s, with %d write path(s). Its holder reads the terms with `%s` and takes it with `%s`\n",
			stored.ID, orDash(string(stored.State)), len(stored.WritePaths),
			hint.DescribeJob.With(stored.ID), hint.JobExec.With(stored.ID))
		if stored.ModelTier != nil {
			fmt.Printf("model tier: %s\n", stored.ModelTier)
		}
		printJobWorktree(os.Stdout, stored)
		printClaimNotes(os.Stdout, job.ForkNotes(ctx, store, plan, stored))
		if len(derived) > 0 {
			fmt.Printf("its symbol goals claimed %d declaration(s) its write paths did not cover:\n", len(derived))
			for _, claim := range derived {
				fmt.Printf("  %s\n", claim)
			}
		}
		job.RenderDeletionCandidates(os.Stdout, deletionCandidates(ctx, graph.load, stored))
		job.RenderReferencedOutside(os.Stdout, referenced, indexed)
		job.RenderForegroundOverrun(os.Stdout, overrun)
		// The forker waits for the build budget so the job's holder never does.
		if globalCfg.Broker.Resolved() != types.BrokerOff {
			if capacity, err := queryCapacity(ctx); err == nil {
				if full := guard.BudgetFull(capacity, time.Now()); full != "" {
					fmt.Println(full)
				}
			}
		}
		pointAtConsole(ctx, stored.ID)
		return nil
	default:
		return emitFormatted(opts, forkReport{
			forkOutput{
				Job:                stored,
				DeletionCandidates: deletionCandidates(ctx, graph.load, stored),
				ReferencedOutside:  referenced,
				ForegroundOverrun:  overrun,
				ClaimNotes:         job.ForkNotes(ctx, store, plan, stored),
			},
			derived,
		})
	}
}

// printClaimNotes prints each of a fork's claim notes on a line of its own.
func printClaimNotes(out io.Writer, notes []string) {
	for _, n := range notes {
		fmt.Fprintf(out, "note: %s\n", n)
	}
}

// forkReport is forkOutput with the write path claims the fork derived from its symbol
// goals, which are computed at the fork and never stored apart from write_paths.
type forkReport struct {
	forkOutput    `yaml:",inline"`
	DerivedClaims []string `json:"derived_claims,omitempty" yaml:"derived_claims,omitempty"`
}

// workspaceStage is the release stage of m's newest root tag, unknown when its VCS lists
// none.
func workspaceStage(ctx context.Context, m *magus.Magus) types.ReleaseStage {
	res, err := vcs.Resolve(ctx, m.Root(), "", m.VCSOptions())
	if err != nil || res.VCS == nil {
		return types.ReleaseStage{}
	}
	tags, err := res.VCS.Tags(ctx, m.Root(), "")
	if err != nil {
		return types.ReleaseStage{}
	}
	return types.NewestReleaseStage(tags)
}

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
		pointAtConsole(ctx, stored.ID)
		return nil
	default:
		return emitFormatted(opts, stored)
	}
}

// printJobEntries lists a job's entries, one line each, or nothing when it has none.
func printJobEntries(out io.Writer, entries []types.JobEntry) {
	if len(entries) == 0 {
		return
	}
	fmt.Fprintln(out, "entries: writes into its paths by somebody other than its holder")
	for _, e := range entries {
		written := "not yet written"
		if e.Consumed != 0 {
			written = "written " + time.Unix(e.Consumed, 0).UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(out, "  %s  entered %s by %s, %s\n", e.Path,
			time.Unix(e.At, 0).UTC().Format(time.RFC3339), e.By.Label(), written)
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
	pos, err := cmdParse("job exec", args, func(fs *flag.FlagSet) {
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
			fmt.Fprintln(os.Stderr, "broker records the command with its owner, deadline and slots for `magus status`,")
			fmt.Fprintln(os.Stderr, "stops the group if this process dies first, and files one attention row for")
			fmt.Fprintln(os.Stderr, "each command it stops. The exit status is the command's, or 124 at the deadline.")
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
		fmt.Printf("took %s in %s; read the terms with `%s`\n", stored.ID, root, hint.DescribeJob.With(stored.ID))
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
// deadline. The broker is told what runs, so `magus status` shows it by owner and the
// broker stops the group if this process dies first; this process enforces the deadline
// itself too, so the bound holds with no broker (broker: off, or none answered).
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
	spawn := trail.SpawnFromEnv()
	lease := spawn.Lease
	if root != "" {
		if loc, err := jobPlace(root); err == nil {
			lease, _ = job.ActingLease(loc, spawn.Lease)
		}
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	broker.InOwnGroup(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("magus job exec: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	h := broker.Hold{
		Lease: lease, Spawner: spawn.Spawner, PGID: cmd.Process.Pid, Command: strings.Join(command, " "),
		Root: root, Slots: slots, Deadline: time.Now().Add(timeout),
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
		slog.WarnContext(ctx, "the broker is not watching this command, so `magus status` will not show it; this process still stops it at its deadline",
			attr.Error(err))
		return func() {}
	}
	return func() {
		rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		client.ReleaseHold(rctx, id)
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
	pos, err := cmdParse("job exit", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&schema, "schema", false, "Print the JSON schema a result must satisfy, and exit")
		fs.BoolVar(&stdin, "stdin", false, "Read this job's result from stdin; without it the job ends with no result filed")
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
			fmt.Fprintln(os.Stderr, "With no --stdin each named job is ended and recorded "+string(types.StateNoReturn)+", which is")
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
	store, err := openJobs(ctx, root, jobOpen{})
	if err != nil {
		return err
	}

	if !stdin {
		return abandonJobs(ctx, store, pos)
	}

	result, err := job.DecodeResult(os.Stdin)
	if err != nil {
		return exit.Usagef("magus job exit: %s (`%s` prints the schema it must satisfy)", err, hint.JobExit.With("--schema"))
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
	switch opts.Format {
	case outputName:
		return emitNames(os.Stdout, []string{stored.ID})
	case outputText:
		fmt.Printf("exited %s, recorded %s, with its result filed. Whoever forked it verifies with `%s`\n",
			stored.ID, stored.State, hint.JobWait.With(stored.ID))
		return nil
	default:
		return emitFormatted(opts, stored)
	}
}

// abandonJobs ends every named job as no_return (see [job.Abandon]), printing one line per
// job with the end_reason the store recorded for it. A job that cannot be ended does not
// stop the rest: each is tried, and the command fails afterwards naming every refusal.
func abandonJobs(ctx context.Context, store *job.Store, ids []string) error {
	ended, refused := job.Abandon(ctx, store, ids)
	endedIDs := make([]string, len(ended))
	for i, stored := range ended {
		endedIDs[i] = stored.ID
		fmt.Printf("ended %s, recorded %s: %s\n", stored.ID, stored.State, stored.EndReason)
	}
	removed := endWorktrees(ctx, os.Stdout, store, endedIDs...)
	if len(refused) > 0 {
		reasons := make([]string, len(refused))
		for i, err := range refused {
			reasons[i] = err.Error()
		}
		return errors.Join(exit.Usagef("magus job exit: %d of %d jobs not ended:\n  %s", len(ids)-len(ended), len(ids), strings.Join(reasons, "\n  ")), removed)
	}
	return removed
}

// endWorktrees removes the checkouts that jobs which ended own, and the linked checkout each
// job in ended was taken in, restoring what magus wrote in them first, and prints what
// became of each one: removed, or kept and why. What magus wrote includes the tool rows a
// wired harness's install puts in a checkout, which describe file leaves unclaimed.
func endWorktrees(ctx context.Context, out io.Writer, store *job.Store, ended ...string) error {
	if globalCfg.DryRun {
		return nil
	}
	written := func(ctx context.Context, checkout string, paths []string) ([]string, error) {
		ws, err := magus.Inspect(ctx, checkout, append(cliOptions(), workspace.WithLoadedConfig(globalCfg), magus.WithVersion(version))...)
		if err != nil {
			return nil, err
		}
		files, err := ws.ClassifyFiles(ctx, paths)
		if err != nil {
			return nil, err
		}
		wired := workspaceHarnessNames(ws)
		hostCtx := agent.ContextWithWiredHarnesses(ctx, wired)
		var toolRows []agent.HarnessTools
		for _, id := range wired {
			if d, _, err := agent.LoadHarness(hostCtx, ws.Root(), id); err == nil && d.Tools != nil {
				toolRows = append(toolRows, *d.Tools)
			}
		}
		var out []string
		for _, f := range files {
			installed := slices.ContainsFunc(toolRows, func(t agent.HarnessTools) bool { return t.Holds(f.Path) })
			if installed || f.Role == types.DiffRoleOutput || f.Role == types.DiffRoleMaintained {
				out = append(out, f.Path)
			}
		}
		return out, nil
	}
	changed, err := store.EndCheckouts(ctx, written, ended...)
	for _, row := range changed {
		printJobWorktree(out, row)
	}
	if err != nil {
		return fmt.Errorf("magus job: recording what became of a job's checkout: %w", err)
	}
	return nil
}

// printJobWorktree says where the checkout a job owns stands: made for it, removed, or kept
// and why.
func printJobWorktree(out io.Writer, row types.Job) {
	wt := row.Worktree
	switch {
	case wt == nil:
	case wt.Removed != 0:
		fmt.Fprintf(out, "checkout %s: removed once job %s ended\n", wt.Root, row.ID)
	case wt.Kept != "":
		fmt.Fprintf(out, "checkout %s: kept after job %s ended: %s\n", wt.Root, row.ID, wt.Kept)
	default:
		fmt.Fprintf(out, "checkout %s: made for job %s at %.12s; its holder takes it there with `%s`, and magus removes it once the job ends unless it holds work\n",
			wt.Root, row.ID, wt.Base, hint.JobExec.With(row.ID))
		if wt.Binary != "" {
			fmt.Fprintf(out, "  placed %s, the magus the forking checkout runs\n", wt.Binary)
		} else {
			fmt.Fprintln(out, "  placed no magus: the forking checkout runs magus from PATH, and so does the job")
		}
	}
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
	pos, err := cmdParse("job wait", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&schema, "schema", false, "Print the JSON schema a result must satisfy, and exit")
		fs.BoolVar(&stdin, "stdin", false, "Read the result from stdin instead of from the job, for one that was never filed")
		fs.BoolVar(&integration, "integration", false, "Grade only the job's check goals against the runs the --stdin result names in THIS checkout, and record that beside its state")
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
			fmt.Fprintln(os.Stderr, "that verifies is recorded "+string(types.StatePass)+".")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Exit 1 is a status: the result was read and rejected, and every rule that failed")
			fmt.Fprintln(os.Stderr, "is named. Exit 2 is magus unable to answer: nothing was filed and nothing was")
			fmt.Fprintln(os.Stderr, "piped in, the result would not decode, or the job would not write.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "It checks what is mechanical. Whether the work is GOOD, and whether the job's")
			fmt.Fprintln(os.Stderr, "acceptance criteria are met, stay the reading of whoever forked it.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "--evidence <goal>=<ref> grades a check goal against a run recorded in THIS checkout")
			fmt.Fprintln(os.Stderr, "instead of the one the job was exited with; the job's own check is `"+types.PrimaryGoalID+"`.")
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
			fmt.Fprintln(os.Stderr, "the result's validation.output_ref and gate_evidence name here, and the grade is")
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
	status, err := job.Wait(ctx, store, pos[0], result, storedAttempts(root), jobObserver(root, newJobGraph(root, true)), evidence)
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
		printJobStatus(os.Stdout, status)
		pointAtConsole(ctx, status.Job)
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
	pos, err := cmdParse("job watch", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job watch <job>")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Follow what a job's holder is doing, one line per event, until interrupted.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Three sources, merged in time order: files changed under the job's declared write")
			fmt.Fprintln(os.Stderr, "paths, tool calls the guard observed under its lease, and the runs magus recorded")
			fmt.Fprintln(os.Stderr, "against it. None of them asks the holder anything, so watching costs it nothing.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "`"+hint.DescribeJob.With("<job>")+"` grades what it has finished; this shows what it is doing.")
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
	pointAtConsole(ctx, id)
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
				printFeedLine(out, e)
			}
		case <-tick.C:
			for _, e := range job.ToolEvents(cursor.Next(job.Ascending(recentTrail(cacheDir)))) {
				if e.Job == id {
					printFeedLine(out, e)
				}
			}
			for _, e := range runs.Next(plan()) {
				if e.Job == id {
					printFeedLine(out, e)
				}
			}
		}
	}
}

// jobWatchFiles starts this checkout's own file watcher and returns the attributed changes.
// A watcher that will not start is REPORTED and then done without: the other two sources
// still answer, and a silently missing third is how a feed comes to show a quiet tree.
func jobWatchFiles(ctx context.Context, out io.Writer, root string, plan func() []types.Job) <-chan job.FeedEvent {
	// RelativeIgnore, not BuiltinIgnore: an agent worktree lives under a dot-directory, and
	// the absolute form would skip every file in the very tree this verb exists to watch.
	w, err := watch.New(ctx, watch.WithRoot(root), watch.WithIgnore(watch.RelativeIgnore(root, watch.BuiltinIgnore)))
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

// printFeedLine writes one event as one line. Fixed columns rather than prose, because the
// value of this feed is skimming a column: a person watching four workers is looking for
// the word "deny" going past, not reading sentences.
func printFeedLine(out io.Writer, e job.FeedEvent) {
	stamp := time.UnixMilli(e.Ts).Format("15:04:05")
	switch e.Kind {
	case job.FeedFile:
		fmt.Fprintf(out, "%s  file  %s\n", stamp, e.Action)
	case job.FeedTool:
		verdict := e.Decision
		if verdict == "" {
			verdict = "observed" // the guard judged nothing; see trail.AppendAgentCommand
		}
		fmt.Fprintf(out, "%s  tool  %-8s %s\n", stamp, verdict, e.Action)
	default:
		status := "ok"
		if e.Outcome == trail.OutcomeError {
			status = "failed"
		}
		fmt.Fprintf(out, "%s  run   %-8s %s  %s\n", stamp, status, e.Action, e.Ref)
	}
	if e.Note != "" && e.Kind == job.FeedFile {
		fmt.Fprintf(out, "          %s\n", e.Note)
	}
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

func printJobStatus(out io.Writer, s job.Status) {
	if s.Verified {
		fmt.Fprintf(out, "verified %s, recorded %s\n", s.Job, types.StatePass)
	} else {
		fmt.Fprintf(out, "rejected %s, and its state is unchanged\n", s.Job)
		for _, violation := range s.Violations {
			fmt.Fprintf(out, "  %s\n", violation)
		}
	}
	if s.Command != "" {
		fmt.Fprintf(out, "its holder reports it ran %s\n", s.Command)
	}
	for _, gate := range s.Gates {
		state := "rejected"
		if gate.Verified {
			state = "verified"
		}
		fmt.Fprintf(out, "goal %s: %s", gate.ID, state)
		if gate.OutputRef != "" {
			fmt.Fprintf(out, " (%s)", gate.OutputRef)
		}
		fmt.Fprintln(out)
		// A failed gate names the ref but not how to read it; one command away
		// from the projects the run touched and the diff it produced.
		if !gate.Verified && gate.OutputRef != "" {
			fmt.Fprintf(out, "  %s\n", hint.QueryOutput.With(gate.OutputRef))
		}
	}
	printJobFootprint(out, s)
	printJobEntries(out, s.Entries)
	if !s.Clean() {
		fmt.Fprintln(out, "unresolved risks its holder reported")
		for _, risk := range s.UnresolvedRisks {
			fmt.Fprintf(out, "  %s\n", risk)
		}
	}
}

// printJobFootprint writes the declarations the job's diff landed in, one line each. An
// unknown footprint says why in one line: silence would read as a job that touched nothing.
// A landing outside the job's declaration claims is among the violations above it.
func printJobFootprint(out io.Writer, s job.Status) {
	const heading = "footprint"
	if !s.FootprintKnown {
		reason := s.FootprintReason
		if reason == "" {
			reason = "nothing observed the tree"
		}
		fmt.Fprintf(out, "%s: not known, %s\n", heading, reason)
		return
	}
	if len(s.Footprint) == 0 {
		fmt.Fprintf(out, "%s: no line changed since the checkpoint\n", heading)
		return
	}
	fmt.Fprintf(out, "%s: where its diff since the checkpoint landed\n", heading)
	var printed []string
	for _, r := range s.Footprint {
		line := r.Location().String()
		if r.Declaration == "" {
			line = fmt.Sprintf("%s:%d-%d", r.File.Path, r.Lines[0], r.Lines[1])
		}
		if slices.Contains(printed, line) {
			continue
		}
		printed = append(printed, line)
		if slices.ContainsFunc(s.Entries, func(e types.JobEntry) bool { return e.Consumed != 0 && job.Covers(e.Path, r.File.Path) }) {
			line += "  (entered: may be the entrant's write, not the holder's)"
		}
		fmt.Fprintf(out, "  %s\n", line)
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
	pos, err := cmdParse("job rm", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&force, "force", false, "Remove a row that already ended, destroying the record of what happened")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job rm <job> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Remove ONE job from the plan. The rows it leaves alone are the difference from")
			fmt.Fprintln(os.Stderr, "`"+hint.ToolClient.String()+"` (magus\\job.clear), which drops every row in the repository.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "This is not how a job ENDS. `"+hint.JobExit.String()+"` records what happened and")
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

// jobApply is `magus job apply -f <file|->`, the way `kubectl apply -f` reads a manifest:
// each record is a job's whole spec, upserted in order, and nothing magus records about the
// job (its state, holder, registration, results) moves. A new id creates the job.
func jobApply(ctx context.Context, root string, args []string) error {
	var file string
	pos, err := cmdParse("job apply", args, func(fs *flag.FlagSet) {
		fs.StringVar(&file, "f", "", "The records to apply: a file, or - for stdin; one JSON job, a JSON array, or one job per line")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job apply -f <file|->")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Upsert each record's spec: criteria, write, deny and read paths, check, goals,")
			fmt.Fprintln(os.Stderr, "model, depends_on, parent and timeout. The record is the whole spec, so a spec")
			fmt.Fprintln(os.Stderr, "field it leaves out is cleared; an omitted checkpoint or timeout keeps the job's.")
			fmt.Fprintln(os.Stderr, "Status is never written: the job keeps its state, holder and registration, and a")
			fmt.Fprintln(os.Stderr, "record carrying state is refused. A new id creates the job, as fork would.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Every record is checked before any is written, with fork's rules for what it")
			fmt.Fprintln(os.Stderr, "adds, and every record's problems are listed at once with their codes. It prints")
			fmt.Fprintln(os.Stderr, "what changed per job; the global --dry-run prints it and writes nothing.")
			fmt.Fprintln(os.Stderr, "`"+hint.JobFork.With("--schema")+"` prints the record, waivers included.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Example:")
			fmt.Fprintln(os.Stderr, "  magus job apply -f jobs.jsonl --dry-run")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Widening is the orchestrator's: a session holding a lease may apply only a spec")
			fmt.Fprintln(os.Stderr, "that removes some of its own write paths, which releases them. Ending a job is")
			fmt.Fprintln(os.Stderr, "`"+hint.JobExit.String()+"`.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return exit.Usagef("magus job apply: the records name their jobs, so %q is one argument too many", pos[0])
	}
	var in io.Reader
	switch file {
	case "":
		return exit.Usagef("magus job apply: name the records with -f <file>, or -f - for stdin")
	case "-":
		in = os.Stdin
	default:
		f, err := os.Open(file)
		if err != nil {
			return exit.Usagef("magus job apply: %s", err)
		}
		defer f.Close()
		in = f
	}
	records, err := job.DecodeDeclarations(in)
	if err != nil {
		return exit.Usagef("magus job apply: %s (`%s` prints the schema each record must satisfy)", err, hint.JobFork.With("--schema"))
	}
	root = resolveRootOrEmpty(root)
	// A dry run assigns no tier, so it runs no matrix.
	store, err := openJobs(ctx, root, jobOpen{DryRun: globalCfg.DryRun, ModelTiers: !globalCfg.DryRun})
	if err != nil {
		return err
	}
	jobWS := jobWorkspace(ctx, root)
	store = store.InWorkspace(jobWS)
	checkpoint := func() string {
		token, _ := checkoutBaseToken(ctx, root)
		return token
	}
	applied, err := job.Apply(ctx, store, records, globalCfg.Jobs, jobSymbolReader(root, true), checkpoint, globalCfg.DryRun)
	if err != nil {
		ids := make([]string, len(records))
		for i, rec := range records {
			ids[i] = rec.ID
		}
		validations, ok := job.ApplyValidations(err, ids)
		if !ok {
			return exit.Usagef("magus job apply: %s", err)
		}
		var list strings.Builder
		for _, v := range validations {
			fmt.Fprintf(&list, "%s:\n", v.Job.ID)
			job.RenderValidation(&list, v, false)
		}
		return listedProblems{list: list.String(), err: err}
	}
	if jobWS.Unavailable != "" && slices.ContainsFunc(applied, func(a job.Applied) bool { return !a.Next.ReadOnly && len(a.Next.WritePaths) > 0 }) {
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
		printJobApply(os.Stdout, applied, claims, globalCfg.DryRun)
		if len(applied) == 1 && !globalCfg.DryRun {
			pointAtConsole(ctx, applied[0].Next.ID)
		}
		return nil
	}
	report := applyReport{DryRun: globalCfg.DryRun}
	for _, a := range applied {
		report.Jobs = append(report.Jobs, applyEntry{Job: a.Next, Created: a.Created, Changed: a.Changed, ClaimNotes: claims[a.Next.ID]})
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
		if notes := job.ForkNotes(ctx, store, rows, a.Next); len(notes) > 0 {
			out[a.Next.ID] = notes
		}
	}
	return out
}

// applyReport is `job apply`'s structured output: each row as written, or as it would be.
type applyReport struct {
	Jobs   []applyEntry `json:"jobs"    yaml:"jobs"`
	DryRun bool         `json:"dry_run" yaml:"dry_run"`
}

type applyEntry struct {
	Job     types.Job `json:"job"     yaml:"job"`
	Created bool      `json:"created" yaml:"created"`
	Changed []string  `json:"changed" yaml:"changed"`
	// RegeneratedOutside is computed for a row whose write or deny paths the apply set.
	RegeneratedOutside []job.RegeneratedOutput `json:"regenerated_outside,omitempty" yaml:"regenerated_outside,omitempty"`
	// UnrunGates is computed beside it, for the same rows.
	UnrunGates []job.UnrunGate `json:"unrun_gates,omitempty" yaml:"unrun_gates,omitempty"`
	// ClaimNotes is forkOutput's, for every applied row.
	ClaimNotes []string `json:"claim_notes,omitempty" yaml:"claim_notes,omitempty"`
}

// printJobApply prints each row's change, then the claim notes for it.
func printJobApply(out io.Writer, applied []job.Applied, claims map[string][]string, dryRun bool) {
	for _, a := range applied {
		switch {
		case a.Created && dryRun:
			fmt.Fprintf(out, "would create %s with %d write path(s)\n", a.Next.ID, len(a.Next.WritePaths))
		case a.Created:
			fmt.Fprintf(out, "created %s, %s, with %d write path(s)\n", a.Next.ID, a.Next.State, len(a.Next.WritePaths))
			if a.Next.ModelTier != nil {
				fmt.Fprintf(out, "model tier: %s\n", a.Next.ModelTier)
			}
		case len(a.Changed) == 0:
			fmt.Fprintf(out, "unchanged %s\n", a.Next.ID)
		default:
			verb := "updated"
			if dryRun {
				verb = "would update"
			}
			fmt.Fprintf(out, "%s %s, still %s:\n", verb, a.Next.ID, a.Next.State)
			for _, field := range a.Changed {
				fmt.Fprintf(out, "  %s\n", specChange(field, a.Prev, a.Next))
			}
		}
		printClaimNotes(out, claims[a.Next.ID])
	}
	if dryRun {
		fmt.Fprintln(out, "dry run: nothing written; rerun without --dry-run to write it")
	}
}

// specChange renders one changed spec field: the entries a list gained and lost, or the
// field's name for a value that changed.
func specChange(field string, prev, next types.Job) string {
	lists := map[string][2][]string{
		"write_paths": {prev.WritePaths, next.WritePaths},
		"deny_paths":  {prev.DenyPaths, next.DenyPaths},
		"read_paths":  {prev.ReadPaths, next.ReadPaths},
		"depends_on":  {prev.DependsOn, next.DependsOn},
		"breaks":      {prev.Breaks, next.Breaks},
		"goals":       {goalIDs(prev.Goals), goalIDs(next.Goals)},
	}
	pair, ok := lists[field]
	if !ok {
		return field + " changed"
	}
	var parts []string
	for _, p := range pair[1] {
		if !slices.Contains(pair[0], p) {
			parts = append(parts, "+"+p)
		}
	}
	for _, p := range pair[0] {
		if !slices.Contains(pair[1], p) {
			parts = append(parts, "-"+p)
		}
	}
	if len(parts) == 0 {
		return field + " changed"
	}
	return field + " " + strings.Join(parts, " ")
}

func goalIDs(goals []types.Goal) []string {
	ids := make([]string, len(goals))
	for i, g := range goals {
		ids[i] = g.ID
	}
	return ids
}

// jobPrune is `magus job prune`: end every job nobody is working, the way `job exit`
// abandons one, so rows left over from finished work stop refusing new forks.
//
// --all is `docker system prune -a`'s knob and `ls jobs --all`'s word: the wider set.
func jobPrune(ctx context.Context, root string, args []string) error {
	var all bool
	pos, err := cmdParse("job prune", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&all, "all", false, "Also end a job a holder took and nobody touched within jobs.stale_after")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job prune [--all] [<job>...] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "End, as "+string(types.StateNoReturn)+", every job the store can show nobody is working:")
			fmt.Fprintln(os.Stderr, "exited and never collected with `"+hint.JobWait.String()+"`, overdue, orphaned by an")
			fmt.Fprintln(os.Stderr, "ancestor that ended, or declared and never taken past jobs.stale_after. Each row")
			fmt.Fprintln(os.Stderr, "keeps its end_reason, as `"+hint.JobExit.String()+"` would leave it.")
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
	store, err := openJobs(ctx, resolveRootOrEmpty(root), jobOpen{})
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
		printPruned(os.Stdout, ended, globalCfg.DryRun)
		return endWorktrees(ctx, os.Stdout, store, endedIDs...)
	default:
		if ended == nil {
			ended = []job.Ending{}
		}
		return emitFormatted(opts, pruneReport{Ended: ended, DryRun: globalCfg.DryRun})
	}
}

// pruneReport is `job prune`'s structured output.
type pruneReport struct {
	Ended  []job.Ending `json:"ended"   yaml:"ended"`
	DryRun bool         `json:"dry_run" yaml:"dry_run"`
}

func printPruned(out io.Writer, ended []job.Ending, dryRun bool) {
	verb := "ended"
	if dryRun {
		verb = "would end"
	}
	for _, e := range ended {
		fmt.Fprintf(out, "%s %s: %s\n", verb, e.ID, e.Reason)
	}
	if dryRun {
		fmt.Fprintf(out, "would end %d job(s); --dry-run ended nothing\n", len(ended))
		return
	}
	fmt.Fprintf(out, "ended %d job(s)\n", len(ended))
}
