// Command mergequeue drives the merge queue from a CI workflow or a terminal. Every
// command reports JSONL events on stdout; hook output and errors go to stderr.
//
//	mergequeue list     --provider github --base main              > changes.json
//	mergequeue plan     --changes changes.json --provider github \
//	                    --affected 'magus affected ci --plan --stdin' --out plan.json
//	mergequeue validate --plan plan.json --gate 'magus affected ci --base "$MERGEQUEUE_BELOW"' \
//	                    --regenerate 'magus affected generate:rw --base "$MERGEQUEUE_BELOW"' --out stages
//	mergequeue land     --plan plan.json --stages stages --provider github --follow
package main

import (
	"context"
	"encoding/json"
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
	"github.com/egladman/magus/libs/mergequeue/git"
	"github.com/egladman/magus/libs/mergequeue/provider"
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
		fmt.Fprintln(os.Stderr, "mergequeue: "+err.Error())
		os.Exit(1)
	}
}

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
	return cmd(ctx, args[1:], stdin, stdout, stderr)
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage: mergequeue <command> [flags]

Commands:
  list      ask the provider for the changes carrying merge intent; prints a
            mergequeue.changes/v1 document
  plan      check approval, drop what conflicts with the base, partition by affected
            set; writes a mergequeue.plan/v1 document
  validate  stage and gate a plan's changes; writes a mergequeue.stage/v1 verdict per
            change the moment it is decided (read access only)
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

func (c *common) gitRepo() *git.Repo {
	return &git.Repo{Root: c.repo, Remote: c.remote, Attribute: c.attribute}
}

// remoteURL names the remote for a provider: a configured remote's URL, else the value
// as given, since a URL or a path is its own name.
func (c *common) remoteURL(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "git", "-C", c.repo, "remote", "get-url", c.remote).Output()
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

