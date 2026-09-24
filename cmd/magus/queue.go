package main

// magus queue: the merge queue's verbs. The queue is libs/mergequeue; this file reads
// flags, opens what each step needs, and hands over.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/libs/mergequeue"
	"github.com/egladman/magus/libs/mergequeue/client"
	"github.com/egladman/magus/libs/mergequeue/provider"
	"github.com/egladman/magus/libs/mergequeue/types"
	magustypes "github.com/egladman/magus/types"
)

// queueDownloads makes the artifact downloads of `apply run:`; nil is the queue's
// default client.
var queueDownloads *http.Client

// queueOpenVCS opens the checkout every verb works in.
var queueOpenVCS = client.OpenVCS

// queueEnv is what every verb shares: the checkout and the standard streams.
type queueEnv struct {
	dir    string // absolute
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

func queueCmd(ctx context.Context, root string, args []string) error {
	return runQueue(ctx, root, args, os.Stdin, os.Stdout, os.Stderr)
}

func runQueue(ctx context.Context, root string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		queueUsage(stderr)
		return usagef("magus queue: a subcommand is required (want describe, ls, plan, validate, or apply)")
	}
	dir := root
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("magus queue: --root %s: %w", dir, err)
	}
	e := &queueEnv{dir: abs, stdin: stdin, stdout: stdout, stderr: stderr}
	var verb func(context.Context, *queueEnv, []string) error
	switch args[0] {
	case "describe":
		verb = queueDescribe
	case "ls":
		verb = queueLs
	case "plan":
		verb = queuePlan
	case "validate":
		verb = queueValidate
	case "apply":
		verb = queueApply
	case "-h", "--help", "help":
		queueUsage(stdout)
		return nil
	default:
		return usagef("magus queue: unknown subcommand %q (want describe, ls, plan, validate, or apply)", args[0])
	}
	err = verb(ctx, e, args[1:])
	var misuse errUsage
	if err == nil || errors.Is(err, flag.ErrHelp) || errors.As(err, &misuse) {
		return err
	}
	return fmt.Errorf("magus queue %s: %w", args[0], err)
}

func queueUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: magus queue <subcommand> [flags]")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Subcommands:")
	fmt.Fprintln(w, "  describe  ask the provider what it supports and what wiring the queue up still takes; prints the steps, or a mergequeue.capabilities/v1 document with -o json")
	fmt.Fprintln(w, "  ls        ask the provider for the changes carrying merge intent; prints a mergequeue.changes/v1 document")
	fmt.Fprintln(w, "  plan      check approval, find stacks, drop what conflicts with the base, partition by affected set")
	fmt.Fprintln(w, "  validate  build and gate a candidate per change, writing each verdict as it is decided (read access only)")
	fmt.Fprintln(w, "  apply     rebuild and merge the green verdicts <source> holds (holds the write credential; runs no change's code)")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "The checkout is the one at the global --root, and relative paths resolve against it.")
	fmt.Fprintln(w, "Every subcommand prints JSONL events (mergequeue.event/v1) on stdout.")
	fmt.Fprintln(w, "Run `magus queue <subcommand> -h` for its own flags.")
}

// queueSourceUsage documents apply's operand; parseQueueSource implements it.
const queueSourceUsage = `<source> is where apply reads the plan and the verdicts:
  <dir>        the directory validate --verdicts wrote
  run:<run>    the artifacts of a validation run, <run> as the provider names it
               (github: <owner>/<name>/runs/<id>)
Text before the first ":" that reads as a URL scheme of two or more characters is
one, and an unknown scheme is an error; write ./<dir> for a directory named like one.
`

const queueRunScheme = "run"

// queueSource is apply's operand: a verdict directory, or a validation run whose
// artifacts hold one. Exactly one of dir and run is set.
type queueSource struct {
	dir string
	run string // what the provider's list_artifacts reads
}

