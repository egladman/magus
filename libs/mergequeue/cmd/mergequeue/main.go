// Command mergequeue drives the merge queue from a CI workflow or a terminal. Every
// command reports JSONL events on stdout; hook output and errors go to stderr.
//
//	mergequeue ls       --provider github --base main              > changes.json
//	mergequeue plan     --changes changes.json --provider github \
//	                    --affected 'magus affected ci --plan --stdin' --out plan.json
//	mergequeue validate --plan plan.json --gate 'magus affected ci --base "$MERGEQUEUE_ONTO"' \
//	                    --regenerate 'magus affected generate:rw --base "$MERGEQUEUE_ONTO"' --verdicts verdicts
//	mergequeue apply    --provider github verdicts
//	mergequeue apply    --provider github github-actions:acme/widgets/runs/"$RUN_ID"
//
// -C <path>, before the command, runs it as if started in <path>.
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
	"path/filepath"
	"runtime"
	"strconv"
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

// env is what every command shares: the global -C and the standard streams.
type env struct {
	dir    string // absolute
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

type commandFunc func(ctx context.Context, e *env, args []string) error

// run parses the global flags and dispatches a command; its errors read "<command>: ...".
func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	global := flag.NewFlagSet("mergequeue", flag.ContinueOnError)
	global.SetOutput(stderr)
	global.Usage = func() {}
	dir := global.String("C", ".", "")
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage(stdout)
			return nil
		}
		usage(stderr)
		return errUsage
	}
	args = global.Args()
	if len(args) == 0 {
		usage(stderr)
		return errUsage
	}
	cmds := map[string]commandFunc{"ls": ls, "plan": plan, "validate": validate, "apply": apply}
	cmd, ok := cmds[args[0]]
	if !ok {
		if args[0] == "help" {
			usage(stdout)
			return nil
		}
		fmt.Fprintf(stderr, "mergequeue: unknown command %q\n", args[0])
		usage(stderr)
		return errUsage
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return fmt.Errorf("-C %s: %w", *dir, err)
	}
	err = cmd(ctx, &env{dir: abs, stdin: stdin, stdout: stdout, stderr: stderr}, args[1:])
	if err == nil || errors.Is(err, errUsage) {
		return err
	}
	return fmt.Errorf("%s: %w", args[0], err)
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage: mergequeue [-C <path>] <command> [flags] [<source>]

