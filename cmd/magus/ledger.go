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
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/types"
)

// ledgerCmd implements `magus ledger`, the PERSON's door onto the lease ledger: reading
// the plan (ls, brief), declaring a row (register), and grading a worker (accept).
//
// BOTH CHANNELS WRITE, and the store is what keeps the book honest. The magus_ledger MCP
// tool is the agent's channel and this verb is the person's; they reach the same store and
// the same rules, so a row still has one author (internal/ledger.authorizeRow enforces it)
// without a capability existing for agents that a person does not have.
//
// It takes the --root OVERRIDE and resolves it per verb rather than once here: brief and
// accept both reach loadMagus, whose singleton panics when a second caller hands it a
// different spelling of the same root.
func ledgerCmd(ctx context.Context, root string, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			ledgerUsage()
			return nil
		case hint.LedgerBrief.Leaf():
			return ledgerBrief(ctx, root, args[1:])
		case hint.LedgerAccept.Leaf():
			return ledgerAccept(ctx, root, args[1:])
		case hint.LedgerRegister.Leaf():
			return ledgerRegister(ctx, root, args[1:])
		case "ls":
			args = args[1:]
		default:
			// A flag falls through to the default listing, which is what a bare
			// `magus ledger -o json` has to reach.
			if !strings.HasPrefix(args[0], "-") {
				return usagef("magus ledger: unknown subcommand %q (want ls, brief, register or accept)", args[0])
			}
		}
	}
	return ledgerList(resolveRootOrEmpty(root), args)
}

func ledgerUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus ledger [ls|brief <lease-id>|register|accept <lease-id>] [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Read and write the lease ledger: what an orchestrating agent or a person declared")
	fmt.Fprintln(os.Stderr, "would be handed out, as a tree of parents and the leases they spawned. Kept per")
	fmt.Fprintln(os.Stderr, "repository, so every worktree and clone reads one plan.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  ls        the rows and their overlaps (the default)")
	fmt.Fprintln(os.Stderr, "  brief     print one lease's worker brief")
	fmt.Fprintln(os.Stderr, "  register  declare one row, from flags or a JSON record on stdin")
	fmt.Fprintln(os.Stderr, "  accept    grade a worker's report (JSON on stdin) against its row")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "One author per row, enforced by the store: a worker acting under a lease may")
	fmt.Fprintln(os.Stderr, "release paths and end its own row, and nothing else. "+hint.ToolLedger.String()+" is the")
	fmt.Fprintln(os.Stderr, "same store through an agent's channel.")
}

func openLedger(root string) (*ledger.Store, error) {
	cacheDir, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
	if err != nil {
		return nil, err
	}
	return ledger.NewStore(ledger.Location{CacheDir: cacheDir, Root: root}), nil
}