// parseQueueSource reads arg by the grammar in queueSourceUsage. A one-letter scheme is
// a Windows drive, as git reads it, so C:\queue stays a path.
func parseQueueSource(arg string) (queueSource, error) {
	if arg == "" {
		return queueSource{}, errors.New("<source> is empty")
	}
	scheme, spec, ok := strings.Cut(arg, ":")
	if !ok || len(scheme) < 2 || !isURLScheme(scheme) {
		return queueSource{dir: arg}, nil
	}
	if scheme != queueRunScheme {
		return queueSource{}, fmt.Errorf("source %q: unknown scheme %q, write ./%s for a directory", arg, scheme, arg)
	}
	if strings.TrimSpace(spec) == "" {
		return queueSource{}, fmt.Errorf("source %q: want %s:<run>", arg, queueRunScheme)
	}
	return queueSource{run: spec}, nil
}

// isURLScheme applies RFC 3986's scheme syntax.
func isURLScheme(s string) bool {
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

// path resolves a relative path argument against the checkout, as --root promises.
func (e *queueEnv) path(p string) string {
	if p == "" || p == "-" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(e.dir, p)
}

// queueParse parses one verb's flags, bound by bind, and exactly the operands named,
// returning them in order. Its usage lists the verb's own flags; magus's global flags
// apply as everywhere.
func queueParse[F any](e *queueEnv, verb, usage string, args []string, bind func(*flag.FlagSet) F, operands ...string) (F, []string, *flag.FlagSet, error) {
	var (
		flags F
		set   *flag.FlagSet
	)
	got, err := cmdParse("queue "+verb, args, func(fs *flag.FlagSet) {
		set, flags = fs, bind(fs)
		fs.SetOutput(e.stderr)
		fs.Usage = func() {
			// nodisplayflags: this set only prints the verb's own flags in its usage; the
			// set that parses is cmdParse's, which binds them.
			own := flag.NewFlagSet("queue "+verb, flag.ContinueOnError)
			own.SetOutput(e.stderr)
			bind(own)
			fmt.Fprintln(e.stderr, "Usage: "+usage)
			fmt.Fprintln(e.stderr, "\nFlags (magus's global flags apply too; see magus -h):")
			own.PrintDefaults()
			if verb == "apply" {
				fmt.Fprint(e.stderr, "\n"+queueSourceUsage)
			}
		}
	})
	if err != nil {
		return flags, nil, nil, err
	}
	switch {
	case len(got) > len(operands):
		return flags, nil, nil, usagef("magus queue %s: unexpected argument %q", verb, got[len(operands)])
	case len(got) < len(operands):
		return flags, nil, nil, usagef("magus queue %s: missing <%s>", verb, operands[len(got)])
	}
	return flags, got, set, nil
}

// required reports the first flag given no value, in the order given.
func queueRequired(verb string, flags ...[2]string) error {
	for _, f := range flags {
		if f[1] == "" {
			return usagef("magus queue %s: --%s is required", verb, f[0])
		}
	}
	return nil
}

func flagGiven(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

// open opens the checkout.
func (e *queueEnv) open(ctx context.Context, remote, backend string) (magustypes.VCSDriver, mergequeue.Clone, error) {
	drv, err := queueOpenVCS(ctx, e.dir, backend, remote)
	return drv, mergequeue.Clone{Root: e.dir, Remote: remote}, err
}

func (e *queueEnv) openProvider(ctx context.Context, spec string) (*provider.Script, error) {
	if !provider.IsBuiltin(spec) {
		spec = e.path(spec)
	}
	return provider.Open(ctx, spec)
}

// openFacts opens what answers the build tool's side: facts, another build tool's
// command, else the magus workspace at the checkout, computing affected sets for target.
func (e *queueEnv) openFacts(ctx context.Context, verb string, targetGiven bool, facts, target string) (types.BuildFacts, func() error, error) {
	if facts != "" {
		if targetGiven {
			return nil, nil, usagef("magus queue %s: --target has no effect with --facts", verb)
		}
		cmd, err := mergequeue.ParseCommand("--facts", facts)
		if err != nil {
			return nil, nil, err
		}
		return mergequeue.CommandFacts(cmd, e.dir, mergequeue.NewHookLog(e.stderr)), func() error { return nil }, nil
	}
	ws, err := client.OpenWorkspace(ctx, e.dir, target)
	if err != nil {
		return nil, nil, fmt.Errorf("open the magus workspace at %s (pass --facts for another build tool): %w", e.dir, err)
	}
	return ws, ws.Close, nil
}

func queueDescribe(ctx context.Context, e *queueEnv, args []string) error {
	f, _, _, err := queueParse(e, "describe", "magus queue describe --provider <provider> --base <branch> [flags]", args, gen.BindQueueDescribe)
	if err != nil {
		return err
	}
	if err := queueRequired("describe", [2]string{"provider", f.Provider}, [2]string{"base", f.Base}); err != nil {
		return err
	}
	opts, err := ResolveOutput(global.output)
	if err != nil {
		return err
	}
	if opts.Format != FormatText && opts.Format != FormatJSON && opts.Format != FormatJSONL {
		return usagef("magus queue describe: -o %s is not supported (want text, json or jsonl)", opts.Format)
	}
	drv, cl, err := e.open(ctx, f.Remote, f.VCS)
	if err != nil {
		return err
	}
	url, err := drv.RemoteURL(ctx, cl.Root, cl.Remote)
	if err != nil {
		return err
	}
	p, err := e.openProvider(ctx, f.Provider)
	if err != nil {
		return err
	}
	defer p.Close()
	// An empty --status-context asks for no setup, whose reads need permissions a pull
	// request job's token may lack.
	caps, err := p.Describe(ctx, types.ListQuery{Base: f.Base, RemoteURL: url, StatusContext: f.StatusContext, App: f.App, SetupSteps: f.StatusContext != ""})
	if err != nil {
		return err
	}
	if opts.Format != FormatText {
		return mergequeue.WriteCapabilities(e.stdout, f.Base, caps)
	}
	if err := caps.Check(); err != nil {
		return fmt.Errorf("%s: %w", types.SchemaCapabilities, err)
	}
	return writeQueueSetup(e.stdout, f.Provider, f.Base, caps)
}

// writeQueueSetup renders what describe read as a script a person reads and then runs
// step by step: every fact is a comment, every step a command or a link to open.
func writeQueueSetup(w io.Writer, provider, base string, caps types.Capabilities) error {
	var b strings.Builder
	methods := make([]string, len(caps.Methods))
	for i, m := range caps.Methods {
		methods[i] = string(m)
	}
	fmt.Fprintf(&b, "# %s on %s: merge methods %s; stacks merge %s", provider, base, strings.Join(methods, ", "), caps.StackMerge)
	if caps.QueueLabel != "" {
		fmt.Fprintf(&b, "; a stack queues by the label %q", caps.QueueLabel+"<method>")
	}
	b.WriteString("\n")
	s := caps.Setup
	if s == nil {
		fmt.Fprintf(&b, "# no setup: --status-context is empty, or provider %s reports none\n", provider)
		_, err := io.WriteString(w, b.String())
		return err
	}
	fmt.Fprintf(&b, "# the queue posts %q as %s\n", s.StatusContext, s.Credential)
	if len(s.RequiredChecks) == 0 {
		fmt.Fprintf(&b, "# %s requires no check\n", base)
	}
	for _, rc := range s.RequiredChecks {
		from := "any source"
		if rc.Integration != "" {
			from = "integration " + rc.Integration
		}
		seen := "not seen reported"
		if len(rc.Events) > 0 {
			seen = "seen on " + strings.Join(rc.Events, ", ")
		}
		fmt.Fprintf(&b, "# %s requires %q from %s (%s)\n", base, rc.Context, from, seen)
	}
	for _, st := range s.Settings {
		fmt.Fprintf(&b, "# %s is %s; the queue needs %s\n", st.Name, st.Value, st.Want)
	}
	if a := s.App; a != nil {
		fmt.Fprintf(&b, "# app %s is integration %s", a.Slug, a.ID)
		if a.ClientID != "" {
			fmt.Fprintf(&b, ", client id %s", a.ClientID)
		}
		b.WriteString("\n")
	}
	if len(s.Steps) == 0 {
		b.WriteString("# nothing left to set up\n")
	}
	for i, st := range s.Steps {
		fmt.Fprintf(&b, "\n# %d. %s\n", i+1, st.Title)
		if st.URL != "" {
			fmt.Fprintf(&b, "# open %s\n", st.URL)
			continue
		}
		b.WriteString(strings.TrimRight(st.Command, "\n") + "\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func queueLs(ctx context.Context, e *queueEnv, args []string) error {
	f, _, _, err := queueParse(e, "ls", "magus queue ls --provider <provider> --base <branch> [flags]", args, gen.BindQueueLs)
	if err != nil {
		return err
	}
	if err := queueRequired("ls", [2]string{"provider", f.Provider}, [2]string{"base", f.Base}); err != nil {
		return err
	}
	drv, cl, err := e.open(ctx, f.Remote, f.VCS)
	if err != nil {
		return err
	}
	url, err := drv.RemoteURL(ctx, cl.Root, cl.Remote)
	if err != nil {
		return err
	}
	p, err := e.openProvider(ctx, f.Provider)
	if err != nil {
		return err
	}
	defer p.Close()
	changes, err := p.ListChanges(ctx, types.ListQuery{Base: f.Base, RemoteURL: url})
	if err != nil {
		return err
	}
	// One line, so a document on stdout is itself a JSONL record.
	return mergequeue.WriteChanges(e.stdout, changes)
}

func queuePlan(ctx context.Context, e *queueEnv, args []string) error {
	f, _, fs, err := queueParse(e, "plan", "magus queue plan --provider <provider> --out <file> [flags]", args, gen.BindQueuePlan)
	if err != nil {
		return err
	}
	if err := queueRequired("plan", [2]string{"out", f.Out}, [2]string{"provider", f.Provider}); err != nil {
		return err
	}
	if f.Depth < 1 || f.Parallel < 0 {
		return usagef("magus queue plan: --depth must be at least 1 and --parallel must not be negative")
	}
	in, err := readQueueChanges(e.path(f.Changes), e.stdin)
	if err != nil {
		return err
	}
	drv, cl, err := e.open(ctx, f.Remote, f.VCS)
	if err != nil {
		return err
	}
	p, err := e.openProvider(ctx, f.Provider)
	if err != nil {
		return err
	}
	defer p.Close()
	bf, closeFacts, err := e.openFacts(ctx, "plan", flagGiven(fs, gen.FlagQueuePlanTarget), f.Facts, f.Target)
	if err != nil {
		return err
	}
	defer func() { _ = closeFacts() }()
	planner, err := mergequeue.NewPlanner(drv, cl, p, bf)
	if err != nil {
		return err
	}
	planner.Depth, planner.Parallel, planner.Events = f.Depth, f.Parallel, mergequeue.NewEvents(e.stdout)
	pl, err := planner.Run(ctx, in)
	if err != nil {
		return err
	}
	return writeQueueFile(e.path(f.Out), func(w io.Writer) error { return mergequeue.WritePlan(w, pl) })
}

func readQueueChanges(file string, stdin io.Reader) (types.Changes, error) {
	if file == "-" {
		return mergequeue.ReadChanges(stdin)
	}
	f, err := os.Open(file)
	if err != nil {
		return types.Changes{}, err
	}
	defer f.Close()
	return mergequeue.ReadChanges(f)
}

func writeQueueFile(file string, write func(io.Writer) error) error {
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

// queueScratch makes a directory outside the checkout, where candidates are checked
// out: under the checkout they would be discovered as a second copy of it.
func queueScratch(prefix string) (string, func(), error) {
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// queueValidate takes no --provider: it runs the changes' code, so it never talks to
// one.
func queueValidate(ctx context.Context, e *queueEnv, args []string) (err error) {
	var vars queueScratchVars
	f, _, fs, err := queueParse(e, "validate", "magus queue validate --plan <file> --gate <command> --verdicts <dir> [flags]", args,
		func(fs *flag.FlagSet) *gen.QueueValidateFlags {
			fs.Var(&vars, gen.FlagQueueValidateScratchEnv, "`NAME=DIR` sets NAME to DIR in the candidate's scratch directory for every hook, so the cache it names is the candidate's own; repeatable")
			return gen.BindQueueValidate(fs)
		})
	if err != nil {
		return err
	}
	if err := queueRequired("validate", [2]string{"plan", f.Plan}, [2]string{"gate", f.Gate}, [2]string{"verdicts", f.Verdicts}); err != nil {
		return err
	}
	if f.Parallel < 0 {
		return usagef("magus queue validate: --parallel must not be negative")
	}
	gate, err := mergequeue.ParseCommand("--gate", f.Gate)
	if err != nil {
		return err
	}
	var regenerate mergequeue.Command
	if f.Regenerate != "" {
		if regenerate, err = mergequeue.ParseCommand("--regenerate", f.Regenerate); err != nil {
			return err
		}
	}
	dir := &mergequeue.VerdictDir{Path: e.path(f.Verdicts)}
	// A single-change run is one of several filling out; whoever gathers them marks it.
	// A full run marks it however it ends: what it recorded is final, and apply
	// following the directory would otherwise wait forever.
	if f.Only == "" {
		defer func() { err = errors.Join(err, dir.MarkDone()) }()
	}
	pl, err := mergequeue.ReadPlanFile(e.path(f.Plan))
	if err != nil {
		return err
	}
	// Before any verdict, so apply following the directory can check each one.
	if err := dir.WritePlan(pl); err != nil {
		return err
	}
	drv, cl, err := e.open(ctx, f.Remote, f.VCS)
	if err != nil {
		return err
	}
	bf, closeFacts, err := e.openFacts(ctx, "validate", flagGiven(fs, gen.FlagQueueValidateTarget), f.Facts, f.Target)
	if err != nil {
		return err
	}
	defer func() { _ = closeFacts() }()
	scratch, cleanup, err := queueScratch("mergequeue-")
	if err != nil {
		return err
	}
	defer cleanup()
	log := mergequeue.NewHookLog(e.stderr)
	v, err := mergequeue.NewValidator(drv, cl, mergequeue.CommandGate(gate, vars, log), dir, bf, scratch)
	if err != nil {
		return err
	}
	v.Only, v.Parallel, v.Events = f.Only, f.Parallel, mergequeue.NewEvents(e.stdout)
	v.Reproduce = types.Reproduction{Gate: f.Gate, Regenerate: f.Regenerate}
	if regenerate != nil {
		v.Regenerate = mergequeue.CommandRegenerate(regenerate, vars, log)
	}
	return v.Run(ctx, pl)
}

// queueScratchVars is --scratch-env, which may repeat.
type queueScratchVars []mergequeue.ScratchVar

func (s *queueScratchVars) String() string {
	if s == nil {
		return ""
	}
	specs := make([]string, len(*s))
	for i, v := range *s {
		specs[i] = v.Name + "=" + v.Dir
	}
	return strings.Join(specs, ",")
}

func (s *queueScratchVars) Set(spec string) error {
	v, err := mergequeue.ParseScratchVar(spec)
	if err != nil {
		return err
	}
	*s = append(*s, v)
	return nil
}

// queuePlanSource is a verdict source that also carries the plan its verdicts answer to.
type queuePlanSource interface {
	types.VerdictSource
	Plan(ctx context.Context) (types.Plan, bool, error)
}

func queueApply(ctx context.Context, e *queueEnv, args []string) error {
	var vars queueScratchVars
	f, operands, fs, err := queueParse(e, "apply", "magus queue apply --provider <provider> --base <branch> [flags] <source>", args,
		func(fs *flag.FlagSet) *gen.QueueApplyFlags {
			fs.Var(&vars, gen.FlagQueueApplyScratchEnv, "`NAME=DIR` sets NAME to DIR in the rebuild's scratch directory for the regeneration, so the cache it names is that rebuild's own; repeatable")
			return gen.BindQueueApply(fs)
		}, "source")
	if err != nil {
		return err
	}
	if err := queueRequired("apply", [2]string{"provider", f.Provider}, [2]string{"base", f.Base}); err != nil {
		return err
	}
	switch {
	case f.Interval <= 0:
		return usagef("magus queue apply: --interval must be positive")
	case f.Once && flagGiven(fs, gen.FlagQueueApplyInterval):
		return usagef("magus queue apply: --interval has no effect with --once")
	}
	src, err := parseQueueSource(operands[0])
	if err != nil {
		return usagef("magus queue apply: %v", err)
	}
	switch {
	case src.run != "" && f.Workflow == "":
		return usagef("magus queue apply: a run: source needs --workflow, the definition the run must have run")
	case src.dir != "" && f.Workflow != "":
		return usagef("magus queue apply: --workflow has no effect on a directory source")
	}
	who, err := parseCommitter(f.Committer)
	if err != nil {
		return usagef("magus queue apply: --committer: %v", err)
	}
	var regenerate mergequeue.Command
	if f.Regenerate != "" {
		if regenerate, err = mergequeue.ParseCommand("--regenerate", f.Regenerate); err != nil {
			return err
		}
	}
	if f.ReproduceRegenerate != "" && f.ReproduceGate == "" {
		return usagef("magus queue apply: --reproduce-regenerate needs --reproduce-gate")
	}
	// Shown, never run; parsed so a kick-back never shows a line validate would refuse.
	for _, hook := range [][2]string{{"--reproduce-gate", f.ReproduceGate}, {"--reproduce-regenerate", f.ReproduceRegenerate}} {
		if hook[1] == "" {
			continue
		}
		if _, err := mergequeue.ParseCommand(hook[0], hook[1]); err != nil {
			return err
		}
	}
	p, err := e.openProvider(ctx, f.Provider)
	if err != nil {
		return err
	}
	defer p.Close()
	if src.run != "" && !p.ListsArtifacts() {
		return fmt.Errorf("provider %q does not export list_artifacts, which reading run:%s needs", f.Provider, src.run)
	}
	drv, cl, err := e.open(ctx, f.Remote, f.VCS)
	if err != nil {
		return err
	}
	remoteURL, err := drv.RemoteURL(ctx, cl.Root, cl.Remote)
	if err != nil {
		return err
	}
	bf, closeFacts, err := e.openFacts(ctx, "apply", flagGiven(fs, gen.FlagQueueApplyTarget), f.Facts, f.Target)
	if err != nil {
		return err
	}
	defer func() { _ = closeFacts() }()
	events := mergequeue.NewEvents(e.stdout)
	var from queuePlanSource
	if src.dir != "" {
		from = &mergequeue.VerdictDir{Path: e.path(src.dir), Follow: !f.Once, Interval: f.Interval}
	} else {
		// The unpacked run lives only as long as applying does.
		tmp, cleanup, err := queueScratch("mergequeue-run-")
		if err != nil {
			return err
		}
		defer cleanup()
		from = &mergequeue.ArtifactFollower{Lister: p, Source: src.run, Path: tmp, Branch: f.Base, Definition: f.Workflow,
			Follow: !f.Once, Interval: f.Interval, Client: queueDownloads, Events: events}
	}
	pl, planned, err := from.Plan(ctx)
	if err != nil {
		return err
	}
	switch {
	case planned:
	case src.dir != "":
		return fmt.Errorf("%s holds no %s", e.path(src.dir), mergequeue.PlanFile)
	case f.Once:
		events.Emit(mergequeue.Event{Kind: mergequeue.EventNotice, Reason: "run " + src.run + " has uploaded no plan yet; nothing to apply"})
		return nil
	default:
		events.Emit(mergequeue.Event{Kind: mergequeue.EventNotice, Reason: "run " + src.run + " completed without a plan; nothing to apply"})
		return nil
	}
	scratch, cleanup, err := queueScratch("mergequeue-apply-")
	if err != nil {
		return err
	}
	defer cleanup()
	a, err := mergequeue.NewApplier(drv, cl, p, from, bf, scratch)
	if err != nil {
		return err
	}
	a.Base, a.RemoteURL = f.Base, remoteURL
	a.StatusContext, a.App, a.Interval, a.DryRun, a.Committer, a.Source, a.Events = f.StatusContext, f.App, f.Interval, globalCfg.DryRun, who, src.run, events
	a.Reproduce = types.Reproduction{Gate: f.ReproduceGate, Regenerate: f.ReproduceRegenerate}
	if regenerate != nil {
		a.Regenerate = mergequeue.CommandRegenerate(regenerate, vars, mergequeue.NewHookLog(e.stderr))
	}
	return a.Run(ctx, pl)
}

// parsePerson reads "Name <email>"; empty is the zero Person.
func parseCommitter(s string) (magustypes.Person, error) {
	if s == "" {
		return magustypes.Person{}, nil
	}
	name, rest, ok := strings.Cut(s, " <")
	email, closed := strings.CutSuffix(rest, ">")
	name = strings.TrimSpace(name)
	if !ok || !closed || name == "" || email == "" || strings.ContainsAny(email, "<> ") {
		return magustypes.Person{}, fmt.Errorf("%q is not \"Name <email>\"", s)
	}
	return magustypes.Person{Name: name, Email: email}, nil
}
