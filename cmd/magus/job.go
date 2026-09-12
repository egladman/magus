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
	"text/tabwriter"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
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
		return usagef("magus job: requires a subcommand (fork, exec, exit, wait or run)")
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
	case hint.JobRun.Leaf():
		return jobRunCatalog(ctx, args[1:])
	default:
		return usagef("magus job: unknown subcommand %q (want fork, exec, exit, wait or run; `%s` lists what is in flight)", args[0], hint.LsJobs)
	}
}

func jobUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus job <fork|exec|exit|wait|run> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Delegated work, on the shell's own lifecycle. A job is the unit of work; a lease is")
	fmt.Fprintln(os.Stderr, "the grant one holder has on it: its write and read lanes, plus the one check it runs.")
	fmt.Fprintln(os.Stderr, "Kept per repository, so every worktree and clone reads one set of jobs.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  fork  declare a job, from flags or a JSON record on stdin")
	fmt.Fprintln(os.Stderr, "  exec  take the lease on a job here, and record the base this checkout landed on")
	fmt.Fprintln(os.Stderr, "  exit  return a job with its result, or abandon it")
	fmt.Fprintln(os.Stderr, "  wait  collect a returned job's result and verify it")
	fmt.Fprintln(os.Stderr, "  run   submit one of the daemon's own jobs and return")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "`"+hint.LsJobs.String()+"` lists every job in flight, and `"+hint.DescribeJob.With("<job>")+"` prints one job's terms.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "One author per job, enforced by the store: a session holding a lease may release")
	fmt.Fprintln(os.Stderr, "paths and end its own job, and nothing else. "+hint.ToolJob.String()+" is the")
	fmt.Fprintln(os.Stderr, "same store through an agent's channel.")
}

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
			fmt.Fprintln(os.Stderr, "Print every job as a tree, each with its state, model, write-path count and")
			fmt.Fprintln(os.Stderr, "check, followed by every pair that claims the same path.")
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
	list := types.NewJobList(jobs)

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
		return emitNames(ids)
	case outputText:
		printJobTree(os.Stdout, list)
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
	fmt.Fprintln(w, "JOB\tHOLDER\tSTATE\tMODEL\tPATHS\tCHECK")
	for _, row := range jobTreeOrder(report.Jobs) {
		fmt.Fprintf(w, "%s%s\t%s\t%s\t%s\t%d\t%s\n",
			strings.Repeat("  ", row.depth), row.lease.ID,
			string(row.lease.Holder.OrSession()), orDash(string(row.lease.State)), orDash(row.lease.Model),
			len(row.lease.WritePaths), orDash(row.lease.Validation))
	}
	_ = w.Flush()

	if len(report.Overlaps) == 0 {
		return
	}
	fmt.Fprintln(out, "\noverlaps")
	for _, o := range report.Overlaps {
		fmt.Fprintf(out, "  %s and %s claim common ground\n", o.JobA, o.JobB)
		fmt.Fprintf(out, "    %s: %s\n", o.JobA, strings.Join(o.PathsA, ", "))
		fmt.Fprintf(out, "    %s: %s\n", o.JobB, strings.Join(o.PathsB, ", "))
	}
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
// arrival. It prints the goal, the lanes, the check, the model, the checkpoint, the
// dependencies, what the workspace itself puts out of reach, and the graph's blast radius
// for each write path, and it prints no procedure: taking the job is `magus job exec`'s
// work to DO, not a paragraph for somebody to follow by hand.
func describeJob(ctx context.Context, root string, args []string) error {
	pos, err := cmdParse("describe job", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus describe job <job> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print one job's terms: its goal, lanes, check and dependencies, the paths this")
			fmt.Fprintln(os.Stderr, "workspace puts out of reach, and the graph's blast radius for each write path.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "It renders context and never a status: magus assembles what it holds and you")
			fmt.Fprintln(os.Stderr, "hand it to whoever takes the job, the way `magus diff --prompt` does.")
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

// pathList accumulates one repeatable, comma-separated flag, on the same rule --skip
// follows (cmd/magus/run.go): an empty segment is refused rather than dropped, so a
// trailing comma cannot silently shrink a lease's lane.
type pathList []string

func (l *pathList) String() string { return strings.Join(*l, ",") }

func (l *pathList) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part == "" {
			return errors.New("empty path")
		}
		*l = append(*l, part)
	}
	return nil
}

// forkFlags is the one-job case as flags, and job is the same record --stdin decodes.
// One conversion rather than two paths into the store, so a job typed at a terminal and a
// job piped in are the same declaration.
type forkFlags struct {
	goal, parent, check, model, checkpoint string
	writePaths, readPaths, denyPaths       pathList
	dependsOn                              pathList
	readOnly                               bool
}

func (f forkFlags) row(id string) job.Declaration {
	return job.Declaration{
		SchemaVersion: types.JobSchemaVersion,
		ID:            id,
		Parent:        f.parent,
		Goal:          f.goal,
		Checkpoint:    f.checkpoint,
		WritePaths:    f.writePaths,
		DenyPaths:     f.denyPaths,
		ReadPaths:     f.readPaths,
		DependsOn:     f.dependsOn,
		Check:         f.declaredCheck(),
		Model:         f.model,
		ReadOnly:      f.readOnly,
		// A row a person declares is one nobody has picked up yet, which is what the
		// state vocabulary already has a word for.
		State: types.StateDeclared,
	}
}

// declaredCheck is --check as the record the row carries, or nil when the flag is absent.
// A value that does not parse reaches job.Declaration.Validate, which is the one place a
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
		row           job.Declaration
		declared      forkFlags
		schema, stdin bool
	)
	pos, err := cmdParse("job fork", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&schema, "schema", false, "Print the JSON schema a job must satisfy, and exit")
		fs.BoolVar(&stdin, "stdin", false, "Read one job as JSON on stdin instead of taking it from flags")
		fs.StringVar(&declared.goal, "goal", "", "The goal and its observable acceptance criteria")
		fs.StringVar(&declared.parent, "parent", "", "The job this one is forked from")
		fs.StringVar(&declared.checkpoint, "checkpoint", "", "The working state this job is handed, as `magus vcs checkpoint -o name` prints it")
		fs.Var(&declared.writePaths, "write-paths", "A path this job may write; repeatable or comma-separated")
		fs.Var(&declared.denyPaths, "deny-paths", "A path inside the lane this job may not write; repeatable or comma-separated")
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
			fmt.Fprintln(os.Stderr, "A session holding a lease may only fork a CHILD of its own job, inside its own")
			fmt.Fprintln(os.Stderr, "paths; widening a lane is the forking session's.")
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

	store, err := openJobs(resolveRootOrEmpty(root))
	if err != nil {
		return err
	}
	stored, err := store.Update(ctx, row.ID, row.Apply)
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
	var base string
	pos, err := cmdParse("job exec", args, func(fs *flag.FlagSet) {
		fs.StringVar(&base, "base", "", "The base this checkout landed on, as `magus vcs checkpoint -o name` prints it (default: read from this checkout)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus job exec <job> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Take the lease on a job here. Every lease-scoped guard and sandbox rule then")
			fmt.Fprintln(os.Stderr, "reads that job's lanes in this checkout, and the base this tree is on is")
			fmt.Fprintln(os.Stderr, "recorded beside the checkpoint the job was handed, with the divergence between")
			fmt.Fprintln(os.Stderr, "them as a fact rather than a refusal.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "With no job, it prints the one this checkout holds.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
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
	if len(pos) == 0 {
		if id := job.LeaseFromMarker(cacheDir); id != "" {
			fmt.Printf("this checkout holds the lease on %s\n", id)
			return nil
		}
		fmt.Println("this checkout holds no job")
		return nil
	}
	if err := job.BindLease(cacheDir, pos[0]); err != nil {
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
		stored, aerr := store.Update(ctx, pos[0], func(u *types.Job) { u.State = types.StateNoReturn })
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
	// The run is resolved in the holder's own checkout, which is the only place it exists:
	// an output store belongs to a cache dir, so a parent waiting from another worktree
	// asked its own store for a ref it never held and read every honest result as evidence
	// from nowhere. Filing the store's record beside the result is what makes the evidence
	// travel with it.
	att, err := storedAttempt(ctx, flagRoot, result.Validation.OutputRef)
	if err != nil {
		return usagef("magus job exit: %s", err)
	}
	if !att.Found {
		return usagef("magus job exit: the result's output ref %q names no run this checkout recorded, so there is nothing behind it."+
			" Run the job's check here, then file the ref that run prints; a result nobody can reopen is an assertion",
			result.Validation.OutputRef)
	}

	stored, err := store.Update(ctx, pos[0], func(u *types.Job) {
		u.Result, u.Attempt = &result, &att
		u.State = types.StateExited
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
// reading a paragraph. A result that ran a filtered subset, wrote outside its lanes, or
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
			fmt.Fprintln(os.Stderr, "that is not empty, descendants the store carries, and a run recording a PASSING")
			fmt.Fprintln(os.Stderr, "execution of this job's own check. A job that verifies is recorded "+string(types.StatePass)+".")
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
	if actor := store.Actor(); actor.Bound() {
		return fmt.Errorf("magus job wait: this checkout holds the lease on %s, and a holder does not verify its own work."+
			" Exit the job with what you changed and what you ran, and let whoever forked it wait on you", actor.Lease)
	}

	jobs, err := store.List()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(jobs, func(one types.Job) bool { return one.ID == pos[0] })
	if i < 0 {
		return fmt.Errorf("magus job wait: there is no job %q (run `%s` to see them)", pos[0], hint.LsJobs)
	}

	result, att, err := resultToVerify(ctx, flagRoot, jobs[i], stdin)
	if err != nil {
		// Exit 2: magus could not answer, and that is not a status about the work. 1 is
		// reserved for a result that was read and rejected.
		return usagef("magus job wait: %s", err)
	}

	// Verified and recorded under ONE lock: the job a two-step read-then-write verified is
	// not the job it stamps, so a release or a clear landing in between verifies lanes that
	// are gone. Only the state moves, so a rejection leaves every declared field alone.
	var status job.Status
	if _, err := store.Update(ctx, pos[0], func(u *types.Job) {
		status = job.Verify(*u, result, att, jobs)
		if status.Verified {
			u.State = types.StatePass
		}
	}); err != nil {
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
	default:
		err = emitFormatted(opts, status)
	}
	if err != nil || status.Verified {
		return err
	}
	return errSilent{exitCode: 1}
}

// resultToVerify is the result wait reads and the run record behind it: what exit filed on
// the job, or what a caller pipes in for a job that was never exited.
//
// The FILED pair is preferred and its attempt is taken as filed, because the holder
// resolved it in the checkout that ran it. A piped result is resolved against this
// checkout's own store, which is right for the local case and is exactly what cannot work
// across two worktrees.
func resultToVerify(ctx context.Context, root string, one types.Job, stdin bool) (types.JobResult, types.JobAttempt, error) {
	if stdin {
		result, err := job.DecodeResult(os.Stdin)
		if err != nil {
			return types.JobResult{}, types.JobAttempt{}, fmt.Errorf("%s (`%s` prints the schema it must satisfy)", err, hint.JobWait.With("--schema"))
		}
		att, err := storedAttempt(ctx, root, result.Validation.OutputRef)
		return result, att, err
	}
	if one.Result == nil {
		return types.JobResult{}, types.JobAttempt{}, fmt.Errorf("job %s has filed no result: it is %s, and nothing was piped in."+
			" Its holder files one with `%s`, or pass --stdin with a result this job never filed",
			one.ID, orDash(string(one.State)), hint.JobExit.With(one.ID+" --stdin < result.json"))
	}
	var att types.JobAttempt
	if one.Attempt != nil {
		att = *one.Attempt
	}
	return *one.Result, att, nil
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
			Found: true, Ref: ref, Project: d.Project, Target: d.Target, Spell: d.Spell, Failed: d.Failed,
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
	if len(s.Risks) > 0 {
		fmt.Fprintln(out, "unresolved risks its holder reported")
		for _, risk := range s.Risks {
			fmt.Fprintf(out, "  %s\n", risk)
		}
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
// already does: the row alone carries the goal, the boundary and the check, and a worker
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
func leasedBoundary(row types.Job, leases []types.Job) []job.TermsBoundary {
	var out []job.TermsBoundary
	for _, other := range leases {
		if other.ID == row.ID || !other.State.Live() {
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

	if guard.LeaseOwnsGate(row) {
		return fmt.Errorf("magus describe job: job %s is assigned %q, which names the `%s` gate.%s", row.ID, row.Validation, types.TargetCI, fix)
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