Commands:
  ls        ask the provider for the changes carrying merge intent; prints a
            mergequeue.changes/v1 document
  plan      check approval, drop what conflicts with the base, partition by affected
            set; writes a mergequeue.plan/v1 document
  validate  stage and gate a plan's changes; writes the plan and a
            mergequeue.verdict/v1 per change the moment it is decided (read access only)
  apply     land the green verdicts <source> holds as they arrive, each change as its
            own commit (holds the write credential; runs no change's code)

-C <path> runs the command as if started in <path>: the git checkout the queue works
in, and what every relative path resolves against.

Run 'mergequeue <command> -h' for its flags. Every command prints JSONL events
(mergequeue.event/v1) on stdout.
`)
}

// sourceUsage documents apply's operand; parseSource implements it.
const sourceUsage = `<source> is where apply reads the plan and the verdicts:
  <dir>                                     the directory validate --verdicts wrote
  github-actions:<owner>/<name>/runs/<id>   the artifacts of GitHub Actions run <id>
Text before the first ":" that reads as a URL scheme of two or more characters is
one, and an unknown scheme is an error; write ./<dir> for a directory named like one.
`

const actionsScheme = "github-actions"

// source is apply's operand: a verdict directory, or a GitHub Actions run whose
// artifacts hold one. Exactly one of dir and runID is set.
type source struct {
	dir   string
	repo  string // owner/name of the run's repository
	runID string
}

// parseSource reads arg by the grammar in sourceUsage. A one-letter scheme is a
// Windows drive, as git reads it, so C:\queue stays a path.
func parseSource(arg string) (source, error) {
	if arg == "" {
		return source{}, errors.New("<source> is empty")
	}
	scheme, spec, ok := strings.Cut(arg, ":")
	if !ok || len(scheme) < 2 || !isScheme(scheme) {
		return source{dir: arg}, nil
	}
	if scheme != actionsScheme {
		return source{}, fmt.Errorf("source %q: unknown scheme %q; write ./%s for a directory", arg, scheme, arg)
	}
	parts := strings.Split(spec, "/")
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[2] != "runs" || !isRunID(parts[3]) {
		return source{}, fmt.Errorf("source %q: want %s:<owner>/<name>/runs/<id>", arg, actionsScheme)
	}
	return source{repo: parts[0] + "/" + parts[1], runID: parts[3]}, nil
}

// isScheme applies RFC 3986's scheme syntax.
func isScheme(s string) bool {
	for i, r := range s {
		switch {
		case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z':
		case i > 0 && ('0' <= r && r <= '9' || r == '+' || r == '-' || r == '.'):
		default:
			return false
		}
	}
	return s != ""
}

func isRunID(s string) bool {
	n, err := strconv.ParseUint(s, 10, 64)
	return err == nil && n > 0 && strconv.FormatUint(n, 10) == s
}

// path resolves a relative path argument against -C, as git -C and go -C do.
func (e *env) path(p string) string {
	if p == "" || p == "-" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(e.dir, p)
}

func (e *env) gitConfig(remote, attribute string) git.Config {
	return git.Config{Root: e.dir, Remote: remote, Attribute: attribute}
}

func (e *env) openProvider(ctx context.Context, spec string) (*provider.Script, error) {
	if !provider.IsBuiltin(spec) {
		spec = e.path(spec)
	}
	return provider.Open(ctx, spec)
}

// remoteURL names remote for a provider: a configured remote's URL, else the value as
// given, since a URL or a path is its own name.
func (e *env) remoteURL(ctx context.Context, remote string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", e.dir, "remote", "get-url", "--", remote).Output()
	if err != nil {
		return remote
	}
	return strings.TrimSpace(string(out))
}

func remoteFlag(fs *flag.FlagSet) *string {
	return fs.String("remote", "origin", "remote name or URL changes and the base are fetched from")
}

func attributeFlag(fs *flag.FlagSet) *string {
	return fs.String("attribute", git.DefaultAttribute, "gitattribute marking derived files, read at the base commit")
}

// parse parses a command's flags and exactly the operands named, returning them in
// order. Flags go before operands, as Go's flag package reads them.
func (e *env) parse(name string, args []string, bind func(*flag.FlagSet), operands ...string) ([]string, *flag.FlagSet, error) {
	fs := flag.NewFlagSet("mergequeue "+name, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	synopsis := "Usage: mergequeue [-C <path>] " + name + " [flags]"
	for _, o := range operands {
		synopsis += " <" + o + ">"
	}
	fs.Usage = func() {
		fmt.Fprintln(e.stderr, synopsis)
		fmt.Fprintln(e.stderr, "\nFlags:")
		fs.PrintDefaults()
	}
	// After Usage, so bind can extend it.
	bind(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, nil, errHelp
		}
		return nil, nil, errUsage
	}
	got := fs.Args()
	switch {
	case len(got) > len(operands):
		extra := got[len(operands)]
		if strings.HasPrefix(extra, "-") && len(operands) > 0 {
			fmt.Fprintf(e.stderr, "mergequeue %s: unexpected argument %q; flags go before <%s>\n", name, extra, operands[0])
		} else {
			fmt.Fprintf(e.stderr, "mergequeue %s: unexpected argument %q\n", name, extra)
		}
		return nil, nil, errUsage
	case len(got) < len(operands):
		fmt.Fprintf(e.stderr, "mergequeue %s: missing <%s>\n", name, operands[len(got)])
		return nil, nil, errUsage
	}
	return got, fs, nil
}

var errHelp = errors.New("help")

// flagValue is one required flag and what it was given.
type flagValue struct{ name, value string }

// required reports the first missing flag, in the order given.
func (e *env) required(cmd string, flags ...flagValue) error {
	for _, f := range flags {
		if f.value == "" {
			fmt.Fprintf(e.stderr, "mergequeue %s: --%s is required\n", cmd, f.name)
			return errUsage
		}
	}
	return nil
}

// usageError reports a misuse of cmd; the caller returns what it returns.
func (e *env) usageError(cmd, format string, args ...any) error {
	fmt.Fprintf(e.stderr, "mergequeue "+cmd+": "+format+"\n", args...)
	return errUsage
}

func isSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

func ls(ctx context.Context, e *env, args []string) error {
	var spec, base string
	var remote *string
	if _, _, err := e.parse("ls", args, func(fs *flag.FlagSet) {
		fs.StringVar(&spec, "provider", "", "provider: a built-in name (github) or a .buzz file")
		fs.StringVar(&base, "base", "", "branch the queue merges into")
		remote = fs.String("remote", "origin", "remote whose URL names the repository to the provider")
	}); err != nil {
		return helpOK(err)
	}
	if err := e.required("ls", flagValue{"provider", spec}, flagValue{"base", base}); err != nil {
		return err
	}
	p, err := e.openProvider(ctx, spec)
	if err != nil {
		return err
	}
	defer p.Close()
	url := e.remoteURL(ctx, *remote)
	changes, err := p.ListChanges(ctx, mergequeue.ListQuery{Base: base, Remote: url})
	if err != nil {
		return err
	}
	// One line, so a document on stdout is itself a JSONL record.
	return mergequeue.WriteChanges(e.stdout, mergequeue.Changes{Base: base, Remote: url, Changes: changes})
}

func plan(ctx context.Context, e *env, args []string) error {
	var changesFile, spec, affected, out string
	var remote, attribute *string
	var depth, parallel int
	if _, _, err := e.parse("plan", args, func(fs *flag.FlagSet) {
		fs.StringVar(&changesFile, "changes", "-", "the mergequeue.changes/v1 document, or \"-\" for stdin")
		fs.StringVar(&spec, "provider", "", "provider to check approval at each head with; empty admits every change unchecked")
		fs.StringVar(&affected, "affected", "", "command printing a change's affected set from its paths on stdin")
		fs.IntVar(&depth, "depth", 3, "stages of one partition that validate at once")
		fs.IntVar(&parallel, "parallel", runtime.NumCPU(), "changes admitted, and affected hooks run, at once")
		fs.StringVar(&out, "out", "", "file to write the mergequeue.plan/v1 document to")
		remote, attribute = remoteFlag(fs), attributeFlag(fs)
	}); err != nil {
		return helpOK(err)
	}
	if err := e.required("plan", flagValue{"out", out}); err != nil {
		return err
	}
	if depth < 1 || parallel < 1 {
		return e.usageError("plan", "--depth and --parallel must be at least 1")
	}
	in, err := readChanges(e.path(changesFile), e.stdin)
	if err != nil {
		return err
	}
	repo, err := git.NewStagingRepo(e.gitConfig(*remote, *attribute), "", nil)
	if err != nil {
		return err
	}
	planner := mergequeue.NewPlanner(repo)
	planner.Depth, planner.Parallel, planner.Events = depth, parallel, mergequeue.NewEvents(e.stdout)
	if spec != "" {
		p, err := e.openProvider(ctx, spec)
		if err != nil {
			return err
		}
		defer p.Close()
		planner.Provider = p
	}
	if affected != "" {
		planner.Affected = command.Affected(affected, e.dir, command.NewLog(e.stderr))
	}
	pl, err := planner.Run(ctx, in)
	if err != nil {
		return err
	}
	return writeFile(e.path(out), func(w io.Writer) error { return mergequeue.WritePlan(w, pl) })
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

// validate takes no --provider: it runs the changes' code, so it never talks to the
// forge.
func validate(ctx context.Context, e *env, args []string) (err error) {
	var planFile, gate, regenerate, only, dirPath string
	var remote, attribute *string
	var parallel int
	if _, _, err := e.parse("validate", args, func(fs *flag.FlagSet) {
		fs.StringVar(&planFile, "plan", "", "the mergequeue.plan/v1 document")
		fs.StringVar(&gate, "gate", "", "command run in each staging commit's checkout; exit 0 is green")
		fs.StringVar(&regenerate, "regenerate", "", "command run in a staging commit's checkout when the change touched derived files, listed on stdin")
		fs.StringVar(&only, "only", "", "validate this one change; its partition's changes beneath it are staged but not gated")
		fs.IntVar(&parallel, "parallel", runtime.NumCPU(), "stages built or gated at once across every partition")
		fs.StringVar(&dirPath, "verdicts", "", "directory the plan and the verdicts are written to, one subdirectory per change; apply reads it as its <source>")
		remote, attribute = remoteFlag(fs), attributeFlag(fs)
	}); err != nil {
		return helpOK(err)
	}
	if err := e.required("validate", flagValue{"plan", planFile}, flagValue{"gate", gate}, flagValue{"verdicts", dirPath}); err != nil {
		return err
	}
	if parallel < 1 {
		return e.usageError("validate", "--parallel must be at least 1")
	}
	dir := &verdicts.Dir{Path: e.path(dirPath)}
	// A single-change run is one of several filling out; whoever gathers them marks it.
	// A full run marks it however it ends: what it recorded is final, and apply
	// following the directory would otherwise wait forever.
	if only == "" {
		defer func() { err = errors.Join(err, dir.MarkDone()) }()
	}
	pl, err := readPlan(e.path(planFile))
	if err != nil {
		return err
	}
	// Before any verdict, so apply following the directory can check each one.
	if err := dir.WritePlan(pl); err != nil {
		return err
	}
	// Outside the checkout: stages under it would be discovered as a second copy of it.
	scratch, err := os.MkdirTemp("", "mergequeue-")
	if err != nil {
		return err
	}
	log := command.NewLog(e.stderr)
	var regen mergequeue.RegenerateFunc
	if regenerate != "" {
		regen = command.Regenerate(regenerate, pl, log)
	}
	repo, err := git.NewStagingRepo(e.gitConfig(*remote, *attribute), scratch, regen)
	if err != nil {
		_ = os.RemoveAll(scratch)
		return err
	}
	defer func() {
		_ = os.RemoveAll(scratch)
		_ = repo.Prune(context.WithoutCancel(ctx))
	}()
	dir.Export = repo.Export
	v := mergequeue.NewValidator(repo, command.Gate(gate, pl, log), dir)
	v.Only, v.Parallel, v.Events = only, parallel, mergequeue.NewEvents(e.stdout)
	return v.Run(ctx, pl)
}

// planSource is a verdict source that also carries the plan its verdicts answer to.
type planSource interface {
	mergequeue.VerdictSource
	Plan(ctx context.Context) (mergequeue.Plan, bool, error)
}

func apply(ctx context.Context, e *env, args []string) error {
	var spec, statusContext string
	var remote, attribute *string
	var once, dryRun bool
	var interval time.Duration
	operands, fs, err := e.parse("apply", args, func(fs *flag.FlagSet) {
		flagsUsage := fs.Usage
		fs.Usage = func() {
			flagsUsage()
			fmt.Fprint(fs.Output(), "\n"+sourceUsage)
		}
		fs.StringVar(&spec, "provider", "", "provider: a built-in name (github) or a .buzz file")
		fs.StringVar(&statusContext, "status-context", mergequeue.DefaultStatusContext, "commit status the queue posts; branch protection requires it")
		fs.BoolVar(&once, "once", false, "apply what <source> holds now and stop, rather than following it until it is complete")
		fs.DurationVar(&interval, "interval", 10*time.Second, "how often apply reads <source> while following it")
		fs.BoolVar(&dryRun, "dry-run", false, "report what would land; call nothing on the provider")
		remote, attribute = remoteFlag(fs), attributeFlag(fs)
	}, "source")
	if err != nil {
		return helpOK(err)
	}
	if err := e.required("apply", flagValue{"provider", spec}); err != nil {
		return err
	}
	switch {
	case interval <= 0:
		return e.usageError("apply", "--interval must be positive")
	case once && isSet(fs, "interval"):
		return e.usageError("apply", "--interval has no effect with --once")
	}
	src, err := parseSource(operands[0])
	if err != nil {
		return e.usageError("apply", "%v", err)
	}
	p, err := e.openProvider(ctx, spec)
	if err != nil {
		return err
	}
	defer p.Close()
	repo, err := git.NewLandingRepo(e.gitConfig(*remote, *attribute))
	if err != nil {
		return err
	}
	events := mergequeue.NewEvents(e.stdout)
	var from planSource
	if src.dir != "" {
		from = &verdicts.Dir{Path: e.path(src.dir), Follow: !once, Interval: interval}
	} else {
		// Bundles are imported while landing, so the unpacked run lives only as long.
		tmp, err := os.MkdirTemp("", "mergequeue-apply-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		from = &verdicts.ActionsRun{
			API: os.Getenv("GITHUB_API_URL"), Repo: src.repo, RunID: src.runID, Token: readToken(),
			Path: tmp, Follow: !once, Interval: interval, Events: events,
		}
	}
	pl, planned, err := from.Plan(ctx)
	if err != nil {
		return err
	}
	switch {
	case planned:
	case src.dir != "":
		return fmt.Errorf("%s holds no %s; mergequeue validate --verdicts writes one", e.path(src.dir), verdicts.PlanFile)
	case once:
		events.Emit(mergequeue.Event{Kind: mergequeue.EventNotice, Reason: "run " + src.runID + " has uploaded no plan yet; nothing to apply"})
		return nil
	default:
		events.Emit(mergequeue.Event{Kind: mergequeue.EventNotice, Reason: "run " + src.runID + " completed without a plan; nothing to apply"})
		return nil
	}
	l := mergequeue.NewLander(p, repo, from)
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
