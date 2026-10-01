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
	"github.com/egladman/magus/internal/cache"
	internalci "github.com/egladman/magus/internal/ci"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file/watch"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/queue"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/sessions"
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
// BOTH CHANNELS WRITE, and the store is what keeps the book honest. The client MCP
// tool (magus\job) is the agent's channel and this verb is the person's; they reach the same store and
// the same rules, so a job still has one author (internal/job.authorizeRow enforces it)
// without a capability existing for agents that a person does not have.
func jobCmd(ctx context.Context, root string, args []string) error {
	if len(args) == 0 {
		jobUsage()
		return usagef("magus job: requires a subcommand (fork, apply, exec, exit, wait, run, rm or prune)")
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
		return usagef("magus job: unknown subcommand %q (want fork, apply, exec, exit, wait, watch, run, rm or prune; `%s` lists what is in flight)", args[0], hint.LsJobs)
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
	return job.NewStore(job.Location{CacheDir: cacheDir, Root: root, Outputs: outputsIn}), nil
}

// outputsIn resolves a ref against the output store of the checkout at root. That
// checkout's own magus.yaml places its cache dir, and loadMagus cannot open a second root.
func outputsIn(root string) job.AttemptResolver {
	return func(_ context.Context, ref string) (types.JobAttempt, error) {
		if strings.TrimSpace(ref) == "" {
			return types.JobAttempt{}, nil
		}
		dir, err := magus.ResolveCacheDir(root)
		if err != nil {
			return types.JobAttempt{}, err
		}
		switch d, err := cache.NewOutputStore(dir).DescriptorByRef(ref); {
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
}

// lsJobs is `magus ls jobs`: every job this repository carries, whoever holds it.
func lsJobs(root string, args []string) error {
	var all bool
	rest, err := cmdParse("ls jobs", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&all, "all", false, "List every in-flight change, not only yours")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus ls jobs [--all] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print every job as a tree, each with its state, model, write-path count, what")
			fmt.Fprintln(os.Stderr, "its fork could prove about its write paths (PROOF) and its check, followed by every")
			fmt.Fprintln(os.Stderr, "pair that claims the same path.")
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
	// magus\job.list and the console's route use, so the three doors cannot
	// disagree about whether two jobs claim one path.
	list := types.NewJobList(jobs).Flag(time.Now().Unix(), globalCfg.Jobs.StaleAfter)
	ctx := context.Background()
	list, me, err := joinInflight(ctx, root, store, list)
	if err != nil {
		return err
	}
	// Nothing names a caller with no lease and no login, so none of the changes is theirs
	// and the list is everyone's.
	all = all || !me.Known()

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputName {
		list.Overlaps = overlapFootprints(ctx, root, list.Jobs, list.Overlaps)
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
		fmt.Fprintln(os.Stdout)
		printInflight(os.Stdout, list, all)
		printConsoleJobLine(os.Stdout, "")
		return nil
	default:
		list.Changes = inflightScope(list.Changes, all)
		return emitFormatted(opts, list)
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
			string(row.lease.Holder.OrSession()), state, orDash(row.lease.Model),
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
	return job.OverlapFootprints(ctx, driver, rows, overlaps)
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
// holds now, and it prints no procedure: taking the job is `magus job exec`'s work to DO,
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
			fmt.Fprintln(os.Stderr, "hand it to whoever takes the job, the way `magus diff --prompt` does. Grading a goal is")
			fmt.Fprintln(os.Stderr, "still a READ: it records nothing, so asking never advances a job and never blocks the")
			fmt.Fprintln(os.Stderr, "holder still working on it. It is the same grading `"+hint.JobWait.String()+"` does, so the")
			fmt.Fprintln(os.Stderr, "two cannot disagree.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Example:")
			fmt.Fprintln(os.Stderr, "  magus describe job refactor/pricing")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("magus describe job: requires exactly one job")
	}
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
	if err := jobRefusesTheGate(ctx, root, row); err != nil {
		return err
	}

	facts := leaseBoundary(ctx, root, row, leases)
	facts.Evidence, facts.GraphCold = leaseGraphEvidence(ctx, root, row.WritePaths)
	brief := job.NewTerms(row, facts)

	status, err := job.GradeGates(ctx, store, row.ID, jobObserver(root))
	if err != nil {
		return usagef("magus describe job: %s", err)
	}
	if gradesSymbols(leases, row.ID) {
		status.StaleIndexes = staleIndexProjects(ctx, root)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		return emitNames([]string{row.ID})
	case outputText:
		fmt.Print(brief.String())
		job.RenderGates(os.Stdout, status)
		printJobEntries(os.Stdout, row.Entries)
		joined, _, err := joinInflight(ctx, root, store, types.JobList{Jobs: leases})
		if err != nil {
			return err
		}
		printJobChange(os.Stdout, joined.Changes, row.ID)
		printConsoleJobLine(os.Stdout, row.ID)
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
	)
	pos, err := cmdParse("job fork", args, func(fs *flag.FlagSet) {
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
		fs.StringVar(&declared.model, "model", "", "The model the work was matched to")
		fs.BoolVar(&declared.readOnly, "read-only", false, "A job that gathers evidence and writes nothing")
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
			fmt.Fprintln(os.Stderr, "writes is refused without a check or a goal; a read-only one is exempt.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A session holding a lease may only fork a CHILD of its own job, inside its own")
			fmt.Fprintln(os.Stderr, "paths; widening a boundary is the forking session's.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A --stdin record carrying only id and `enter` ENTERS a live job instead: it")
			fmt.Fprintln(os.Stderr, "acknowledges one write into that path by somebody other than its holder, and the")
			fmt.Fprintln(os.Stderr, "job records it. The guard lets that one write through once the holder has been")
			fmt.Fprintln(os.Stderr, "idle for a minute. A job takes two entries; past that, resume its holder.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "There is no --state: this declares a NEW job, and one nobody has taken is")
			fmt.Fprintln(os.Stderr, string(types.StateDeclared)+". A holder moves its own job with `"+hint.JobExec.String()+"` and `"+hint.JobExit.String()+"`.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Example:")
			fmt.Fprintln(os.Stderr, "  magus job fork refactor/pricing --write-paths pricing/total.sh --check 'test .'")
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
	if row.Enter != "" {
		return jobEnter(ctx, store, row.ID, row.Enter)
	}
	var probe types.Job
	row.Apply(&probe)
	probe.ID = row.ID
	if err := job.RefuseUngraded(probe); err != nil {
		return usagef("magus job fork: %s", err)
	}
	plan, err := store.List()
	if err != nil {
		return err
	}
	if err := job.RefuseForkLimits(plan, row.ID, row.Parent, globalCfg.Jobs); err != nil {
		return usagef("magus job fork: %s", err)
	}
	// The reader loads the graph only when a gate names a symbol.
	if err := job.RefuseAmbiguousSymbols(ctx, row.Goals, jobSymbolReader(root)); err != nil {
		return usagef("magus job fork: %s", err)
	}
	candidate := types.Job{ID: row.ID, WritePaths: row.WritePaths, Parent: row.Parent, DependsOn: row.DependsOn}
	if err := job.RefuseDirectoryWritePaths(store, row.ID, candidate); err != nil {
		return usagef("magus job fork: %s", err)
	}
	if err := job.RefuseUngradableClaims(ctx, store, row.ID, candidate); err != nil {
		return usagef("magus job fork: %s", err)
	}
	if err := job.RefuseSharedCheckout(store, plan, row.ID, candidate); err != nil {
		return usagef("magus job fork: %s", err)
	}
	if err := job.RefuseUnorderedFileShare(ctx, store, plan, row.ID, candidate); err != nil {
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

// jobEnter records an entry into job id's path rel, the --stdin record's other shape.
func jobEnter(ctx context.Context, store *job.Store, id, rel string) error {
	stored, err := store.Enter(ctx, id, rel)
	if err != nil {
		return usagef("magus job fork: %s", err)
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		return emitNames([]string{stored.ID})
	case outputText:
		fmt.Println(job.EntryAdvice(stored, rel))
		printConsoleJobLine(os.Stdout, stored.ID)
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
// The base is READ FROM THE CHECKOUT rather than passed in. A holder typing the token it
// believes it is on is a holder reporting a belief; the divergence this records is only
// worth anything if the value comes from the tree.
func jobExec(ctx context.Context, root string, args []string) error {
	var base string
	pos, err := cmdParse("job exec", args, func(fs *flag.FlagSet) {
		fs.StringVar(&base, "base", "", "The base this checkout landed on, as `magus vcs checkpoint -o name` prints it (default: read from this checkout)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job exec <job> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Take a job here: record the base this tree is on beside the checkpoint the job")
			fmt.Fprintln(os.Stderr, "was handed, with the divergence between them as a fact rather than a refusal,")
			fmt.Fprintln(os.Stderr, "and the checkout it was taken in.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The guard hook binds the caller to the job when it lets this command through,")
			fmt.Fprintln(os.Stderr, "keyed on the session and subagent its agent host names, so every lease-scoped")
			fmt.Fprintln(os.Stderr, "rule then grades that caller and nobody else. A host that names neither binds")
			fmt.Fprintln(os.Stderr, "this checkout. A subagent whose spawn the guard attributed to the job is bound")
			fmt.Fprintln(os.Stderr, "already, and exec only records its base.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Taking a different job is refused while the one held is still declared or")
			fmt.Fprintln(os.Stderr, "running; once it has exited or ended, exec takes the next one.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Example:")
			fmt.Fprintln(os.Stderr, "  magus job exec refactor/pricing   # in the worktree you will edit")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("magus job exec: requires exactly one job (`%s` lists them)", hint.LsJobs)
	}
	root = resolveRootOrEmpty(root)
	if root == "" {
		return errors.New("magus job exec: no workspace here: run from inside one or pass --root <path>")
	}
	if strings.TrimSpace(base) == "" {
		base, _ = checkoutBaseToken(ctx, root)
	}
	if strings.TrimSpace(base) == "" {
		return fmt.Errorf("magus job exec: this checkout reports no revision, so there is no base to record for %s;"+
			" pass the one you are on with --base", pos[0])
	}
	store, err := openJobs(root)
	if err != nil {
		return err
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
		fmt.Printf("took %s in %s; read the terms with `%s`\n", stored.ID, root, hint.DescribeJob.With(stored.ID))
		return nil
	default:
		return emitFormatted(opts, stored)
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
			fmt.Fprintln(os.Stderr, "returned it, which is not the same as returning it and failing. To end every job")
			fmt.Fprintln(os.Stderr, "nobody is working in one call, use `magus job prune`.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Example:")
			fmt.Fprintln(os.Stderr, "  magus job exit refactor/pricing --stdin < result.json")
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
		return storedAttempt(ctx, root, ref)
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
	pos, err := cmdParse("job wait", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&schema, "schema", false, "Print the JSON schema a result must satisfy, and exit")
		fs.BoolVar(&stdin, "stdin", false, "Read the result from stdin instead of from the job, for one that was never filed")
		fs.BoolVar(&integration, "integration", false, "Grade only the job's check goals against the runs the --stdin result names in THIS checkout, and record that beside its state")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job wait <job>")
			fmt.Fprintln(os.Stderr, "       magus job wait <job> --stdin < result.json")
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
	root = resolveRootOrEmpty(root)

	store, err := openJobs(root)
	if err != nil {
		return err
	}
	rows, err := store.List()
	if err != nil {
		return err
	}
	if actor := store.Actor(); !actor.Verifies(rows, pos[0]) {
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
	if integration {
		if result == nil {
			return usagef("magus job wait: --integration grades runs in this checkout, and only a --stdin result can name them")
		}
		return jobWaitIntegration(ctx, store, root, pos[0], *result)
	}
	status, err := job.Wait(ctx, store, pos[0], result, func(ctx context.Context, ref string) (types.JobAttempt, error) {
		return storedAttempt(ctx, root, ref)
	}, jobObserver(root))
	if err != nil {
		return usagef("magus job wait: %s", err)
	}
	if rows, err := store.List(); err == nil {
		status.Entries = job.EntriesOf(rows, status.Job)
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
	grade, err := job.WaitIntegration(ctx, store, id, result, func(ctx context.Context, ref string) (types.JobAttempt, error) {
		return storedAttempt(ctx, root, ref)
	}, checkout)
	if err != nil {
		return usagef("magus job wait: %s", err)
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		err = emitNames([]string{id})
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
	if len(s.Risks) > 0 {
		fmt.Fprintln(out, "unresolved risks its holder reported")
		for _, risk := range s.Risks {
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
			rooted := glob.Root(p.Path)
			if seen[rooted.String()] || !slices.ContainsFunc(owned, func(o string) bool { return types.PathsIntersect(o, rooted.Pattern) }) {
				continue
			}
			seen[rooted.String()] = true
			reason := fmt.Sprintf("generated by %s: regenerate it, never hand-edit", types.ProjectLabel(p.Path, p.Name))
			if len(rooted.Except) > 0 {
				// The carved-out files are hand-maintained, and a worker must not read
				// the pattern as a fence around them.
				reason = fmt.Sprintf("generated by %s, except %s: regenerate it, never hand-edit",
					types.ProjectLabel(p.Path, p.Name), strings.Join(rooted.Except, ", "))
			}
			out = append(out, job.TermsBoundary{Path: rooted.Pattern, Reason: reason})
		}
	}
	return out
}

// leasedBoundary is every path another [job.Editing] lease claims. A terminal or exited
// row claims nothing: that is the same rule the overlap report follows, and the reason a
// worker releasing a path early lets a waiter start against it.
//
// An ANCESTOR claims nothing against its descendant either: a child is forked inside its
// parent's boundary, so listing the parent's paths would put the child's own out of reach.
func leasedBoundary(row types.Job, leases []types.Job) []job.TermsBoundary {
	ancestors := types.JobAncestors(leases, row.ID)
	var out []job.TermsBoundary
	for _, other := range leases {
		if _, blocked := types.JobBlockedOn(leases, other); other.ID == row.ID || !job.Editing(other) || blocked ||
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

// jobObserver is the checkpoint observer plus, for a job with a check gate on ci, the
// newest green ci gate on this checkout's branch and the tier of the change since it.
func jobObserver(root string) job.Observer {
	checkpoint := job.CheckpointObserver(root, jobSymbolReader(root))
	return func(ctx context.Context, row types.Job) (job.Observed, error) {
		seen, err := checkpoint(ctx, row)
		if err != nil || root == "" || !gatesOnCI(row) {
			return seen, err
		}
		seen.GreenGate = latestGreenGate(ctx, root)
		return seen, nil
	}
}

func gatesOnCI(row types.Job) bool {
	return slices.ContainsFunc(row.EffectiveGoals(), func(g types.CompletionGate) bool {
		return g.Kind == types.GateKindCheck && g.Check.Target == types.TargetCI
	})
}

// latestGreenGate is the newest green ci gate on root's branch and the tier of the change
// since it. It is zero when there is none, a merge lies between it and HEAD, or the
// change cannot be assessed, the same conditions the redundancy check declines on.
func latestGreenGate(ctx context.Context, root string) job.GreenGate {
	m, err := loadMagus(ctx, root)
	if err != nil {
		return job.GreenGate{}
	}
	res, err := vcs.Resolve(ctx, m.Root(), "", m.VCSOptions())
	if err != nil || res.VCS == nil || res.Source == types.VCSSourceDisabled {
		return job.GreenGate{}
	}
	meta, err := res.VCS.Metadata(ctx, m.Root())
	if err != nil || meta.Ref == "" {
		return job.GreenGate{}
	}
	dir, err := sessions.Dir(m.Root())
	if err != nil {
		return job.GreenGate{}
	}
	fold, err := sessions.ReadAll(dir)
	if err != nil {
		return job.GreenGate{}
	}
	rec, ok := sessions.LatestGate(fold, meta.Ref, types.TargetCI)
	if !ok || rec.Outcome != sessions.OutcomePass || rec.Commit == "" {
		return job.GreenGate{}
	}
	if meta.ID != rec.Commit {
		history, err := res.VCS.History(ctx, m.Root(), types.HistoryQuery{Limit: gateMergeScanLimit})
		if err != nil || !internalci.MergeFreeRange(history, rec.Commit) {
			return job.GreenGate{}
		}
	}
	rep, err := m.AssessChange(ctx, types.TargetCI, magus.AssessOptions{Base: rec.Commit})
	if err != nil {
		return job.GreenGate{}
	}
	return job.GreenGate{Commit: rec.Commit, Projects: rec.Projects, Tier: rep.Tier}
}

// gradesSymbols reports whether grading id reads the symbol graph: a symbol gate of its
// own, or one inherited from an ancestor.
func gradesSymbols(rows []types.Job, id string) bool {
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	if i < 0 {
		return false
	}
	for _, r := range append([]types.Job{rows[i]}, types.JobAncestors(rows, id)...) {
		if slices.ContainsFunc(r.Goals, func(g types.CompletionGate) bool { return g.Resolve().Kind == types.GateKindSymbol }) {
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
			fmt.Fprintln(os.Stderr, "adds. It prints what changed per job; the global --dry-run prints it and writes")
			fmt.Fprintln(os.Stderr, "nothing. `"+hint.JobFork.With("--schema")+"` prints the record.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Example:")
			fmt.Fprintln(os.Stderr, "  magus job apply -f jobs.jsonl --dry-run")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Widening is the orchestrator's: a session holding a lease may apply only a spec")
			fmt.Fprintln(os.Stderr, "that removes some of its own write paths, which releases them. Ending a job is")
			fmt.Fprintln(os.Stderr, "`"+hint.JobExit.String()+"`.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("magus job apply: the records name their jobs, so %q is one argument too many", pos[0])
	}
	var in io.Reader
	switch file {
	case "":
		return usagef("magus job apply: name the records with -f <file>, or -f - for stdin")
	case "-":
		in = os.Stdin
	default:
		f, err := os.Open(file)
		if err != nil {
			return usagef("magus job apply: %s", err)
		}
		defer f.Close()
		in = f
	}
	records, err := job.DecodeDeclarations(in)
	if err != nil {
		return usagef("magus job apply: %s (`%s` prints the schema each record must satisfy)", err, hint.JobFork.With("--schema"))
	}
	root = resolveRootOrEmpty(root)
	store, err := openJobs(root)
	if err != nil {
		return err
	}
	checkpoint := func() string {
		token, _ := checkoutBaseToken(ctx, root)
		return token
	}
	applied, err := job.Apply(ctx, store, records, globalCfg.Jobs, jobSymbolReader(root), checkpoint, globalCfg.DryRun)
	if err != nil {
		return usagef("magus job apply: %s", err)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		ids := make([]string, len(applied))
		for i, a := range applied {
			ids[i] = a.Next.ID
		}
		return emitNames(ids)
	case outputText:
		printJobApply(os.Stdout, applied, globalCfg.DryRun)
		return nil
	default:
		report := applyReport{DryRun: globalCfg.DryRun}
		for _, a := range applied {
			report.Jobs = append(report.Jobs, applyEntry{Job: a.Next, Created: a.Created, Changed: a.Changed})
		}
		return emitFormatted(opts, report)
	}
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
}

func printJobApply(out io.Writer, applied []job.Applied, dryRun bool) {
	for _, a := range applied {
		switch {
		case a.Created && dryRun:
			fmt.Fprintf(out, "would create %s with %d write path(s)\n", a.Next.ID, len(a.Next.WritePaths))
		case a.Created:
			fmt.Fprintf(out, "created %s, %s, with %d write path(s)\n", a.Next.ID, a.Next.State, len(a.Next.WritePaths))
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
	}
	if dryRun {
		fmt.Fprintln(out, "dry run: nothing written; rerun without --dry-run to write it")
		return
	}
	if len(applied) == 1 {
		printConsoleJobLine(out, applied[0].Next.ID)
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

func goalIDs(goals []types.CompletionGate) []string {
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
			fmt.Fprintln(os.Stderr, "Usage: magus job prune [--all] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "End, as "+string(types.StateNoReturn)+", every job the store can show nobody is working:")
			fmt.Fprintln(os.Stderr, "exited and never collected with `"+hint.JobWait.String()+"`, overdue, orphaned by an")
			fmt.Fprintln(os.Stderr, "ancestor that ended, or declared and never taken past jobs.stale_after. Each row")
			fmt.Fprintln(os.Stderr, "keeps its end_reason, as `"+hint.JobExit.String()+"` would leave it.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A job a holder took and touched within jobs.stale_after is never ended, nor is")
			fmt.Fprintln(os.Stderr, "its parent. --all also ends a taken job nobody touched within jobs.stale_after.")
			fmt.Fprintln(os.Stderr, "The global --dry-run lists what would end and ends nothing.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Example:")
			fmt.Fprintln(os.Stderr, "  magus job prune --dry-run")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return usagef("magus job prune: takes no arguments (got %q)", pos[0])
	}
	store, err := openJobs(resolveRootOrEmpty(root))
	if err != nil {
		return err
	}
	ended, err := store.Prune(ctx, job.PruneOptions{All: all, DryRun: globalCfg.DryRun})
	if err != nil {
		return fmt.Errorf("magus job prune: %w", err)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		ids := make([]string, len(ended))
		for i, e := range ended {
			ids[i] = e.ID
		}
		return emitNames(ids)
	case outputText:
		printPruned(os.Stdout, ended, globalCfg.DryRun)
		return nil
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
