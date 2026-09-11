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
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/types"
)

// ledgerCmd implements `magus ledger`, the door onto the lease ledger for the two
// parties the MCP tool does not serve: the person orchestrating, and a shell script
// grading a worker.
//
// Declaring the plan stays on the magus_ledger MCP tool and magus\ledger, because an
// orchestrating AGENT writes it. What lives here is reading (ls, brief) and the one
// write that is a VERDICT rather than a declaration: accept grades a finished worker
// and needs an exit status, which a tool call cannot hand a shell.
// It takes the --root OVERRIDE and resolves it per verb rather than once here. brief and
// accept both reach loadMagus, whose singleton panics when a second caller hands it a
// different spelling of the same root, and every other command reaches it with the raw
// flag value.
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
		case "ls":
			args = args[1:]
		default:
			// A flag falls through to the default listing, which is what a bare
			// `magus ledger -o json` has to reach.
			if !strings.HasPrefix(args[0], "-") {
				return usagef("magus ledger: unknown subcommand %q (want ls, brief or accept)", args[0])
			}
		}
	}
	return ledgerList(resolveRootOrEmpty(root), args)
}

func ledgerUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus ledger [ls|brief <lease-id>|accept <lease-id>] [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Read the lease ledger: what an orchestrating agent declared it would hand out,")
	fmt.Fprintln(os.Stderr, "as a tree of parents and the leases they spawned. Kept per repository, so every")
	fmt.Fprintln(os.Stderr, "worktree and clone reads one plan.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  ls       the rows and their overlaps (the default)")
	fmt.Fprintln(os.Stderr, "  brief    print one lease's worker brief")
	fmt.Fprintln(os.Stderr, "  accept   grade a worker's report (JSON on stdin) against its row")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Declaring the plan is the "+hint.ToolLedger.String()+" MCP tool's job, not this verb's.")
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

// ledgerRow is one printed line: the row plus how deep its parent chain runs.
type ledgerRow struct {
	lease types.Lease
	depth int
}

// ledgerTreeOrder flattens the rows parent-first, each lease followed by the leases it
// handed out, preserving ledger order among siblings.
//
// Every row reaches the output. One whose parent was cleared, and one caught in a parent
// cycle, print at the top level instead of disappearing: a lease nobody can see is worse
// than one shown without its indentation, and both cases mean the plan is already damaged.
func ledgerTreeOrder(leases []types.Lease) []ledgerRow {
	children := map[string][]types.Lease{}
	for _, lease := range leases {
		children[lease.Parent] = append(children[lease.Parent], lease)
	}
	out := make([]ledgerRow, 0, len(leases))
	emitted := map[string]bool{}
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		for _, lease := range children[parent] {
			if emitted[lease.ID] {
				continue
			}
			emitted[lease.ID] = true
			out = append(out, ledgerRow{lease: lease, depth: depth})
			walk(lease.ID, depth+1)
		}
	}
	walk("", 0)
	for _, lease := range leases {
		if !emitted[lease.ID] {
			emitted[lease.ID] = true
			out = append(out, ledgerRow{lease: lease})
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
			fmt.Fprintln(os.Stderr, "dependencies, the graph's blast radius for each owned path, and the fixed")
			fmt.Fprintln(os.Stderr, "blocks this workspace's "+ledger.BriefTemplatePath+" carries.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "It renders context and never a verdict: magus assembles what it holds and")
			fmt.Fprintln(os.Stderr, "you hand it to the worker, the way `magus diff --prompt` does.")
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
	override := root
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
	if reason := briefRefusesTheGate(ctx, override, row); reason != "" {
		return errors.New(reason)
	}

	brief := ledger.NewBrief(row)
	brief.Evidence, brief.GraphCold = leaseGraphEvidence(ctx, override, row.OwnedPaths)
	if brief.Projects, brief.Derived, brief.Affinity, err = leaseBoundary(ctx, override, row, leases); err != nil {
		return err
	}
	if brief.Footer, err = leaseBriefFooter(root, row); err != nil {
		return err
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputName:
		return emitNames([]string{row.ID})
	case outputText:
		fmt.Print(brief.Text())
		return nil
	default:
		return emitFormatted(opts, brief)
	}
}

// ledgerAccept grades one worker's report against its row and records the verdict.
//
// THE ENFORCEMENT POINT. Everything else about a lease is a declaration: the ledger
// records, the guard grades writes as they happen, and acceptance was the root agent
// reading a paragraph. A report that ran a filtered subset, wrote outside its boundary, or
// cited evidence that no longer resolves reads exactly like one that did the work, and
// this is where that stops being true.
//
// Exits non-zero on a rejection, because the caller is a shell step in an integration
// sequence and a verdict nothing can branch on is a verdict nobody acts on.
func ledgerAccept(ctx context.Context, root string, args []string) error {
	var af *gen.LedgerAcceptFlags
	pos, err := cmdParse("ledger accept", args, func(fs *flag.FlagSet) {
		af = gen.BindLedgerAccept(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus ledger accept <lease-id> [flags]  < report.json")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Grade a finished worker's report, read as JSON on stdin, against the lease it was")
			fmt.Fprintln(os.Stderr, "handed: every changed path inside the declared owned paths, the validation passed,")
			fmt.Fprintln(os.Stderr, "and its output ref still resolving in this workspace's store. A row that passes is")
			fmt.Fprintln(os.Stderr, "recorded "+string(types.StatePass)+"; a rejection names every rule that failed and exits non-zero.")
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
	if af.Schema {
		fmt.Print(ledger.ReportSchema)
		return nil
	}
	if len(pos) != 1 {
		return usagef("magus ledger accept: requires exactly one lease id, with the worker's report on stdin")
	}
	override := root
	root = resolveRootOrEmpty(root)

	var report ledger.Report
	if err := json.NewDecoder(os.Stdin).Decode(&report); err != nil {
		return fmt.Errorf("magus ledger accept: read the report on stdin: %w (`%s` prints the schema it must satisfy)", err, hint.LedgerAccept.With("--schema"))
	}

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
		return fmt.Errorf("magus ledger accept: no lease %q is declared (run `%s` to see the plan)", pos[0], hint.Ledger)
	}

	verdict, err := ledger.Accept(leases[i], report, outputRefResolves(ctx, override))
	if err != nil {
		return err
	}
	// Recorded before it is printed: a verdict the operator reads and the ledger does not
	// carry is the split the row's state exists to close.
	if verdict.Accepted {
		if _, err := store.Update(ctx, verdict.Lease, func(u *types.Lease) { u.State = types.StatePass }); err != nil {
			return err
		}
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

// outputRefResolves answers whether a run's captured output is still in this workspace's
// store, through the lookup `magus query output` uses. A ref that aged out of the cache
// is reported MISSING rather than as an error: the root cannot reopen it either way, and
// that is the fact acceptance turns on.
func outputRefResolves(ctx context.Context, root string) ledger.OutputLookup {
	return func(ref string) (bool, error) {
		m, err := loadMagus(ctx, root)
		if err != nil {
			return false, err
		}
		switch _, _, err := m.OutputByRef(ref); {
		case err == nil:
			return true, nil
		case errors.Is(err, fs.ErrNotExist):
			return false, nil
		default:
			return false, err
		}
	}
}

func printLeaseVerdict(out io.Writer, v ledger.Verdict) {
	if v.Accepted {
		fmt.Fprintf(out, "accepted %s, recorded %s\n", v.Lease, types.StatePass)
		return
	}
	fmt.Fprintf(out, "rejected %s, and its row is unchanged\n", v.Lease)
	for _, violation := range v.Violations {
		fmt.Fprintf(out, "  %s\n", violation)
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
func leaseBoundary(ctx context.Context, root string, row types.Lease, leases []types.Lease) ([]string, []ledger.BriefBoundary, []ledger.BriefAffinity, error) {
	if len(row.OwnedPaths) == 0 {
		return nil, nil, nil, nil
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return nil, nil, nil, err
	}
	affected, err := m.AffectedFromPaths(ctx, row.OwnedPaths)
	if err != nil {
		return nil, nil, nil, err
	}
	derived := generatedBoundary(m, affected.Affected, row.OwnedPaths)
	derived = append(derived, leasedBoundary(row, leases)...)
	derived = append(derived, sharedBoundary(m, affected.Seed)...)
	return affected.Affected, derived, leaseAffinity(ctx, m, affected.Seed), nil
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

// briefRefusesTheGate reports why this row must not be briefed, or "" to render it.
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
func briefRefusesTheGate(ctx context.Context, root string, row types.Lease) string {
	fix := fmt.Sprintf(" The gate runs ONCE, in the orchestrator's tree, after every unit lands."+
		" Give this row the narrowest target covering its paths (`%s` decomposes what the gate chains) with the %s tool, then ask for the brief again.",
		hint.DescribeTarget.With(types.TargetCI+" <project>"), hint.ToolLedger)

	if validationNamesGate(row.Validation) {
		return fmt.Sprintf("magus ledger brief: lease %s is assigned %q, which names the `%s` gate.%s", row.ID, row.Validation, types.TargetCI, fix)
	}
	if chain := validationReachesGate(ctx, root, row.Validation); len(chain) > 0 {
		return fmt.Sprintf("magus ledger brief: lease %s is assigned %q, and that target reaches the `%s` gate through %s.%s",
			row.ID, row.Validation, types.TargetCI, strings.Join(chain, " -> "), fix)
	}
	return ""
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

// leaseBriefFooter renders the workspace's footer template, or nothing when the workspace
// ships none.
//
// An ABSENT template is not an error: the fixed blocks are the workspace's to own, and
// owning none is a legitimate answer for a workspace that has not written one yet. A
// template that exists and will not render IS an error, because the person who put it
// there meant it to appear.
func leaseBriefFooter(root string, row types.Lease) (string, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ledger.BriefTemplatePath)))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return ledger.RenderBriefFooter(string(raw), row)
}
