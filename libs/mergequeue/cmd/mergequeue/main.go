// Command mergequeue drives the merge queue from a CI workflow or a terminal. Every
// command reports JSONL events on stdout; hook output and errors go to stderr.
//
//	mergequeue list     --provider github --base main              > changes.json
//	mergequeue plan     --changes changes.json --provider github \
//	                    --affected 'magus affected ci --plan --stdin' --out plan.json
//	mergequeue validate --plan plan.json --gate 'magus affected ci --base "$MERGEQUEUE_ONTO"' \
//	                    --regenerate 'magus affected generate:rw --base "$MERGEQUEUE_ONTO"' --verdicts verdicts
//	mergequeue land     --plan plan.json --verdicts verdicts --provider github --follow
//	mergequeue land     --from-run "$RUN_ID" --verdicts verdicts --provider github
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"github.com/egladman/magus/libs/mergequeue"
	"github.com/egladman/magus/libs/mergequeue/command"
	"github.com/egladman/magus/libs/mergequeue/git"
	"github.com/egladman/magus/libs/mergequeue/provider"
	"github.com/egladman/magus/libs/mergequeue/verdicts"
)

// errUsage exits 2 rather than 1, so a workflow can tell a wiring mistake from a
// queue that ran and failed.
var errUsage = errors.New("usage")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	switch {
	case errors.Is(err, errUsage):
		os.Exit(2)
	case err != nil:
		fmt.Fprintln(os.Stderr, "mergequeue "+err.Error())
		os.Exit(1)
	}
}

// run dispatches a command; its errors read "<command>: ...".
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		usage(stderr)
		return errUsage
	}
	cmds := map[string]func(context.Context, []string, io.Reader, io.Writer, io.Writer) error{
		"list": list, "plan": plan, "validate": validate, "land": land,
	}
	cmd, ok := cmds[args[0]]
	if !ok {
		if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
			usage(stdout)
			return nil
		}
		fmt.Fprintf(stderr, "mergequeue: unknown command %q\n", args[0])
		usage(stderr)
		return errUsage
	}
	err := cmd(ctx, args[1:], stdin, stdout, stderr)
	if err == nil || errors.Is(err, errUsage) {
		return err
	}
	return fmt.Errorf("%s: %w", args[0], err)
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage: mergequeue <command> [flags]