func required(stderr io.Writer, cmd string, flags map[string]string) error {
	for name, v := range flags {
		if v == "" {
			fmt.Fprintf(stderr, "mergequeue %s: --%s is required\n", cmd, name)
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
	if err := required(stderr, "list", map[string]string{"provider": spec, "base": base}); err != nil {
		return err
	}
	p, err := provider.Load(ctx, spec)
	if err != nil {
		return err
	}
	defer p.Close()
	remote := c.remoteURL(ctx)
	changes, err := p.List(ctx, mergequeue.ListQuery{Base: base, Remote: remote})
	if err != nil {
		return err
	}
	return encode(stdout, mergequeue.Changes{Schema: mergequeue.SchemaChanges, Base: base, Remote: remote, Changes: changes})
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
		fs.IntVar(&parallel, "parallel", runtime.NumCPU(), "affected hook calls that run at once")
		fs.StringVar(&out, "out", "", "file to write the mergequeue.plan/v1 document to")
	}); err != nil {
		return helpOK(err)
	}
	if err := required(stderr, "plan", map[string]string{"out": out}); err != nil {
		return err
	}
	in, err := readChanges(changesFile, stdin)
	if err != nil {
		return err
	}
	planner := &mergequeue.Planner{Stager: c.gitRepo(), Depth: depth, Parallel: parallel, Events: mergequeue.NewEvents(stdout)}
	if spec != "" {
		p, err := provider.Load(ctx, spec)
		if err != nil {
			return err
		}
		defer p.Close()
		planner.Provider = p
	}
	if affected != "" {
		planner.Affected = mergequeue.AffectedCommand(affected, c.repo, stderr)
	}
	pl, runErr := planner.Run(ctx, in)
	if pl.BaseSHA == "" {
		return runErr
	}
	return errors.Join(runErr, mergequeue.WriteJSON(out, pl))
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

func validate(ctx context.Context, args []string, _ io.Reader, stdout, stderr io.Writer) error {
	var c common
	var planFile, gate, regenerate, only, out string
	if err := parse("validate", args, stderr, func(fs *flag.FlagSet) {
		c.bind(fs)
		fs.StringVar(&planFile, "plan", "", "the mergequeue.plan/v1 document")
		fs.StringVar(&gate, "gate", "", "command run in each staging commit's checkout; exit 0 is green")
		fs.StringVar(&regenerate, "regenerate", "", "command run in a staging commit's checkout when the change touched derived files, listed on stdin")
		fs.StringVar(&only, "only", "", "validate this one change; its partition's changes beneath it are staged but not gated")
		fs.StringVar(&out, "out", "", "directory the verdicts are written to, one subdirectory per change")
	}); err != nil {
		return helpOK(err)
	}
	if err := required(stderr, "validate", map[string]string{"plan": planFile, "gate": gate, "out": out}); err != nil {
		return err
	}
	pl, err := mergequeue.ReadPlan(planFile)
	if err != nil {
		return err
	}
	// Outside the checkout: stages under it would be discovered as a second copy of it.
	scratch, err := os.MkdirTemp("", "mergequeue-")
	if err != nil {
		return err
	}
	repo := c.gitRepo()
	repo.Scratch = scratch
	defer func() {
		_ = os.RemoveAll(scratch)
		_ = repo.Prune(context.WithoutCancel(ctx))
	}()
	log := mergequeue.NewPrefixWriter(stderr)
	if regenerate != "" {
		repo.Regenerate = func(ctx context.Context, dir, below string, ch mergequeue.Change, paths []string) error {
			w := log.With("[regenerate #" + ch.ID + "] ")
			return mergequeue.Command{
				Line: regenerate,
				Dir:  dir,
				Env: []string{mergequeue.EnvChange + "=" + ch.ID, mergequeue.EnvHead + "=" + ch.Head,
					mergequeue.EnvBase + "=" + pl.Base, mergequeue.EnvBaseSHA + "=" + pl.BaseSHA, mergequeue.EnvBelow + "=" + below},
				Stdin:  strings.NewReader(strings.Join(paths, "\n") + "\n"),
				Stdout: w,
				Stderr: w,
			}.Run(ctx)
		}
	}
	export := func(ctx context.Context, file, stage string) error { return repo.Export(ctx, file, pl.BaseSHA, stage) }
	v := &mergequeue.Validation{
		Stager: repo,
		Gate:   &mergequeue.CommandGate{Line: gate, Base: pl.Base, BaseSHA: pl.BaseSHA, Log: log},
		Only:   only,
		Result: func(ctx context.Context, r mergequeue.StageResult) error {
			return mergequeue.WriteResult(ctx, out, r, export)
		},
		Events: mergequeue.NewEvents(stdout),
	}
	if err := v.Run(ctx, pl); err != nil {
		return err
	}
	// A single-change run is one of several filling out; whoever gathers them marks it.
	if only == "" {
		return mergequeue.MarkDone(out)
	}
	return nil
}

func land(ctx context.Context, args []string, _ io.Reader, stdout, stderr io.Writer) error {
	var c common
	var planFile, stages, spec, statusContext string
	var follow, dryRun bool
	var interval time.Duration
	if err := parse("land", args, stderr, func(fs *flag.FlagSet) {
		c.bind(fs)
		fs.StringVar(&planFile, "plan", "", "the mergequeue.plan/v1 document")
		fs.StringVar(&stages, "stages", "", "directory validation writes its verdicts to")
		fs.StringVar(&spec, "provider", "", "provider: a built-in name (github) or a .buzz file")
		fs.StringVar(&statusContext, "status-context", mergequeue.DefaultStatusContext, "commit status the queue posts; branch protection requires it")
		fs.BoolVar(&follow, "follow", false, "keep landing as verdicts arrive, until "+mergequeue.DoneFile+" appears in the stages directory")
		fs.DurationVar(&interval, "interval", 10*time.Second, "how often --follow looks for new verdicts")
		fs.BoolVar(&dryRun, "dry-run", false, "report what would land; call nothing on the provider")
	}); err != nil {
		return helpOK(err)
	}
	if err := required(stderr, "land", map[string]string{"plan": planFile, "stages": stages, "provider": spec}); err != nil {
		return err
	}
	pl, err := mergequeue.ReadPlan(planFile)
	if err != nil {
		return err
	}
	p, err := provider.Load(ctx, spec)
	if err != nil {
		return err
	}
	defer p.Close()
	l := &mergequeue.Landing{
		Provider:      p,
		Lander:        c.gitRepo(),
		StatusContext: statusContext,
		Interval:      interval,
		DryRun:        dryRun,
		Events:        mergequeue.NewEvents(stdout),
	}
	return l.Run(ctx, pl, &mergequeue.DirResults{Dir: stages, Follow: follow})
}

func helpOK(err error) error {
	if errors.Is(err, errHelp) {
		return nil
	}
	return err
}

// encode writes v as one line, so a document on stdout is itself a JSONL record.
func encode(w io.Writer, v any) error {
	return json.NewEncoder(w).Encode(v)
}
