package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/types"
)

// ledgerCmd implements `magus ledger`, the READ-ONLY door onto the lease ledger.
//
// Read-only is the whole shape of it. put, register and clear stay on the magus_ledger
// MCP tool and magus\ledger, because an orchestrating AGENT writes the plan; this verb
// exists because the person orchestrating that agent is the one who has to see it, and
// until now had to read a JSON file or open the console to do so.
func ledgerCmd(ctx context.Context, root string, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			ledgerUsage()
			return nil
		case hint.LedgerBrief.Leaf():
			return ledgerBrief(ctx, root, args[1:])
		case "ls":
			args = args[1:]
		default:
			// A flag falls through to the default listing, which is what a bare
			// `magus ledger -o json` has to reach.
			if !strings.HasPrefix(args[0], "-") {
				return usagef("magus ledger: unknown subcommand %q (want ls or brief)", args[0])
			}
		}
	}
	return ledgerList(root, args)
}

func ledgerUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus ledger [ls|brief <lease-id>] [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Read the lease ledger: what an orchestrating agent declared it would hand out,")
	fmt.Fprintln(os.Stderr, "as a tree of parents and the leases they spawned. Kept per repository, so every")
	fmt.Fprintln(os.Stderr, "worktree and clone reads one plan.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  ls       the rows and their overlaps (the default)")
	fmt.Fprintln(os.Stderr, "  brief    print one lease's worker brief")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Writing the plan is the "+hint.ToolLedger.String()+" MCP tool's job, not this verb's.")
}

func openLedger(root string) (*ledger.Store, error) {
	cacheDir, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
	if err != nil {
		return nil, err
	}
	return ledger.NewStore(ledger.Location{CacheDir: cacheDir, Root: root}), nil
}

func ledgerList(root string, args []string) error {
	if _, err := cmdParse("ledger ls", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus ledger ls [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print the declared leases as a tree, each with its state, tier, owned-path")
			fmt.Fprintln(os.Stderr, "count and validation, followed by every pair that claims the same path.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	}); err != nil {
		return err
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
		for i, u := range report.Leases {
			ids[i] = u.ID
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
	for _, u := range ledgerTreeOrder(report.Leases) {
		fmt.Fprintf(w, "%s%s\t%s\t%s\t%d\t%s\n",
			strings.Repeat("  ", u.depth), u.lease.ID,
			dashIfEmpty(string(u.lease.State)), dashIfEmpty(u.lease.Tier),
			len(u.lease.OwnedPaths), dashIfEmpty(u.lease.Validation))
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
	for _, u := range leases {
		children[u.Parent] = append(children[u.Parent], u)
	}
	out := make([]ledgerRow, 0, len(leases))
	emitted := map[string]bool{}
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		for _, u := range children[parent] {
			if emitted[u.ID] {
				continue
			}
			emitted[u.ID] = true
			out = append(out, ledgerRow{lease: u, depth: depth})
			walk(u.ID, depth+1)
		}
	}
	walk("", 0)
	for _, u := range leases {
		if !emitted[u.ID] {
			emitted[u.ID] = true
			out = append(out, ledgerRow{lease: u})
		}
	}
	return out
}

func dashIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
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
	store, err := openLedger(root)
	if err != nil {
		return err
	}
	leases, err := store.List()
	if err != nil {
		return err
	}
	i := slices.IndexFunc(leases, func(u types.Lease) bool { return u.ID == pos[0] })
	if i < 0 {
		return fmt.Errorf("magus ledger brief: no lease %q is declared (run `%s` to see the plan)", pos[0], hint.Ledger)
	}
	row := leases[i]

	brief := ledger.Brief{Lease: row, Bind: ledger.BriefBind(row.ID)}
	brief.Evidence, brief.GraphCold = leaseGraphEvidence(ctx, root, row.OwnedPaths)
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

// leaseGraphEvidence resolves each owned path against the knowledge graph, one line per
// path the graph knows.
//
// A path the graph cannot resolve is skipped SILENTLY, because most owned paths are
// ordinary source directories the containment tree does not carry, and a "no node" line
// per path would bury the ones that do resolve. A graph that would not load at all is
// different and is reported once, through the cold flag: "not asked" must not read as
// "nothing depends on this".
func leaseGraphEvidence(ctx context.Context, root string, paths []string) ([]ledger.BriefEvidence, bool) {
	if len(paths) == 0 {
		return nil, false
	}
	g, err := loadKnowledgeGraph(ctx, root, false, false, false)
	if err != nil {
		return nil, true
	}
	var out []ledger.BriefEvidence
	for _, p := range paths {
		out = append(out, pathEvidence(g, p)...)
	}
	return out, false
}

// pathEvidence answers for one declared path, trying the node ids a path can carry in the
// containment tree.
//
// It accepts only a node whose id IS the ref it asked for. Explain resolves a bare name
// fuzzily, which is right for a person typing `magus explain build` and wrong here: asked
// for "cmd/magus" it answered target:.:release-sign, and a brief that hands a worker the
// blast radius of an unrelated node is worse than one that stays quiet.
func pathEvidence(g *knowledge.Graph, declared string) []ledger.BriefEvidence {
	for _, ref := range []string{types.KindDir + ":" + declared, types.KindFile + ":" + declared, declared} {
		out, ok := g.Explain(ref)
		if !ok || out.Node.ID != ref {
			continue
		}
		return []ledger.BriefEvidence{{Path: declared, Node: out.Node.ID, BlastRadius: out.BlastRadius}}
	}
	return nil
}

// leaseBriefFooter renders the workspace's footer template, or nothing when the workspace
// ships none.
//
// An ABSENT template is not an error: the fixed blocks are the workspace's to own, and
// owning none is a legitimate answer for a workspace that has not written one yet. A
// template that exists and will not render IS an error, because the person who put it
// there meant it to appear.
func leaseBriefFooter(root string, u types.Lease) (string, error) {
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ledger.BriefTemplatePath)))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return ledger.RenderBriefFooter(string(raw), u)
}