func ledgerList(root string, args []string) error {
	rest, err := cmdParse("ledger ls", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus ledger ls [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print the declared leases as a tree, each with its state, tier, owned-path")
			fmt.Fprintln(os.Stderr, "count and validation, followed by every pair that claims the same path.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return usagef("magus ledger ls: takes no arguments (got %q)", rest[0])
	}
	store, err := openLedger(root)
	if err != nil {
		return err
	}
	leases, err := store.List()
	if err != nil {
		return err
	}
	// The report, not the bare rows: the overlaps are derived by the same constructor
	// the magus_ledger list op and the console's route use, so the three doors cannot
	// disagree about whether two leases claim one path.
	report := types.NewLeaseReport(leases)

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		ids := make([]string, len(report.Leases))
		for i, lease := range report.Leases {
			ids[i] = lease.ID
		}
		return emitNames(ids)
	case outputText:
		printLedgerTree(os.Stdout, report)
		return nil
	default:
		return emitFormatted(opts, report)
	}
}

func printLedgerTree(out io.Writer, report types.LeaseReport) {
	if len(report.Leases) == 0 {
		fmt.Fprintln(out, "No leases declared. An orchestrating agent declares a plan with the `"+
			hint.ToolLedger.String()+"` MCP tool (op=put), and every worktree of this repository then reads it here.")
		return
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "LEASE\tSTATE\tTIER\tPATHS\tVALIDATION")
	for _, row := range ledgerTreeOrder(report.Leases) {
		fmt.Fprintf(w, "%s%s\t%s\t%s\t%d\t%s\n",
			strings.Repeat("  ", row.depth), row.lease.ID,
			orDash(string(row.lease.State)), orDash(row.lease.Tier),
			len(row.lease.OwnedPaths), orDash(row.lease.Validation))
	}
	_ = w.Flush()

	if len(report.Overlaps) == 0 {
		return
	}
	fmt.Fprintln(out, "\noverlaps")
	for _, o := range report.Overlaps {
		fmt.Fprintf(out, "  %s and %s claim common ground\n", o.LeaseA, o.LeaseB)
		fmt.Fprintf(out, "    %s: %s\n", o.LeaseA, strings.Join(o.PathsA, ", "))
		fmt.Fprintf(out, "    %s: %s\n", o.LeaseB, strings.Join(o.PathsB, ", "))
	}
}

// ledgerTreeLine is one printed line: the row plus how deep its parent chain runs.
type ledgerTreeLine struct {
	lease types.Lease
	depth int
}

// ledgerTreeOrder flattens the rows parent-first, each lease followed by the leases it
// handed out, preserving ledger order among siblings.
//
// Every row reaches the output. One whose parent was cleared, and one caught in a parent
// cycle, print at the top level instead of disappearing: a lease nobody can see is worse
// than one shown without its indentation, and both cases mean the plan is already damaged.
func ledgerTreeOrder(leases []types.Lease) []ledgerTreeLine {
	children := map[string][]types.Lease{}
	for _, lease := range leases {
		children[lease.Parent] = append(children[lease.Parent], lease)
	}
	out := make([]ledgerTreeLine, 0, len(leases))
	emitted := map[string]bool{}
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		for _, lease := range children[parent] {
			if emitted[lease.ID] {
				continue
			}
			emitted[lease.ID] = true
			out = append(out, ledgerTreeLine{lease: lease, depth: depth})
			walk(lease.ID, depth+1)
		}
	}
	walk("", 0)
	for _, lease := range leases {
		if !emitted[lease.ID] {
			emitted[lease.ID] = true
			out = append(out, ledgerTreeLine{lease: lease})
		}
	}
	return out
}

