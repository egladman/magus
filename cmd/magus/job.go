package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// jobCmd implements `magus job`, the POSIX child lifecycle over delegated work: fork
// declares a job, exec takes the lease on it in this checkout, exit returns it with its
// result, and wait verifies that result. Listing is `magus ls jobs` and the terms are
// `magus describe job`, because enumerating and defining are those verbs' work everywhere
// else in this CLI.
//
// BOTH CHANNELS WRITE, and the store is what keeps the book honest. The magus_job MCP
// tool is the agent's channel and this verb is the person's; they reach the same store and
// the same rules, so a job still has one author (internal/job.authorizeRow enforces it)
// without a capability existing for agents that a person does not have.
//
// It takes the --root OVERRIDE and resolves it per verb rather than once here: two of the
// verbs reach loadMagus, whose singleton panics when a second caller hands it a different
// spelling of the same root.
func jobCmd(ctx context.Context, root string, args []string) error {
	if len(args) == 0 {
		jobUsage()
		return usagef("magus job: requires a subcommand (fork, exec, exit, wait, run or rm)")
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
	default:
		return usagef("magus job: unknown subcommand %q (want fork, exec, exit, wait, watch, run or rm; `%s` lists what is in flight)", args[0], hint.LsJobs)
	}
}

func jobUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus job <fork|exec|exit|wait|run> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Delegated work, on the shell's own lifecycle. A job is the unit of work; a lease is")
	fmt.Fprintln(os.Stderr, "the grant one holder has on it: the paths it may write and read, plus the one check it runs.")
	fmt.Fprintln(os.Stderr, "A job is not a run: `magus run` executes a target with no job involved, while a job's")
	fmt.Fprintln(os.Stderr, "check and the server's maintenance each cause runs.")
	fmt.Fprintln(os.Stderr, "Kept per repository, so every worktree and clone reads one set of jobs.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  fork  declare a job, from flags or a JSON record on stdin")
	fmt.Fprintln(os.Stderr, "  exec  take the lease on a job here, and record the base this checkout landed on;")
	fmt.Fprintln(os.Stderr, "        --vacate gives it up instead")
	fmt.Fprintln(os.Stderr, "  exit  return a job with its result, or abandon it")
	fmt.Fprintln(os.Stderr, "  wait  collect a returned job's result and verify it")
	fmt.Fprintln(os.Stderr, "  watch follow what its holder is doing, until interrupted")
	fmt.Fprintln(os.Stderr, "  run   submit one of the server's own jobs and return")
	fmt.Fprintln(os.Stderr, "  rm    remove one job from the plan; a row that already ended needs --force")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "`"+hint.LsJobs.String()+"` lists every job in flight, and `"+hint.DescribeJob.With("<job>")+"` prints one job's terms.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "One author per job, enforced by the store: a session holding a lease may release")
	fmt.Fprintln(os.Stderr, "paths and end its own job, and nothing else. "+hint.ToolJob.String()+" is the")
	fmt.Fprintln(os.Stderr, "same store through an agent's channel.")
}