Commands:
  list      ask the provider for the changes carrying merge intent; prints a
            mergequeue.changes/v1 document
  plan      check approval, drop what conflicts with the base, partition by affected
            set; writes a mergequeue.plan/v1 document
  validate  stage and gate a plan's changes; writes a mergequeue.verdict/v1 per change
            the moment it is decided (read access only)
  land      land verdicts as they arrive, each change as its own commit (holds the
            write credential; runs no change's code)

Run 'mergequeue <command> -h' for its flags. Every command prints JSONL events
(mergequeue.event/v1) on stdout.
`)
}

type common struct {
	repo, remote, attribute string
}

func (c *common) bind(fs *flag.FlagSet) {
	fs.StringVar(&c.repo, "repo", ".", "the git checkout the queue works in")
	fs.StringVar(&c.remote, "remote", "origin", "remote name or URL changes and the base are fetched from")
	fs.StringVar(&c.attribute, "attribute", git.DefaultAttribute, "gitattribute marking derived files, read at the base commit")
}

func (c *common) config() git.Config {
	return git.Config{Root: c.repo, Remote: c.remote, Attribute: c.attribute}
}

// remoteURL names the remote for a provider: a configured remote's URL, else the value
// as given, since a URL or a path is its own name.
func (c *common) remoteURL(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "git", "-C", c.repo, "remote", "get-url", "--", c.remote).Output()
	if err != nil {
		return c.remote
	}
	return strings.TrimSpace(string(out))
}

func parse(name string, args []string, stderr io.Writer, bind func(*flag.FlagSet)) error {
	fs := flag.NewFlagSet("mergequeue "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	bind(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errHelp
		}
		return errUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "mergequeue %s: unexpected argument %q\n", name, fs.Arg(0))
		return errUsage
	}
	return nil
}

var errHelp = errors.New("help")

// flagValue is one required flag and what it was given.
type flagValue struct{ name, value string }

// required reports the first missing flag, in the order given.
func required(stderr io.Writer, cmd string, flags ...flagValue) error {
	for _, f := range flags {
		if f.value == "" {
			fmt.Fprintf(stderr, "mergequeue %s: --%s is required\n", cmd, f.name)
			return errUsage
		}
	}
	return nil
}

func list(ctx context.Context, args []string, _ io.Reader, stdout, stderr io.Writer) error {
	var c common
	var spec, base string
	if err := parse("list", args, stderr, func(fs *flag.FlagSet) {
		c.bind(fs)
		fs.StringVar(&spec, "provider", "", "provider: a built-in name (github) or a .buzz file")
		fs.StringVar(&base, "base", "", "branch the queue merges into")
	}); err != nil {
		return helpOK(err)
	}
	if err := required(stderr, "list", flagValue{"provider", spec}, flagValue{"base", base}); err != nil {
		return err
	}
	p, err := provider.Open(ctx, spec)
	if err != nil {
		return err
	}
	defer p.Close()
	remote := c.remoteURL(ctx)
	changes, err := p.ListChanges(ctx, mergequeue.ListQuery{Base: base, Remote: remote})
	if err != nil {
		return err
	}
	// One line, so a document on stdout is itself a JSONL record.
	return mergequeue.WriteChanges(stdout, mergequeue.Changes{Base: base, Remote: remote, Changes: changes})
}

func plan(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	var c common
	var changesFile, spec, affected, out string
	var depth, parallel int
	if err := parse("plan", args, stderr, func(fs *flag.FlagSet) {
		c.bind(fs)
		fs.StringVar(&changesFile, "changes", "-", "the mergequeue.changes/v1 document, or \"-\" for stdin")
		fs.StringVar(&spec, "provider", "", "provider to check approval at each head with; empty admits every change unchecked")
		fs.StringVar(&affected, "affected", "", "command printing a change's affected set from its paths on stdin")
		fs.IntVar(&depth, "depth", 3, "stages of one partition that validate at once")
		fs.IntVar(&parallel, "parallel", runtime.NumCPU(), "changes admitted, and affected hooks run, at once")
		fs.StringVar(&out, "out", "", "file to write the mergequeue.plan/v1 document to")
	}); err != nil {
		return helpOK(err)
	}
	if err := required(stderr, "plan", flagValue{"out", out}); err != nil {
		return err
	}
	if depth < 1 || parallel < 1 {
		fmt.Fprintf(stderr, "mergequeue plan: --depth and --parallel must be at least 1\n")
		return errUsage
	}
	in, err := readChanges(changesFile, stdin)
	if err != nil {
		return err
	}
	repo, err := git.NewStagingRepo(c.config(), "", nil)
	if err != nil {
		return err
	}
	planner := mergequeue.NewPlanner(repo)
	planner.Depth, planner.Parallel, planner.Events = depth, parallel, mergequeue.NewEvents(stdout)
	if spec != "" {
		p, err := provider.Open(ctx, spec)
		if err != nil {
			return err
		}
		defer p.Close()
		planner.Provider = p
	}
	if affected != "" {
		planner.Affected = command.Affected(affected, c.repo, command.NewLog(stderr))
	}
	pl, err := planner.Run(ctx, in)
	if err != nil {
		return err
	}
	return writeFile(out, func(w io.Writer) error { return mergequeue.WritePlan(w, pl) })
}

func readChanges(file string, stdin io.Reader) (mergequeue.Changes, error) {
	if file == "-" {
		return mergequeue.ReadChanges(stdin)
	}
	f, err := os.Open(file)
	if err != nil {
		return mergequeue.Changes{}, err
	}
	defer f.Close()
	return mergequeue.ReadChanges(f)
}

func readPlan(file string) (mergequeue.Plan, error) {
	f, err := os.Open(file)
	if err != nil {
		return mergequeue.Plan{}, err
	}
	defer f.Close()
	p, err := mergequeue.ReadPlan(f)
	if err != nil {
		return mergequeue.Plan{}, fmt.Errorf("%s: %w", file, err)
	}
	return p, nil
}

func writeFile(file string, write func(io.Writer) error) error {
	f, err := os.Create(file)
	if err != nil {
		return err
	}
	if err := write(f); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func validate(ctx context.Context, args []string, _ io.Reader, stdout, stderr io.Writer) error {
	var c common
	var planFile, gate, regenerate, only, dirPath string
	var parallel int
	if err := parse("validate", args, stderr, func(fs *flag.FlagSet) {
		c.bind(fs)
		fs.StringVar(&planFile, "plan", "", "the mergequeue.plan/v1 document")
		fs.StringVar(&gate, "gate", "", "command run in each staging commit's checkout; exit 0 is green")
		fs.StringVar(&regenerate, "regenerate", "", "command run in a staging commit's checkout when the change touched derived files, listed on stdin")
		fs.StringVar(&only, "only", "", "validate this one change; its partition's changes beneath it are staged but not gated")
		fs.IntVar(&parallel, "parallel", runtime.NumCPU(), "stages built or gated at once across every partition")
		fs.StringVar(&dirPath, "verdicts", "", "directory the verdicts are written to, one subdirectory per change")
	}); err != nil {
		return helpOK(err)
	}
	if err := required(stderr, "validate", flagValue{"plan", planFile}, flagValue{"gate", gate}, flagValue{"verdicts", dirPath}); err != nil {
		return err
	}
	if parallel < 1 {
		fmt.Fprintf(stderr, "mergequeue validate: --parallel must be at least 1\n")
		return errUsage
	}
	pl, err := readPlan(planFile)
	if err != nil {
		return err
	}
	// Outside the checkout: stages under it would be discovered as a second copy of it.
	scratch, err := os.MkdirTemp("", "mergequeue-")
	if err != nil {
		return err
	}
	log := command.NewLog(stderr)
	var regen mergequeue.RegenerateFunc
	if regenerate != "" {
		regen = command.Regenerate(regenerate, pl, log)
	}
	repo, err := git.NewStagingRepo(c.config(), scratch, regen)
	if err != nil {
		return err
	}
	defer func() {
		_ = os.RemoveAll(scratch)
		_ = repo.Prune(context.WithoutCancel(ctx))
	}()
	dir := &verdicts.Dir{Path: dirPath, Export: repo.Export}
	v := mergequeue.NewValidator(repo, command.Gate(gate, pl, log), dir)
	v.Only, v.Parallel, v.Events = only, parallel, mergequeue.NewEvents(stdout)
	err = v.Run(ctx, pl)
	// A single-change run is one of several filling out; whoever gathers them marks it.
	// A full run marks it even when it stopped: what it recorded is final, and a Lander
	// following the directory would otherwise wait forever.
	if only == "" {
		err = errors.Join(err, dir.MarkDone())
	}
	return err
}

func land(ctx context.Context, args []string, _ io.Reader, stdout, stderr io.Writer) error {
	var c common
	var planFile, dirPath, spec, statusContext, fromRun, runRepo string
	var follow, dryRun bool
	var interval time.Duration
	if err := parse("land", args, stderr, func(fs *flag.FlagSet) {
		c.bind(fs)
		fs.StringVar(&planFile, "plan", "", "the mergequeue.plan/v1 document")
		fs.StringVar(&dirPath, "verdicts", "", "directory validation writes its verdicts to")
		fs.StringVar(&spec, "provider", "", "provider: a built-in name (github) or a .buzz file")
		fs.StringVar(&statusContext, "status-context", mergequeue.DefaultStatusContext, "commit status the queue posts; branch protection requires it")
		fs.BoolVar(&follow, "follow", false, "keep landing as verdicts arrive, until "+verdicts.DoneFile+" appears in the verdicts directory")
		fs.DurationVar(&interval, "interval", 10*time.Second, "how often --follow looks for new verdicts")
		fs.BoolVar(&dryRun, "dry-run", false, "report what would land; call nothing on the provider")
		fs.StringVar(&fromRun, "from-run", "", "follow this GitHub Actions run instead of --plan: unpack its "+verdicts.PlanArtifact+
			" and "+verdicts.VerdictArtifactPrefix+"<id> artifacts into --verdicts as it uploads them (implies --follow)")
		fs.StringVar(&runRepo, "run-repo", os.Getenv("GITHUB_REPOSITORY"), "owner/name of the repository --from-run belongs to")
	}); err != nil {
		return helpOK(err)
	}
	if (planFile == "") == (fromRun == "") {
		fmt.Fprintf(stderr, "mergequeue land: give exactly one of --plan and --from-run\n")
		return errUsage
	}
	if err := required(stderr, "land", flagValue{"verdicts", dirPath}, flagValue{"provider", spec}); err != nil {
		return err
	}
	if owner, name, ok := strings.Cut(runRepo, "/"); fromRun != "" && (!ok || owner == "" || name == "" || strings.Contains(name, "/")) {
		fmt.Fprintf(stderr, "mergequeue land: --run-repo %q is not owner/name\n", runRepo)
		return errUsage
	}
	var pl mergequeue.Plan
	if planFile != "" {
		var err error
		if pl, err = readPlan(planFile); err != nil {
			return err
		}
	}
	p, err := provider.Open(ctx, spec)
	if err != nil {
		return err
	}
	defer p.Close()
	repo, err := git.NewLandingRepo(c.config())
	if err != nil {
		return err
	}
	events := mergequeue.NewEvents(stdout)
	var src mergequeue.VerdictSource = &verdicts.Dir{Path: dirPath, Follow: follow}
	if fromRun != "" {
		run := &verdicts.ActionsRun{
			API: os.Getenv("GITHUB_API_URL"), Repo: runRepo, RunID: fromRun, Token: readToken(),
			Path: dirPath, Interval: interval, Events: events,
		}
		var planned bool
		if pl, planned, err = run.Plan(ctx); err != nil {
			return err
		}
		if !planned {
			events.Emit(mergequeue.Event{Kind: mergequeue.EventNotice, Reason: "run " + fromRun + " completed without a plan; nothing to land"})
			return nil
		}
		src = run
	}
	l := mergequeue.NewLander(p, repo, src)
	l.StatusContext, l.Interval, l.DryRun, l.Events = statusContext, interval, dryRun, events
	return l.Run(ctx, pl)
}

// readToken picks the credential the GitHub provider reads with: the landing job's
// MERGEQUEUE_TOKEN where it holds one, else GITHUB_TOKEN.
func readToken() string {
	if t := os.Getenv("MERGEQUEUE_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GITHUB_TOKEN")
}

func helpOK(err error) error {
	if errors.Is(err, errHelp) {
		return nil
	}
	return err
}