func ledgerBrief(ctx context.Context, root string, args []string) error {
	pos, err := cmdParse("ledger brief", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus ledger brief <lease-id> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print one lease's worker brief: the row's own goal, boundary, validation and")
			fmt.Fprintln(os.Stderr, "dependencies, the graph's blast radius for each owned path, and the commands")
			fmt.Fprintln(os.Stderr, "the worker starts with.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "It renders context and never a verdict: magus assembles what it holds and")
			fmt.Fprintln(os.Stderr, "you give it to the worker, the way `magus diff --prompt` does.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return usagef("magus ledger brief: requires exactly one lease id")
	}
	flagRoot := root
	root = resolveRootOrEmpty(root)
	store, err := openLedger(root)
	if err != nil {
		return err
	}
	leases, err := store.List()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(leases, func(lease types.Lease) bool { return lease.ID == pos[0] })
	if i < 0 {
		return fmt.Errorf("magus ledger brief: no lease %q is declared (run `%s` to see the plan)", pos[0], hint.Ledger)
	}
	row := leases[i]
	if err := briefRefusesTheGate(ctx, flagRoot, row); err != nil {
		return err
	}

	facts := leaseBoundary(ctx, flagRoot, row, leases)
	facts.Evidence, facts.GraphCold = leaseGraphEvidence(ctx, flagRoot, row.OwnedPaths)
	brief := ledger.NewBrief(row, facts)

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

// registerFlags is the one-row case as flags, and row is the same record --stdin decodes.
// One conversion rather than two paths into the store, so a row typed at a terminal and a
// row piped in are the same declaration.
type registerFlags struct {
	goal, parent, check, model, checkpoint string
	writePaths, readPaths, denyPaths       pathList
	dependsOn                              pathList
	readOnly                               bool
}

func (f registerFlags) row(id string) ledger.Row {
	return ledger.Row{
		SchemaVersion:  types.LeaseSchemaVersion,
		ID:             id,
		Parent:         f.parent,
		Goal:           f.goal,
		Checkpoint:     f.checkpoint,
		OwnedPaths:     f.writePaths,
		ForbiddenPaths: f.denyPaths,
		Focus:          f.readPaths,
		DependsOn:      f.dependsOn,
		Check:          f.declaredCheck(),
		Tier:           f.model,
		ReadOnly:       f.readOnly,
		// A row a person declares is one nobody has picked up yet, which is what the
		// state vocabulary already has a word for.
		State: types.StateDeclared,
	}
}

// declaredCheck is --check as the record the row carries, or nil when the flag is absent.
// A value that does not parse reaches ledger.Row.Validate, which is the one place a
// declaration is refused.
func (f registerFlags) declaredCheck() *types.LeaseCheck {
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

// ledgerRegister declares one row, from flags or from a JSON record on stdin.
//
// THE PERSON'S WRITE. An orchestrating agent declares its plan through the magus_ledger
// tool; this is the same store and the same rules for somebody at a terminal, which is
// what keeps the ledger from being a thing only agents can write. The flags cover the
// one-row case so that declaring a lease does not require composing a JSON document, and
// --stdin takes the record when a script already has one.
//
// It REPLACES the row it names rather than merging into it, unlike the tool's put: a
// person typing a row is declaring what it is, while an agent advancing one field of a
// live row must not erase the rest.
func ledgerRegister(ctx context.Context, root string, args []string) error {
	var (
		row           ledger.Row
		declared      registerFlags
		schema, stdin bool
	)
	pos, err := cmdParse("ledger register", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&schema, "schema", false, "Print the JSON schema a row must satisfy, and exit")
		fs.BoolVar(&stdin, "stdin", false, "Read one row as JSON on stdin instead of taking it from flags")
		fs.StringVar(&declared.goal, "goal", "", "The goal and its observable acceptance criteria")
		fs.StringVar(&declared.parent, "parent", "", "The lease this one is handed out under")
		fs.StringVar(&declared.checkpoint, "checkpoint", "", "The working state this lease is handed, as `magus vcs checkpoint -o name` prints it")
		fs.Var(&declared.writePaths, "write-paths", "A path this lease may write; repeatable or comma-separated")
		fs.Var(&declared.denyPaths, "deny-paths", "A path inside the lane this lease may not write; repeatable or comma-separated")
		fs.Var(&declared.readPaths, "read-paths", "A path whose projects this lease may read; repeatable or comma-separated (additive: the written paths are readable already)")
		fs.Var(&declared.dependsOn, "depends-on", "A lease this one waits on; repeatable or comma-separated")
		fs.StringVar(&declared.check, "check", "", "The one check this lease runs, as `<target> <project> [-- args]` (the `magus run` is implied)")
		fs.StringVar(&declared.model, "model", "", "The model the work was matched to")
		fs.BoolVar(&declared.readOnly, "read-only", false, "A lease that gathers evidence and writes nothing")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus ledger register <lease-id> [flags]")
			fmt.Fprintln(os.Stderr, "       magus ledger register --stdin < row.json")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Declare one lease row: what a worker is handed, where it may write, and the one")
			fmt.Fprintln(os.Stderr, "check it runs. The row replaces any row with the same id.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A session bound to a lease may only declare a CHILD of its own row, inside its")
			fmt.Fprintln(os.Stderr, "own paths; widening a lane is the orchestrator's.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "There is no --state: this declares a NEW row, and a row nobody has picked up is")
			fmt.Fprintln(os.Stderr, string(types.StateDeclared)+". A worker moves its own row with "+hint.ToolLedger.String()+" (op put).")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if schema {
		fmt.Print(ledger.RowSchema)
		return nil
	}

	switch {
	case stdin:
		if len(pos) > 0 {
			return usagef("magus ledger register: --stdin reads the id from the record, so %q is one id too many", pos[0])
		}
		if row, err = ledger.DecodeRow(os.Stdin); err != nil {
			return usagef("magus ledger register: %s (`%s` prints the schema it must satisfy)", err, hint.LedgerRegister.With("--schema"))
		}
	case len(pos) != 1:
		return usagef("magus ledger register: requires exactly one lease id, or --stdin with a row on it")
	default:
		row = declared.row(pos[0])
		if err := row.Validate(); err != nil {
			return usagef("magus ledger register: %s", err)
		}
	}

	store, err := openLedger(resolveRootOrEmpty(root))
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
		fmt.Printf("registered lease %s, %s, with %d owned path(s). Brief its worker with `%s`\n",
			stored.ID, orDash(string(stored.State)), len(stored.OwnedPaths), hint.LedgerBrief.With(stored.ID))
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
func ledgerAccept(ctx context.Context, root string, args []string) error {
	var schema, stdin bool
	pos, err := cmdParse("ledger accept", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&schema, "schema", false, "Print the JSON schema a report must satisfy, and exit")
		fs.BoolVar(&stdin, "stdin", false, "Read the worker's report from stdin (required: nothing is read without it)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus ledger accept <lease-id> --stdin < report.json")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Grade a finished worker's report against the lease it was handed: every changed")
			fmt.Fprintln(os.Stderr, "path inside the declared owned paths and outside the forbidden ones, a change set")
			fmt.Fprintln(os.Stderr, "that is not empty, descendants the plan carries, and an output ref that resolves")
			fmt.Fprintln(os.Stderr, "to a PASSING run of this row's own validation. A row that passes is recorded "+string(types.StatePass)+".")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Exit 1 is a verdict: the report was read and rejected, and every rule that failed")
			fmt.Fprintln(os.Stderr, "is named. Exit 2 is magus unable to answer: the report would not decode, the")
			fmt.Fprintln(os.Stderr, "output store would not open, or the row would not write.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "It checks what is mechanical. Whether the work is GOOD, and whether the row's")
			fmt.Fprintln(os.Stderr, "acceptance criteria are met, stay the orchestrator's reading.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if schema {
		fmt.Print(ledger.ReportSchema)
		return nil
	}
	if len(pos) != 1 {
		return usagef("magus ledger accept: requires exactly one lease id, with the worker's report on stdin")
	}
	if !stdin {
		return usagef("magus ledger accept: the report is read from stdin and only with --stdin (`%s`)",
			hint.LedgerAccept.With(pos[0]+" --stdin < report.json"))
	}
	flagRoot := root
	root = resolveRootOrEmpty(root)

	store, err := openLedger(root)
	if err != nil {
		return err
	}
	if actor := store.Actor(); actor.Bound() {
		return fmt.Errorf("magus ledger accept: this checkout is bound to lease %s, and a worker does not grade its own work."+
			" Report what you changed and what you verified, and let the orchestrator accept it", actor.Lease)
	}

	report, err := ledger.DecodeReport(os.Stdin)
	if err != nil {
		return usagef("magus ledger accept: %s (`%s` prints the schema it must satisfy)", err, hint.LedgerAccept.With("--schema"))
	}

	leases, err := store.List()
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(leases, func(lease types.Lease) bool { return lease.ID == pos[0] }) {
		return fmt.Errorf("magus ledger accept: no lease %q is declared (run `%s` to see the plan)", pos[0], hint.Ledger)
	}
	att, err := storedAttempt(ctx, flagRoot, report.Validation.OutputRef)
	if err != nil {
		// Exit 2, with the decode failures: magus could not answer, and that is not a
		// verdict about the work. 1 is reserved for a report that was read and rejected.
		return usagef("magus ledger accept: %s", err)
	}

	// Graded and recorded under ONE lock: the row a two-step read-then-write graded is not
	// the row it stamps, so a release or a clear landing in between grades a boundary that
	// is gone. Only the state moves, so a rejection leaves every declared field alone.
	var verdict ledger.Verdict
	if _, err := store.Update(ctx, pos[0], func(u *types.Lease) {
		verdict = ledger.Grade(*u, report, att, leases)
		if verdict.Accepted {
			u.State = types.StatePass
		}
	}); err != nil {
		return usagef("magus ledger accept: %s", err)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		err = emitNames([]string{verdict.Lease})
	case outputText:
		printLeaseVerdict(os.Stdout, verdict)
	default:
		err = emitFormatted(opts, verdict)
	}
	if err != nil || verdict.Accepted {
		return err
	}
	return errSilent{exitCode: 1}
}

// storedAttempt is what the output store recorded about the run behind ref: which command
// produced it, and whether it failed. An empty ref, and one that aged out of the cache,
// both report MISSING rather than an error: the root cannot reopen either, and that is the
// fact acceptance turns on.
//
// The DESCRIPTOR, not the bytes. Acceptance reads the run's identity and its exit status,
// both of which are metadata, and a captured log is as large as the target was noisy.
func storedAttempt(ctx context.Context, root, ref string) (ledger.Attempt, error) {
	if strings.TrimSpace(ref) == "" {
		return ledger.Attempt{}, nil
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return ledger.Attempt{}, err
	}
	switch d, err := m.OutputDescriptorByRef(ref); {
	case err == nil:
		return ledger.Attempt{
			Found: true, Project: d.Project, Target: d.Target, Spell: d.Spell, Failed: d.Failed,
		}, nil
	case errors.Is(err, fs.ErrNotExist):
		return ledger.Attempt{}, nil
	default:
		return ledger.Attempt{}, err
	}
}

func printLeaseVerdict(out io.Writer, v ledger.Verdict) {
	if v.Accepted {
		fmt.Fprintf(out, "accepted %s, recorded %s\n", v.Lease, types.StatePass)
	} else {
		fmt.Fprintf(out, "rejected %s, and its state is unchanged\n", v.Lease)
		for _, violation := range v.Violations {
			fmt.Fprintf(out, "  %s\n", violation)
		}
	}
	if v.Command != "" {
		fmt.Fprintf(out, "the worker reports it ran %s\n", v.Command)
	}
	if len(v.Risks) > 0 {
		fmt.Fprintln(out, "unresolved risks the worker reported")
		for _, risk := range v.Risks {
			fmt.Fprintf(out, "  %s\n", risk)
		}
	}
}

// leaseAffinityCommits is how far back the co-change scan reads. Wide enough that a
// coupling has to be a habit rather than one refactor, narrow enough that a boundary
// two owners ago does not warn about a partition nobody runs any more.
const leaseAffinityCommits = 200

// leaseBoundary derives what the WORKSPACE says about one lease: the projects its owned
// paths reach, the paths its own declarations put out of reach, and the projects that
// change alongside the leased ones.
//
// This is the half a hand-written ledger row cannot carry. An orchestrator writes down
// the forbidden paths it REMEMBERED; the generated outputs of every project a lease
// invalidates, and the paths a sibling lease is holding right now, hold whether anybody
// remembered them or not.
//
// A read-only lease has no write set, so it gets none of this: the skill puts such a row
// outside the collision analysis entirely.
//
// A workspace that will not load DEGRADES rather than failing, the way the graph evidence
// already does: the row alone carries the goal, the boundary and the check, and a worker
// in a tree whose magusfile is mid-edit is exactly who needs to read them.
func leaseBoundary(ctx context.Context, root string, row types.Lease, leases []types.Lease) ledger.BriefFacts {
	if len(row.OwnedPaths) == 0 {
		return ledger.BriefFacts{}
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return ledger.BriefFacts{WorkspaceCold: true}
	}
	affected, err := m.AffectedFromPaths(ctx, row.OwnedPaths)
	if err != nil {
		return ledger.BriefFacts{WorkspaceCold: true}
	}
	derived := generatedBoundary(m, affected.Affected, row.OwnedPaths)
	derived = append(derived, leasedBoundary(row, leases)...)
	derived = append(derived, sharedBoundary(m, affected.Seed)...)
	return ledger.BriefFacts{
		Projects:         affected.Affected,
		DerivedForbidden: derived,
		Affinity:         leaseAffinity(ctx, m, affected.Seed),
	}
}

// generatedBoundary is the declared output globs, across every project the lease
// invalidates, that land INSIDE its owned paths.
//
// The intersection is what makes this a boundary rather than an inventory. This
// workspace declares around a hundred output globs; listing them all buries the two or
// three a given worker could actually hand-edit, and the worker is already fenced out of
// everything beyond its owned paths. What it cannot know without being told is that a
// file it legitimately owns the directory of is generated.
//
// The AFFECTED set rather than the seeds, because a project writes outputs into trees it
// does not own: a glob from a downstream project can land in this lease's paths.
func generatedBoundary(m *magus.Magus, projects, owned []string) []ledger.BriefBoundary {
	var out []ledger.BriefBoundary
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
			out = append(out, ledger.BriefBoundary{
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
func leasedBoundary(row types.Lease, leases []types.Lease) []ledger.BriefBoundary {
	var out []ledger.BriefBoundary
	for _, other := range leases {
		if other.ID == row.ID || !other.State.Live() {
			continue
		}
		for _, p := range other.OwnedPaths {
			out = append(out, ledger.BriefBoundary{
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
func sharedBoundary(m *magus.Magus, seeds []string) []ledger.BriefBoundary {
	var out []ledger.BriefBoundary
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
			out = append(out, ledger.BriefBoundary{Path: rel, Reason: reason})
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
// dependency is dropped for the reason ledger.BriefAffinity documents.
//
// Best-effort. A repository with no readable history yields nothing, and a partition
// decided without this evidence is the ordinary case rather than a failure.
func leaseAffinity(ctx context.Context, m *magus.Magus, seeds []string) []ledger.BriefAffinity {
	if len(seeds) == 0 {
		return nil
	}
	out, err := m.Affinity(ctx, types.InsightOptions{Commits: leaseAffinityCommits})
	if err != nil {
		return nil
	}
	var pairs []ledger.BriefAffinity
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
		pairs = append(pairs, ledger.BriefAffinity{Project: mine, With: theirs, Commits: pair.Count})
	}
	return pairs
}

// briefRefusesTheGate reports why this row must not be briefed, or nil to render it.
//
// The ONE verdict this command makes, and it is here rather than in ledger.Brief because
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
func briefRefusesTheGate(ctx context.Context, root string, row types.Lease) error {
	fix := fmt.Sprintf(" The gate runs ONCE, in the orchestrator's tree, after every unit lands."+
		" Give this row the narrowest target covering its paths (`%s` decomposes what the gate chains) with the %s tool, then ask for the brief again.",
		hint.DescribeTarget.With(types.TargetCI+" <project>"), hint.ToolLedger)

	if validationNamesGate(row.Validation) {
		return fmt.Errorf("magus ledger brief: lease %s is assigned %q, which names the `%s` gate.%s", row.ID, row.Validation, types.TargetCI, fix)
	}
	if chain := validationReachesGate(ctx, root, row.Validation); len(chain) > 0 {
		return fmt.Errorf("magus ledger brief: lease %s is assigned %q, and that target reaches the `%s` gate through %s.%s",
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

// leaseGraphEvidence resolves each owned path against the knowledge graph, one line per
// path the graph knows. cold reports a graph that would not load at all.
//
// A path the graph cannot resolve is skipped SILENTLY, because most owned paths are
// ordinary source directories the containment tree does not carry, and a "no node" line
// per path would bury the ones that do resolve. A cold graph is different and is
// reported once: "not asked" must not read as "nothing depends on this".
func leaseGraphEvidence(ctx context.Context, root string, paths []string) (evidence []ledger.BriefEvidence, cold bool) {
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
func pathEvidence(g *knowledge.Graph, declared string) (ledger.BriefEvidence, bool) {
	for _, ref := range []string{types.KindDir + ":" + declared, types.KindFile + ":" + declared, declared} {
		out, ok := g.Explain(ref)
		if !ok || out.Node.ID != ref {
			continue
		}
		return ledger.BriefEvidence{Path: declared, Node: out.Node.ID, BlastRadius: out.BlastRadius}, true
	}
	return ledger.BriefEvidence{}, false
}