// consoleJobLine is where to WATCH a job while it runs, printed under every verb that names
// one. Somebody who wants to know how a worker is doing has two ways to find out, and only
// one of them leaves the worker alone.
//
// An empty id asks for the Jobs view itself, which is what a listing wants.
//
// With no server running there is no origin to build a link against, so the line says how to
// start one instead. Printing the URL anyway would hand a person a page that never loads,
// and a browser error page cannot tell them that nothing is listening rather than that the
// console is broken.
func consoleJobLine(id string) string {
	if globalCfg.Console.Enabled != nil && !*globalCfg.Console.Enabled {
		return ""
	}
	serving, serverVersion := probeConsoleServer()
	if !serving {
		return "console: nothing is serving it; `" + hint.ServerStart.String() + "` to watch this job without interrupting its holder"
	}
	host := mcpAddrString()
	link := console.JobLink(host, id)
	if id == "" {
		link = console.Link(console.LinkOpts{Host: host, Surface: console.JobSurface})
	}
	line := "console: " + link + "\n  " + authHint(link)
	if skew := consoleSkew(serverVersion, version); skew != "" {
		line += "\n  " + skew
	}
	return line
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

// printConsoleJobLine writes that line, and nothing at all when the console is off: a
// suppressed surface has no address, and a bare "console:" is worse than silence.
func printConsoleJobLine(out io.Writer, id string) {
	if line := consoleJobLine(id); line != "" {
		fmt.Fprintln(out, line)
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

func openJobs(root string) (*job.Store, error) {
	cacheDir, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
	if err != nil {
		return nil, err
	}
	return job.NewStore(job.Location{CacheDir: cacheDir, Root: root}), nil
}

// lsJobs is `magus ls jobs`: every job this repository carries, whoever holds it.
func lsJobs(root string, args []string) error {
	rest, err := cmdParse("ls jobs", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus ls jobs [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print every job as a tree, each with its state, model, write-path count, what")
			fmt.Fprintln(os.Stderr, "its fork could prove about its write paths (PROOF) and its check, followed by every")
			fmt.Fprintln(os.Stderr, "pair that claims the same path.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "PROOF is what the checkout looked like when the job was forked: alone (nothing")
			fmt.Fprintln(os.Stderr, "else live was bound there), disjoint, or overlapping.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("magus ls jobs: takes no arguments (got %q)", rest[0])
	}
	store, err := openJobs(root)
	if err != nil {
		return err
	}
	jobs, err := store.List()
	if err != nil {
		return err
	}
	// The list, not the bare rows: the overlaps are derived by the same constructor
	// the magus_job list op and the console's route use, so the three doors cannot
	// disagree about whether two jobs claim one path.
	list := types.NewJobList(jobs).Flag(time.Now().Unix(), globalCfg.Jobs.StaleAfter)

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputName {
		list.Overlaps = overlapFootprints(context.Background(), root, list.Jobs, list.Overlaps)
	}
	switch opts.Format {
	case outputName:
		ids := make([]string, len(list.Jobs))
		for i, one := range list.Jobs {
			ids[i] = one.ID
		}
		return emitNames(ids)
	case outputText:
		printJobTree(os.Stdout, list)
		printConsoleJobLine(os.Stdout, "")
		return nil
	default:
		return emitFormatted(opts, list)
	}
}

func printJobTree(out io.Writer, report types.JobList) {
	if len(report.Jobs) == 0 {
		fmt.Fprintln(out, "No jobs. Declare one with `"+hint.JobFork.With("<job>")+"`, or with the `"+
			hint.ToolJob.String()+"` MCP tool (op=fork), and every worktree of this repository reads it here.")
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
			string(row.lease.Holder.OrSession()), state, orDash(row.lease.Model),
			len(row.lease.WritePaths), orDash(string(row.lease.WriteProof)), orDash(row.lease.Validation))
	}
	_ = w.Flush()

	for _, section := range []struct {
		title string
		ids   []string
	}{
		{"overdue: past the deadline their timeout set, so their writes are denied", report.Overdue},
		{"orphans: live under a root job that has ended", report.Orphans},
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

	if len(report.Overlaps) == 0 {
		return
	}
	fmt.Fprintln(out, "\noverlaps")
	for _, o := range report.Overlaps {
		fmt.Fprintf(out, "  %s and %s claim common ground\n", o.JobA, o.JobB)
		fmt.Fprintf(out, "    %s: %s\n", o.JobA, strings.Join(o.PathsA, ", "))
		fmt.Fprintf(out, "    %s: %s\n", o.JobB, strings.Join(o.PathsB, ", "))
		if line := overlapFootprintLine(o.Footprint); line != "" {
			fmt.Fprintf(out, "    %s\n", line)
		}
	}
}

// overlapFootprints compares what each overlapping pair has actually changed. Only the
// overlaps pay for it: a plan with none reads no VCS at all.
func overlapFootprints(ctx context.Context, root string, rows []types.Job, overlaps []types.JobOverlap) []types.JobOverlap {
	if len(overlaps) == 0 {
		return overlaps
	}
	var driver types.VCSDriver
	if res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{}); err == nil && res.Source != types.VCSSourceDisabled {
		driver = res.VCS
	}
	return job.OverlapFootprints(ctx, driver, root, func(dir string) (string, error) {
		if dir == root {
			return magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
		}
		return magus.ResolveCacheDir(dir)
	}, rows, overlaps)
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
// dependencies, what the workspace itself puts out of reach, and the graph's blast radius
// for each write path, and it prints no procedure: taking the job is `magus job exec`'s
// work to DO, not a paragraph for somebody to follow by hand.
func describeJob(ctx context.Context, root string, args []string) error {
	var gates bool
	pos, err := cmdParse("describe job", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&gates, "gates", false, "Grade this job's completion gates against the evidence magus holds now, and record nothing")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe job <job> [flags]")
			fmt.Fprintln(os.Stderr, "       magus describe job <job> --gates")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print one job's terms: its criteria, write paths, check and dependencies, the paths this")
			fmt.Fprintln(os.Stderr, "workspace puts out of reach, and the graph's blast radius for each write path.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "It renders context and never a status: magus assembles what it holds and you")
			fmt.Fprintln(os.Stderr, "hand it to whoever takes the job, the way `magus diff --prompt` does.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "--gates is the exception, and it is still a READ: it grades this job's completion")
			fmt.Fprintln(os.Stderr, "gates against the evidence magus holds right now and records nothing, so asking")
			fmt.Fprintln(os.Stderr, "never advances a job and never blocks the holder still working on it. It is the")
			fmt.Fprintln(os.Stderr, "same grading `"+hint.JobWait.String()+"` does, so the two cannot disagree.")
			fmt.Fprintln(os.Stderr, "Exit 1 means a gate is unmet, so a caller branches on the status rather than")
			fmt.Fprintln(os.Stderr, "reading the text.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if gates {
		return jobGates(ctx, root, pos)
	}
	if len(pos) != 1 {
		return usagef("magus describe job: requires exactly one job")
	}
	flagRoot := root
	root = resolveRootOrEmpty(root)
	store, err := openJobs(root)
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
	if err := jobRefusesTheGate(ctx, flagRoot, row); err != nil {
		return err
	}

	facts := leaseBoundary(ctx, flagRoot, row, leases)
	facts.Evidence, facts.GraphCold = leaseGraphEvidence(ctx, flagRoot, row.WritePaths)
	brief := job.NewTerms(row, facts)

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		return emitNames([]string{row.ID})
	case outputText:
		fmt.Print(brief.String())
		printConsoleJobLine(os.Stdout, row.ID)
		return nil
	default:
		return emitFormatted(opts, brief)
	}
}

// checkoutBaseToken is the base this checkout is on, in the one form the store compares
// against a job's checkpoint: `<rev>`, or `<rev>+<digest>` when the tree is dirty.
//
// Computed here rather than taken from the caller, which is what makes the divergence
// verdict a fact. It is the same value `magus vcs checkpoint -o name` prints, from the
// same function, so the token a person cites and the token exec records cannot differ.
func checkoutBaseToken(ctx context.Context, root string) (string, error) {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return "", err
	}
	res, err := vcs.Resolve(ctx, ws.Root(), "", ws.VCSOptions())
	if err != nil {
		return "", err
	}
	cp, err := vcs.Checkpoint(ctx, ws.Root(), res, false)
	if err != nil {
		return "", err
	}
	return checkpointToken(cp), nil
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

// forkFlags is the one-job case as flags, and job is the same record --stdin decodes.
// One conversion rather than two paths into the store, so a job typed at a terminal and a
// job piped in are the same declaration.
type forkFlags struct {
	criteria, parent, check, model, checkpoint string
	timeout                                    string
	writePaths, readPaths, denyPaths           listFlag
	dependsOn                                  listFlag
	gates                                      []types.CompletionGate
	readOnly                                   bool
}

func (f forkFlags) row(id string) types.Declaration {
	return types.Declaration{
		SchemaVersion:   types.JobSchemaVersion,
		ID:              id,
		Parent:          f.parent,
		Criteria:        f.criteria,
		Checkpoint:      f.checkpoint,
		WritePaths:      f.writePaths,
		DenyPaths:       f.denyPaths,
		ReadPaths:       f.readPaths,
		DependsOn:       f.dependsOn,
		Check:           f.declaredCheck(),
		CompletionGates: f.gates,
		Model:           f.model,
		ReadOnly:        f.readOnly,
		Timeout:         f.timeout,
		// A row a person declares is one nobody has picked up yet, which is what the
		// state vocabulary already has a word for.
		State: types.StateDeclared,
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
// THE PERSON'S WRITE. An orchestrating agent forks through the magus_job tool; this is the
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
	)
	pos, err := cmdParse("job fork", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&schema, "schema", false, "Print the JSON schema a job must satisfy, and exit")
		fs.BoolVar(&stdin, "stdin", false, "Read one job as JSON on stdin instead of taking it from flags")
		fs.StringVar(&declared.criteria, "criteria", "", "What this job is for and what done means, as prose; the machine-checkable half is the completion gates (--check and every --gate-* flag)")
		fs.StringVar(&declared.timeout, "timeout", "", "Deny this job's writes once this long has passed since the fork (e.g. 45m, 2h); unset means no bound, unless magus.yaml sets jobs.default_timeout")
		fs.StringVar(&declared.parent, "parent", "", "The job this one is forked from")
		fs.StringVar(&declared.checkpoint, "checkpoint", "", "The working state this job is handed, as `magus vcs checkpoint -o name` prints it")
		fs.Var(&declared.writePaths, "write-paths", "A path this job may write; repeatable or comma-separated")
		fs.Var(&declared.denyPaths, "deny-paths", "A path this job may not write, carved out of its write paths; repeatable or comma-separated")
		fs.Var(&declared.readPaths, "read-paths", "A path whose projects this job may read; repeatable or comma-separated (additive: the written paths are readable already)")
		fs.Var(&declared.dependsOn, "depends-on", "A job this one waits on; repeatable or comma-separated")
		fs.StringVar(&declared.check, "check", "", "The one check this job runs, as `<target> <project> [-- args]` (the `magus run` is implied)")
		fs.Var(gateList{kind: types.GateKindCheck, gates: &declared.gates}, "gate-check", "A further check this job must pass, as `<id>=<target> <project>`; repeatable")
		fs.Var(gateList{kind: types.GateKindPaths, gates: &declared.gates}, "gate-paths", "Files this job must have CHANGED, as `<id>=<glob>[,<glob>...]`, proven against its checkpoint; repeatable")
		fs.Var(gateList{kind: types.GateKindPaths, expect: types.ExpectPresent, gates: &declared.gates}, "gate-paths-present", "Files that must EXIST when the job is done, as `<id>=<glob>[,<glob>...]`; repeatable")
		fs.Var(gateList{kind: types.GateKindPaths, expect: types.ExpectAbsent, gates: &declared.gates}, "gate-paths-absent", "Files that must be GONE when the job is done, as `<id>=<glob>[,<glob>...]`; repeatable")
		fs.Var(gateList{kind: types.GateKindSymbol, gates: &declared.gates}, "gate-symbol", "Symbols whose definition this job must have CHANGED, as `<id>=<name>[,<name>...]`; repeatable")
		fs.Var(gateList{kind: types.GateKindSymbol, expect: types.ExpectPresent, gates: &declared.gates}, "gate-symbol-present", "Symbols that must resolve when the job is done, as `<id>=<name>[,<name>...]`; repeatable")
		fs.Var(gateList{kind: types.GateKindSymbol, expect: types.ExpectAbsent, gates: &declared.gates}, "gate-symbol-absent", "Symbols that must resolve NOWHERE when the job is done, as `<id>=<name>[,<name>...]`; repeatable")
		fs.Var(gateList{kind: types.GateKindSymbol, expect: types.ExpectUnreferenced, gates: &declared.gates}, "gate-symbol-unreferenced", "Symbols nothing may reference when the job is done, as `<id>=<name>[,<name>...]`; repeatable")
		fs.StringVar(&declared.model, "model", "", "The model the work was matched to")
		fs.BoolVar(&declared.readOnly, "read-only", false, "A job that gathers evidence and writes nothing")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job fork <job> [flags]")
			fmt.Fprintln(os.Stderr, "       magus job fork --stdin < job.json")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Declare one job: what its holder is handed, where it may write, and the one")
			fmt.Fprintln(os.Stderr, "check it runs. It replaces any job with the same id.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A session holding a lease may only fork a CHILD of its own job, inside its own")
			fmt.Fprintln(os.Stderr, "paths; widening a boundary is the forking session's.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "There is no --state: this declares a NEW job, and one nobody has taken is")
			fmt.Fprintln(os.Stderr, string(types.StateDeclared)+". A holder moves its own job with `"+hint.JobExec.String()+"` and `"+hint.JobExit.String()+"`.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if schema {
		fmt.Print(job.DeclarationSchema)
		return nil
	}

	switch {
	case stdin:
		if len(pos) > 0 {
			return usagef("magus job fork: --stdin reads the id from the record, so %q is one id too many", pos[0])
		}
		if row, err = job.DecodeDeclaration(os.Stdin); err != nil {
			return usagef("magus job fork: %s (`%s` prints the schema it must satisfy)", err, hint.JobFork.With("--schema"))
		}
	case len(pos) != 1:
		return usagef("magus job fork: requires exactly one job id, or --stdin with a job on it")
	default:
		row = declared.row(pos[0])
		if err := row.Validate(); err != nil {
			return usagef("magus job fork: %s", err)
		}
	}

	root = resolveRootOrEmpty(root)
	store, err := openJobs(root)
	if err != nil {
		return err
	}
	plan, err := store.List()
	if err != nil {
		return err
	}
	if err := job.RefuseForkLimits(plan, row.ID, row.Parent, globalCfg.Jobs); err != nil {
		return usagef("magus job fork: %s", err)
	}
	// The reader loads the graph only when a gate names a symbol.
	if err := job.RefuseAmbiguousSymbols(ctx, row.CompletionGates, jobSymbolReader(root)); err != nil {
		return usagef("magus job fork: %s", err)
	}
	candidate := types.Job{ID: row.ID, WritePaths: row.WritePaths}
	if err := job.RefuseDirectoryWritePaths(store, row.ID, candidate); err != nil {
		return usagef("magus job fork: %s", err)
	}
	if err := job.RefuseSharedCheckout(store, plan, row.ID, candidate); err != nil {
		return usagef("magus job fork: %s", err)
	}
	proof := store.WriteProof(plan, row.ID, candidate)
	// A writing job is verified against the diff since its checkpoint, so one forked without
	// a checkpoint could never pass. The fork records this checkout's state instead; where it
	// cannot be read (no VCS here) the row stays without one and wait says why it cannot verify.
	if !row.ReadOnly && row.Checkpoint == "" {
		if token, err := checkoutBaseToken(ctx, root); err == nil {
			row.Checkpoint = token
		}
	}
	declare := job.Declare(row, globalCfg.Jobs.DefaultTimeout)
	stored, err := store.Update(ctx, row.ID, func(u *types.Job) {
		declare(u)
		u.WriteProof = proof
	})
	if err != nil {
		return err
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		return emitNames([]string{stored.ID})
	case outputText:
		fmt.Printf("forked %s, %s, with %d write path(s). Its holder reads the terms with `%s` and takes it with `%s`\n",
			stored.ID, orDash(string(stored.State)), len(stored.WritePaths),
			hint.DescribeJob.With(stored.ID), hint.JobExec.With(stored.ID))
		printConsoleJobLine(os.Stdout, stored.ID)
		return nil
	default:
		return emitFormatted(opts, stored)
	}
}

// jobExec takes the lease on a job in THIS checkout: it binds the marker every
// lease-scoped rule reads, and records the base this tree actually landed on beside the
// checkpoint the job was handed.
//
// Two acts, one verb, because splitting them is what the previous surface did and it left
// the base unrecorded on every job a person took by hand: binding was a CLI verb and
// recording the base was an MCP op, so a holder without the tool simply skipped it and the
// guard then refused its first write for a registration nobody could make.
//
// The base is READ FROM THE CHECKOUT rather than passed in. A holder typing the token it
// believes it is on is a holder reporting a belief; the divergence this records is only
// worth anything if the value comes from the tree.
func jobExec(ctx context.Context, root string, args []string) error {
	var base, session string
	var vacate bool
	pos, err := cmdParse("job exec", args, func(fs *flag.FlagSet) {
		fs.StringVar(&base, "base", "", "The base this checkout landed on, as `magus vcs checkpoint -o name` prints it (default: read from this checkout)")
		fs.StringVar(&session, "session", "", "The session taking the job, as this agent host names it. Several sessions in one checkout each hold their own lease; without it the binding is the whole checkout's, as it was before")
		fs.BoolVar(&vacate, "vacate", false, "Give up the lease this checkout holds, so a later exec can take a different one. A no-op if it holds none.")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job exec <job> [flags]")
			fmt.Fprintln(os.Stderr, "       magus job exec --vacate")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Take the lease on a job here. Every lease-scoped guard and sandbox rule then")
			fmt.Fprintln(os.Stderr, "reads that job's write paths in this checkout, and the base this tree is on is")
			fmt.Fprintln(os.Stderr, "recorded beside the checkpoint the job was handed, with the divergence between")
			fmt.Fprintln(os.Stderr, "them as a fact rather than a refusal.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "With no job, it prints the one this checkout holds.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "--session names the session taking it, so several sessions sharing one checkout")
			fmt.Fprintln(os.Stderr, "each hold their own lease and each gets its own boundary graded. Pass the id this")
			fmt.Fprintln(os.Stderr, "agent host reports to its hooks, or the binding is the whole checkout's.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "--vacate gives that binding up instead of taking one, so the checkout can exec a")
			fmt.Fprintln(os.Stderr, "different job (or the server's next assignment). Refused while the job is still")
			fmt.Fprintln(os.Stderr, "declared or running: walking away from those two would leave the checkout's next")
			fmt.Fprintln(os.Stderr, "write ungraded. A job this checkout already exited, one the store no longer")
			fmt.Fprintln(os.Stderr, "carries, or no binding at all, all vacate cleanly.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if vacate {
		if len(pos) > 0 {
			return usagef("magus job exec --vacate: takes no job; it gives up whichever this checkout holds")
		}
		if strings.TrimSpace(base) != "" {
			return usagef("magus job exec --vacate: --base names nothing to record when there is no job to exec")
		}
		root = resolveRootOrEmpty(root)
		if root == "" {
			return errors.New("magus job exec --vacate: no workspace here: the lease marker lives in a checkout's cache dir, so run from inside one or pass --root <path>")
		}
		cacheDir, cerr := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
		if cerr != nil {
			return fmt.Errorf("magus job exec --vacate: %w", cerr)
		}
		return jobExecVacate(root, job.Checkout{CacheDir: cacheDir, Session: strings.TrimSpace(session)})
	}
	if len(pos) > 1 {
		return usagef("magus job exec: takes at most one job")
	}
	flagRoot := root
	root = resolveRootOrEmpty(root)
	if root == "" {
		return errors.New("magus job exec: no workspace here: the lease marker lives in a checkout's cache dir, so run from inside one or pass --root <path>")
	}
	cacheDir, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
	if err != nil {
		return fmt.Errorf("magus job exec: %w", err)
	}
	here := job.Checkout{CacheDir: cacheDir, Session: strings.TrimSpace(session)}
	if len(pos) == 0 {
		id, err := here.Marker()
		if err != nil {
			return fmt.Errorf("magus job exec: %w", err)
		}
		if id != "" {
			fmt.Printf("this checkout holds the lease on %s\n", id)
			return nil
		}
		fmt.Println("this checkout holds no job")
		return nil
	}
	if err := here.Bind(pos[0]); err != nil {
		return fmt.Errorf("magus job exec: %w", err)
	}
	if strings.TrimSpace(base) == "" {
		// A tree with no readable revision still TAKES the job: binding is what puts every
		// lease-scoped rule in force, and refusing it because there is nothing to compare
		// a base against would leave the holder unguarded over a missing FACT.
		base, _ = checkoutBaseToken(ctx, flagRoot)
	}
	store, err := openJobs(root)
	if err != nil {
		return err
	}
	if strings.TrimSpace(base) == "" {
		fmt.Printf("holding the lease on %s; this checkout reports no revision, so no base was recorded\n", pos[0])
		return nil
	}
	stored, err := store.Exec(ctx, pos[0], base)
	if err != nil {
		return fmt.Errorf("magus job exec: %w", err)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		return emitNames([]string{stored.ID})
	case outputText:
		fmt.Println(job.BaseAdvice(stored))
		fmt.Printf("the lease is bound to %s; read the terms with `%s`\n", root, hint.DescribeJob.With(stored.ID))
		return nil
	default:
		return emitFormatted(opts, stored)
	}
}

// jobExecVacate gives up the lease this session holds here, so a later exec can take a
// different one (or the same one again, which Bind already permitted). It releases only
// this session's binding: a sibling session working in the same checkout keeps its own.
//
// Refused only while the row says work is still IN FLIGHT (declared or running): a
// holder that walks away from those two leaves its next write ungraded from here on,
// the same escape denyLeaseScopedRebind already closes for rebinding outright. Once a
// holder has exited, there is no more of ITS OWN work left to protect against: exited
// counts as live elsewhere (types.JobState.Live) so a rejected wait can send work back
// to the same write paths, but nobody is required to ever run that wait, and a checkout stuck
// on a lease nobody will collect is exactly the bug this flag exists to fix. A row the
// store cannot find or read is treated the same permissive way: nothing here declares a
// boundary left to protect, the same fail-open reading the guard gives an unreadable
// ledger.
func jobExecVacate(root string, here job.Checkout) error {
	id, err := here.Marker()
	if err != nil {
		// A marker that does not read binds no row there is a state to check, and clearing
		// it is the fix the error names.
		if _, verr := here.Vacate(); verr != nil {
			return fmt.Errorf("magus job exec --vacate: %w", verr)
		}
		fmt.Printf("cleared a lease marker that did not read (%v)\n", err)
		return nil
	}
	if id == "" {
		fmt.Println("this checkout holds no job; nothing to vacate")
		return nil
	}
	if store, serr := openJobs(root); serr == nil {
		if rows, lerr := store.List(); lerr == nil {
			if i := slices.IndexFunc(rows, func(j types.Job) bool { return j.ID == id }); i >= 0 {
				if state := rows[i].State; state == types.StateDeclared || state == types.StateRunning {
					return fmt.Errorf("magus job exec --vacate: this checkout holds the lease on %s, and it is still %s."+
						" Exit it first with `%s`, or leave it to the orchestrator: vacating mid-flight would leave your next write ungraded",
						id, state, hint.JobExit.With(id))
				}
			}
		}
	}
	cleared, err := here.Vacate()
	if err != nil {
		return fmt.Errorf("magus job exec --vacate: %w", err)
	}
	fmt.Printf("vacated the lease on %s\n", cleared)
	return nil
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
		fs.BoolVar(&stdin, "stdin", false, "Read this job's result from stdin; without it the job is abandoned")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job exit <job> --stdin < result.json")
			fmt.Fprintln(os.Stderr, "       magus job exit <job>")
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
			fmt.Fprintln(os.Stderr, "With no --stdin the job is ABANDONED and recorded "+string(types.StateNoReturn)+": nobody")
			fmt.Fprintln(os.Stderr, "returned it, which is not the same as returning it and failing.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
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
		return usagef("magus job exit: requires exactly one job")
	}
	flagRoot := root
	root = resolveRootOrEmpty(root)
	store, err := openJobs(root)
	if err != nil {
		return err
	}

	if !stdin {
		stored, aerr := job.Exit(ctx, store, pos[0], nil, nil)
		if aerr != nil {
			return usagef("magus job exit: %s", aerr)
		}
		fmt.Printf("abandoned %s, recorded %s\n", stored.ID, stored.State)
		return nil
	}

	result, err := job.DecodeResult(os.Stdin)
	if err != nil {
		return usagef("magus job exit: %s (`%s` prints the schema it must satisfy)", err, hint.JobExit.With("--schema"))
	}
	// Exit owns the portable evidence snapshot for every declared gate. Keeping the
	// CLI as a decoder/renderer avoids a second lifecycle that silently files only
	// the historical primary check.
	stored, err := job.Exit(ctx, store, pos[0], &result, func(ctx context.Context, ref string) (types.JobAttempt, error) {
		return storedAttempt(ctx, flagRoot, ref)
	})
	if err != nil {
		return usagef("magus job exit: %s", err)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		return emitNames([]string{stored.ID})
	case outputText:
		fmt.Printf("exited %s, recorded %s, with its result filed. Whoever forked it verifies with `%s`\n",
			stored.ID, stored.State, hint.JobWait.With(stored.ID))
		return nil
	default:
		return emitFormatted(opts, stored)
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
// THE VERIFIER IS NEVER THE VERIFIED. A session holding the lease is refused before
// anything is read: a holder that can verify its own job is the loop's one remaining
// self-assessment, and the store would refuse the resulting write anyway, so it is refused
// here where the message can say why.
//
// Two failing statuses, because the caller is a shell step and the two send it somewhere
// different: 2 for a result magus could not read at all (fix the result), 1 for one that
// was read and rejected (the work is not verified).
func jobWait(ctx context.Context, root string, args []string) error {
	var schema, stdin bool
	pos, err := cmdParse("job wait", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&schema, "schema", false, "Print the JSON schema a result must satisfy, and exit")
		fs.BoolVar(&stdin, "stdin", false, "Read the result from stdin instead of from the job, for one that was never filed")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job wait <job>")
			fmt.Fprintln(os.Stderr, "       magus job wait <job> --stdin < result.json")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Verify the result a job was exited with, against the job it was handed: every")
			fmt.Fprintln(os.Stderr, "changed path inside its write paths and outside its denied ones, a change set")
			fmt.Fprintln(os.Stderr, "that is not empty, descendants the store carries, and PASSING output for every")
			fmt.Fprintln(os.Stderr, "completion gate. Evidence must be newer than this job's declaration; each target")
			fmt.Fprintln(os.Stderr, "keeps its own execution timeout. A job that verifies is recorded "+string(types.StatePass)+".")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Exit 1 is a status: the result was read and rejected, and every rule that failed")
			fmt.Fprintln(os.Stderr, "is named. Exit 2 is magus unable to answer: nothing was filed and nothing was")
			fmt.Fprintln(os.Stderr, "piped in, the result would not decode, or the job would not write.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "It checks what is mechanical. Whether the work is GOOD, and whether the job's")
			fmt.Fprintln(os.Stderr, "acceptance criteria are met, stay the reading of whoever forked it.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
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
		return usagef("magus job wait: requires exactly one job")
	}
	flagRoot := root
	root = resolveRootOrEmpty(root)

	store, err := openJobs(root)
	if err != nil {
		return err
	}
	actor, err := store.Actor()
	if err != nil {
		return fmt.Errorf("magus job wait: %w", err)
	}
	if actor.Bound() {
		return fmt.Errorf("magus job wait: this checkout holds the lease on %s, and a holder does not verify its own work."+
			" Exit the job with what you changed and what you ran, and let whoever forked it wait on you", actor.Lease)
	}

	var result *types.JobResult
	if stdin {
		decoded, derr := job.DecodeResult(os.Stdin)
		if derr != nil {
			return usagef("magus job wait: %s (`%s` prints the schema it must satisfy)", derr, hint.JobWait.With("--schema"))
		}
		result = &decoded
	}
	status, err := job.Wait(ctx, store, pos[0], result, func(ctx context.Context, ref string) (types.JobAttempt, error) {
		return storedAttempt(ctx, flagRoot, ref)
	}, job.CheckpointObserver(root, jobSymbolReader(root)))
	if err != nil {
		return usagef("magus job wait: %s", err)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		err = emitNames([]string{status.Job})
	case outputText:
		printJobStatus(os.Stdout, status)
		printConsoleJobLine(os.Stdout, status.Job)
	default:
		err = emitFormatted(opts, status)
	}
	if err != nil || status.Verified {
		return err
	}
	return errSilent{exitCode: 1}
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
			fmt.Fprintln(os.Stderr, "`"+hint.DescribeJob.With("<job>", "--gates")+"` grades what it has finished; this shows what it is doing.")
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("magus job watch: requires exactly one job")
	}
	id := pos[0]
	root = resolveRootOrEmpty(root)
	store, err := openJobs(root)
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
	cacheDir, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
	if err != nil {
		return err
	}

	out := os.Stdout
	printConsoleJobLine(out, id)
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
// value of this surface is skimming a column: a person watching four workers is looking for
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

// storedAttempt is what the output store recorded about the run behind ref: which command
// produced it, and whether it failed. An empty ref, and one that aged out of the cache,
// both report MISSING rather than an error: nobody can reopen either, and that is the fact
// verification turns on.
//
// The DESCRIPTOR, not the bytes. Verification reads the run's identity and its exit status,
// both of which are metadata, and a captured log is as large as the target was noisy.
func storedAttempt(ctx context.Context, root, ref string) (types.JobAttempt, error) {
	if strings.TrimSpace(ref) == "" {
		return types.JobAttempt{}, nil
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return types.JobAttempt{}, err
	}
	switch d, err := m.OutputDescriptorByRef(ref); {
	case err == nil:
		return types.JobAttempt{
			Found: true, Ref: ref, Project: d.Project, Target: d.Target, Spell: d.Spell, Failed: d.Failed, TimestampMs: d.TimestampMs,
		}, nil
	case errors.Is(err, fs.ErrNotExist):
		return types.JobAttempt{}, nil
	default:
		return types.JobAttempt{}, err
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
		fmt.Fprintf(out, "completion gate %s: %s", gate.ID, state)
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
	if len(s.Risks) > 0 {
		fmt.Fprintln(out, "unresolved risks its holder reported")
		for _, risk := range s.Risks {
			fmt.Fprintf(out, "  %s\n", risk)
		}
	}
}

// printJobFootprint writes the declarations the job's diff landed in, one line each. An
// unknown footprint says why in one line: silence would read as a job that touched nothing.
func printJobFootprint(out io.Writer, s job.Status) {
	const heading = "footprint, reported and never graded"
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
		fmt.Fprintf(out, "  %s\n", line)
	}
}

// leaseAffinityCommits is how far back the co-change scan reads. Wide enough that a
// coupling has to be a habit rather than one refactor, narrow enough that a boundary
// two owners ago does not warn about a partition nobody runs any more.
const leaseAffinityCommits = 200

// leaseBoundary derives what the WORKSPACE says about one lease: the projects its write
// paths reach, the paths its own declarations put out of reach, and the projects that
// change alongside the leased ones.
//
// This is the half a hand-written ledger row cannot carry. An orchestrator writes down
// the deny paths it REMEMBERED; the generated outputs of every project a lease
// invalidates, and the paths a sibling lease is holding right now, hold whether anybody
// remembered them or not.
//
// A read-only lease has no write set, so it gets none of this: the skill puts such a row
// outside the collision analysis entirely.
//
// A workspace that will not load DEGRADES rather than failing, the way the graph evidence
// already does: the row alone carries the criteria, the boundary and the check, and a worker
// in a tree whose magusfile is mid-edit is exactly who needs to read them.
func leaseBoundary(ctx context.Context, root string, row types.Job, leases []types.Job) job.TermsFacts {
	if len(row.WritePaths) == 0 {
		return job.TermsFacts{}
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return job.TermsFacts{WorkspaceCold: true}
	}
	affected, err := m.AffectedFromPaths(ctx, row.WritePaths)
	if err != nil {
		return job.TermsFacts{WorkspaceCold: true}
	}
	derived := generatedBoundary(m, affected.Affected, row.WritePaths)
	derived = append(derived, leasedBoundary(row, leases)...)
	derived = append(derived, sharedBoundary(m, affected.Seed)...)
	return job.TermsFacts{
		Projects:         affected.Affected,
		DerivedDenyPaths: derived,
		Affinity:         leaseAffinity(ctx, m, affected.Seed),
	}
}

// generatedBoundary is the declared output globs, across every project the lease
// invalidates, that land INSIDE its write paths.
//
// The intersection is what makes this a boundary rather than an inventory. This
// workspace declares around a hundred output globs; listing them all buries the two or
// three a given worker could actually hand-edit, and the worker is already fenced out of
// everything beyond its write paths. What it cannot know without being told is that a
// file it legitimately owns the directory of is generated.
//
// The AFFECTED set rather than the seeds, because a project writes outputs into trees it
// does not own: a glob from a downstream project can land in this lease's paths.
func generatedBoundary(m *magus.Magus, projects, owned []string) []job.TermsBoundary {
	var out []job.TermsBoundary
	seen := map[string]bool{}
	for _, path := range projects {
		p := m.Get(path)
		if p == nil {
			continue
		}
		for _, glob := range p.AllOutputs() {
			rooted := types.RootGlob(p.Path, glob)
			if seen[rooted] || !slices.ContainsFunc(owned, func(o string) bool { return types.PathsIntersect(o, rooted) }) {
				continue
			}
			seen[rooted] = true
			out = append(out, job.TermsBoundary{
				Path:   rooted,
				Reason: fmt.Sprintf("generated by %s: regenerate it, never hand-edit", types.ProjectLabel(p.Path, p.Name)),
			})
		}
	}
	return out
}

// leasedBoundary is every path another LIVE lease claims. A terminal row claims nothing:
// that is the same rule the overlap report follows, and the reason a worker releasing a
// path early lets a waiter start against it.
//
// An ANCESTOR claims nothing against its descendant either: a child is forked inside its
// parent's boundary, so listing the parent's paths would put the child's own out of reach.
func leasedBoundary(row types.Job, leases []types.Job) []job.TermsBoundary {
	ancestors := types.JobAncestors(leases, row.ID)
	var out []job.TermsBoundary
	for _, other := range leases {
		if _, blocked := types.JobBlockedOn(leases, other); other.ID == row.ID || !other.State.Live() || blocked ||
			slices.ContainsFunc(ancestors, func(a types.Job) bool { return a.ID == other.ID }) {
			continue
		}
		for _, p := range other.WritePaths {
			out = append(out, job.TermsBoundary{
				Path:   p,
				Reason: fmt.Sprintf("owned by live lease %s: coordinate, never work around", other.ID),
			})
		}
	}
	return out
}

// sharedBoundary is the files in the workspace root and the lease's own project
// directories that decide what every tool DOES: dependency locks, rule sets, toolchain
// pins, the workspace config, and the files magus maintains itself.
//
// One owner each, which is the skill's collision rule. They are named from what is on
// disk rather than from a list of every manifest a monorepo could hold, so the boundary
// describes this workspace instead of a catalogue.
func sharedBoundary(m *magus.Magus, seeds []string) []job.TermsBoundary {
	var out []job.TermsBoundary
	seen := map[string]bool{}
	for _, dir := range append([]string{"."}, seeds...) {
		entries, err := os.ReadDir(filepath.Join(m.Root(), filepath.FromSlash(dir)))
		if err != nil {
			continue
		}
		for _, e := range entries {
			rel := path.Join(dir, e.Name())
			if e.IsDir() || seen[rel] {
				continue
			}
			reason := sharedReason(rel, e.Name())
			if reason == "" {
				continue
			}
			seen[rel] = true
			out = append(out, job.TermsBoundary{Path: rel, Reason: reason})
		}
	}
	return out
}

// sharedReason says why a file has one owner, or "" for one that does not.
func sharedReason(rel, name string) string {
	switch {
	case name == config.Filename:
		return "workspace configuration: changing it changes the plan every lease is running"
	case types.IsMagusMaintained(rel):
		return "magus maintains this file itself"
	case types.LooksLikeBuildInput(rel):
		return "shared build input: it decides what the tools do, so it has one owner"
	default:
		return ""
	}
}

// leaseAffinity reports the undeclared couplings between a leased project and one outside
// the lease, loudest first.
//
// A pair INSIDE the lease is dropped: two projects one worker owns move together by
// assignment, and saying so is not evidence about anything. A pair that declares its
// dependency is dropped for the reason job.TermsAffinity documents.
//
// Best-effort. A repository with no readable history yields nothing, and a partition
// decided without this evidence is the ordinary case rather than a failure.
func leaseAffinity(ctx context.Context, m *magus.Magus, seeds []string) []job.TermsAffinity {
	if len(seeds) == 0 {
		return nil
	}
	out, err := m.Affinity(ctx, types.InsightOptions{Commits: leaseAffinityCommits})
	if err != nil {
		return nil
	}
	var pairs []job.TermsAffinity
	for _, pair := range out.Pairs {
		if !pair.Hidden {
			continue
		}
		mine, theirs := pair.A, pair.B
		if !slices.Contains(seeds, mine) {
			mine, theirs = pair.B, pair.A
		}
		if !slices.Contains(seeds, mine) || slices.Contains(seeds, theirs) {
			continue
		}
		pairs = append(pairs, job.TermsAffinity{Project: mine, With: theirs, Commits: pair.Count})
	}
	return pairs
}

// briefRefusesTheGate reports why this row must not be briefed, or nil to render it.
//
// The ONE verdict this command makes, and it is here rather than in job.Terms because
// the brief renders context and never a verdict. The gate runs once, in the orchestrator's
// tree, after every unit lands; seven workers ran the whole pipeline concurrently on one
// machine (2026-09-09) because every hand-typed brief ended with it. The guard already
// denies the gate to a worker that binds a narrower row, and this closes the half the
// guard cannot reach: a row whose validation IS the gate, which the guard reads as a lease
// that legitimately owns it.
//
// A COMPOSITE reaching the gate is refused too. Naming the pipeline indirectly buys the
// same seven concurrent runs, and it is the likelier mistake once this refuses the obvious
// spelling.
func jobRefusesTheGate(ctx context.Context, root string, row types.Job) error {
	fix := fmt.Sprintf(" The gate runs ONCE, in the forking session's tree, after every job lands."+
		" Give this job the narrowest target covering its paths (`%s` decomposes what the gate chains) with `%s`, then ask for the terms again.",
		hint.DescribeTarget.With(types.TargetCI+" <project>"), hint.JobFork)

	if source, owns := guard.LeaseGateSource(row); owns {
		return fmt.Errorf("magus describe job: job %s is assigned %s, which names the `%s` gate.%s", row.ID, source, types.TargetCI, fix)
	}
	if chain := validationReachesGate(ctx, root, row.Validation); len(chain) > 0 {
		return fmt.Errorf("magus describe job: job %s is assigned %q, and that target reaches the `%s` gate through %s.%s",
			row.ID, row.Validation, types.TargetCI, strings.Join(chain, " -> "), fix)
	}
	return nil
}

// validationReachesGate walks the ctx.needs graph from each target a validation field
// names and returns the first chain that arrives at the gate, or nil.
//
// WITHIN one project's graph. A cross-project edge is a ref rather than a bare node name,
// so the walk stops at one instead of guessing which project it meant; that under-reports
// and never over-reports, which is the direction a refusal has to fail in. A workspace
// that cannot be inspected also reports nothing: this rule must not be the reason a brief
// is unavailable.
func validationReachesGate(ctx context.Context, root, validation string) []string {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return nil
	}
	graph, err := ws.TargetGraph(ctx)
	if err != nil {
		return nil
	}
	for _, word := range strings.Fields(validation) {
		t, err := types.ParseTarget(word)
		if err != nil {
			continue
		}
		for _, p := range graph.Projects {
			deps := map[string][]string{}
			for _, n := range p.Nodes {
				deps[n.Name] = n.Dependencies
			}
			if chain := chainToGate(t.Name, deps); len(chain) > 0 {
				return chain
			}
		}
	}
	return nil
}

// chainToGate is the path from start to the gate through a project's target dependencies,
// or nil when there is none. Depth-first with a visited set, so a cyclic graph terminates
// rather than being trusted to be acyclic: `magus describe graph` reports cycles instead
// of rejecting them, so one can reach here.
func chainToGate(start string, deps map[string][]string) []string {
	seen := map[string]bool{}
	var walk func(name string) []string
	walk = func(name string) []string {
		if seen[name] {
			return nil
		}
		seen[name] = true
		if name == types.TargetCI {
			return []string{name}
		}
		for _, next := range deps[name] {
			if chain := walk(next); len(chain) > 0 {
				return append([]string{name}, chain...)
			}
		}
		return nil
	}
	// A start nobody declares is not a chain of length one: every bare word in the
	// validation field reaches here, project paths and flags included.
	if _, ok := deps[start]; !ok {
		return nil
	}
	return walk(start)
}

// leaseGraphEvidence resolves each write path against the knowledge graph, one line per
// path the graph knows. cold reports a graph that would not load at all.
//
// A path the graph cannot resolve is skipped SILENTLY, because most write paths are
// ordinary source directories the containment tree does not carry, and a "no node" line
// per path would bury the ones that do resolve. A cold graph is different and is
// reported once: "not asked" must not read as "nothing depends on this".
func leaseGraphEvidence(ctx context.Context, root string, paths []string) (evidence []job.TermsEvidence, cold bool) {
	if len(paths) == 0 {
		return nil, false
	}
	g, err := loadKnowledgeGraph(ctx, root, false, false, false)
	if err != nil {
		return nil, true
	}
	for _, p := range paths {
		if e, ok := pathEvidence(g, p); ok {
			evidence = append(evidence, e)
		}
	}
	return evidence, false
}

// pathEvidence answers for one declared path, trying the node ids a path can carry in the
// containment tree.
//
// It accepts only a node whose id IS the ref it asked for. Explain resolves a bare name
// fuzzily, which is right for a person typing `magus explain build` and wrong here: asked
// for "cmd/magus" it answered target:.:release-sign, and a brief that hands a worker the
// blast radius of an unrelated node is worse than one that stays quiet.
func pathEvidence(g *knowledge.Graph, declared string) (job.TermsEvidence, bool) {
	for _, ref := range []string{types.KindDir + ":" + declared, types.KindFile + ":" + declared, declared} {
		out, ok := g.Explain(ref)
		if !ok || out.Node.ID != ref {
			continue
		}
		return job.TermsEvidence{Path: declared, Node: out.Node.ID, BlastRadius: out.BlastRadius}, true
	}
	return job.TermsEvidence{}, false
}

// gateList collects `--gate-paths` and `--gate-check`, each `<id>=<spec>` and repeatable.
//
// Two flags rather than one taking a kind, because the spec after the `=` has a different
// shape per kind and a single flag would have to name the kind inside its own value. A
// reader would then be typing `--gate done=paths:db/**` where the word `paths` is neither
// the id nor the spec, which is the shape that gets mistyped.
type gateList struct {
	kind   types.GateKind
	expect types.GateExpect
	gates  *[]types.CompletionGate
}

func (g gateList) String() string {
	if g.gates == nil {
		return ""
	}
	var ids []string
	for _, gate := range *g.gates {
		if gate.Kind == g.kind {
			ids = append(ids, gate.ID)
		}
	}
	return strings.Join(ids, ",")
}

// Set parses one `<id>=<spec>`. A spec that does not parse is carried through rather than
// refused here, so types.Declaration.Validate stays the one place a declaration is
// refused: the same rule declaredCheck follows.
func (g gateList) Set(value string) error {
	id, spec, ok := strings.Cut(value, "=")
	if !ok {
		return fmt.Errorf("a gate is `<id>=<spec>` and %q names no id", value)
	}
	gate := types.CompletionGate{ID: strings.TrimSpace(id), Kind: g.kind, Expect: g.expect}
	if g.kind == types.GateKindCheck {
		// A check is one `<target> <project>`, not a list, and a spec that does not parse
		// is carried through rather than refused here so types.Declaration.Validate stays
		// the one place a declaration is refused: the same rule declaredCheck follows.
		parsed, err := types.ParseLeaseCheck(spec)
		if err != nil {
			gate.Check = types.LeaseCheck{Target: spec}
		} else {
			gate.Check = parsed
		}
		*g.gates = append(*g.gates, gate)
		return nil
	}
	var items listFlag
	if err := items.Set(spec); err != nil {
		return fmt.Errorf("gate %q: %w", gate.ID, err)
	}
	if g.kind == types.GateKindPaths {
		gate.Paths = items
	} else {
		gate.Symbols = items
	}
	*g.gates = append(*g.gates, gate)
	return nil
}

// jobSymbolReader loads the knowledge graph ONCE and answers every symbol a job's gates
// name from it.
//
// Lazy and memoized because a verification reads several names and the graph is the
// expensive part; loading per name would reopen it once per symbol. A load failure is
// remembered too, so a verification does not retry a graph that is not there once per
// gate and report the same failure five times.
func jobSymbolReader(root string) job.SymbolReader {
	var (
		once   sync.Once
		loaded job.SymbolReader
	)
	return func(ctx context.Context, name string) (job.SymbolFact, bool) {
		once.Do(func() {
			g, err := loadKnowledgeGraphForRefs(ctx, root, false, "")
			if err != nil {
				return
			}
			loaded = job.GraphSymbols(g)
		})
		if loaded == nil {
			return job.SymbolFact{}, false
		}
		return loaded(ctx, name)
	}
}

// jobGates is `magus describe job <job> --gates`: each completion gate graded against the
// evidence magus holds right now.
//
// A READ. It records nothing, so an orchestrator may ask while the holder is still
// working, and asking never advances a job the way `job wait` does. Exit 1 when a gate is
// unmet, so a script can branch without parsing the text; exit 0 means every gate this
// job declared is satisfied, which is not the same as the job being recorded pass.
func jobGates(ctx context.Context, root string, pos []string) error {
	if len(pos) != 1 {
		return usagef("magus describe job --gates: requires exactly one job")
	}
	root = resolveRootOrEmpty(root)
	store, err := openJobs(root)
	if err != nil {
		return err
	}
	status, err := job.GradeGates(ctx, store, pos[0], job.CheckpointObserver(root, jobSymbolReader(root)))
	if err != nil {
		return usagef("magus describe job --gates: %s", err)
	}
	if rows, lerr := store.List(); lerr == nil && gradesSymbols(rows, pos[0]) {
		status.StaleIndexes = staleIndexProjects(ctx, root)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		// The unmet gates, one id per line: what a caller filters for, and empty when
		// every gate is met.
		var unmet []string
		for _, gate := range status.Gates {
			if !gate.Verified {
				unmet = append(unmet, gate.ID)
			}
		}
		err = emitNames(unmet)
	case outputText:
		job.RenderGates(os.Stdout, status)
	default:
		err = emitFormatted(opts, status)
	}
	if err != nil || status.Verified {
		return err
	}
	return errSilent{exitCode: 1}
}

// gradesSymbols reports whether grading id reads the symbol graph: a symbol gate of its
// own, or one inherited from an ancestor.
func gradesSymbols(rows []types.Job, id string) bool {
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	if i < 0 {
		return false
	}
	for _, r := range append([]types.Job{rows[i]}, types.JobAncestors(rows, id)...) {
		if slices.ContainsFunc(r.CompletionGates, func(g types.CompletionGate) bool { return g.Resolve().Kind == types.GateKindSymbol }) {
			return true
		}
	}
	return false
}

// jobDelete is `magus job rm <job>`: take one row out of the plan.
//
// Named rm rather than delete because that is the verb a person types for this everywhere
// else, and the surface it sits beside (fork, exec, exit, wait) is the shell's own
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
			fmt.Fprintln(os.Stderr, "`"+hint.ToolJob.String()+"` op=clear, which drops every row in the repository.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "This is not how a job ENDS. `"+hint.JobExit.String()+"` records what happened and")
			fmt.Fprintln(os.Stderr, "leaves the row as the account of it; rm is for a row that should never have been")
			fmt.Fprintln(os.Stderr, "written. A row that already ended is refused unless --force, because deleting it")
			fmt.Fprintln(os.Stderr, "destroys the only record that the work ran.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The dropped rows are archived beside the plan first, so this is recoverable.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("magus job rm: requires exactly one job")
	}
	store, err := openJobs(resolveRootOrEmpty(root))
	if err != nil {
		return err
	}
	dropped, err := store.Delete(ctx, pos[0], force)
	if err != nil {
		return usagef("magus job rm: %s", err)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		return emitNames([]string{dropped.ID})
	case outputText:
		fmt.Printf("removed %s (%s); the plan it was part of is archived beside it\n", dropped.ID, orDash(string(dropped.State)))
		return nil
	default:
		return emitFormatted(opts, dropped)
	}
}
