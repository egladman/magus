package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/job/evidence"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
)

// jobEvidenceEnv is what internal/job/evidence asks of the command line.
func jobEvidenceEnv() evidence.Env {
	return evidence.Env{
		Config:  globalCfg,
		Version: version,
		Options: cliOptions(),
		LoadMagus: func(ctx context.Context, root string) (*magus.Magus, error) {
			return loadMagus(ctx, root)
		},
		RootStale: staleIndexes,
		FindStale: func(ctx context.Context, m *magus.Magus) []types.KnowledgeStaleIndex {
			return findStaleIndexes(ctx, m)
		},
		RootRefsGraph: func(ctx context.Context, root string) (*knowledge.Graph, error) {
			return loadKnowledgeGraphForRefs(ctx, root, false, "")
		},
		RootDomainGraph: func(ctx context.Context, root string) (*knowledge.Graph, error) {
			return loadKnowledgeGraph(ctx, root, false, false, false)
		},
		RefsGraph: func(ctx context.Context, m *magus.Magus) (*knowledge.Graph, error) {
			return loadRefsGraph(ctx, m, globalCfg, false, "")
		},
		DomainGraph: func(ctx context.Context, m *magus.Magus) (*knowledge.Graph, error) {
			return graphReads.LoadGraph(ctx, m, globalCfg, false, false, false)
		},
		Notes: func(ctx context.Context, m *magus.Magus, row jobtypes.Job) ([]job.RegeneratedOutput, []job.UnrunGate) {
			notes := collectNotes(ctx, m, row)
			return notes.RegeneratedOutside, notes.UnrunGates
		},
		HolderCell:         job.HolderCell,
		GateMergeScanLimit: gateMergeScanLimit,
	}
}

func newJobGraph(root string, rebuild bool) *evidence.Graph {
	return evidence.New(root, rebuild, jobEvidenceEnv())
}

func jobRefusesTheGate(ctx context.Context, root string, row jobtypes.Job) error {
	return evidence.RefusesTheGate(ctx, root, row, jobEvidenceEnv())
}

func leaseBoundary(ctx context.Context, root string, store *job.Store, row jobtypes.Job, leases []jobtypes.Job, graph *evidence.Graph) job.TermsFacts {
	return evidence.LeaseBoundary(ctx, root, store, row, leases, graph, jobEvidenceEnv())
}

func jobSymbolReader(root string, rebuild bool) job.SymbolReader {
	return evidence.JobSymbolReader(root, rebuild, jobEvidenceEnv())
}

func graphsByTree(root string, rootGraph *evidence.Graph, rebuild bool) func(tree string) *evidence.Graph {
	return evidence.ByTree(root, rootGraph, rebuild, jobEvidenceEnv())
}

func jobObserver(root string, graphIn func(tree string) *evidence.Graph) job.Observer {
	return evidence.Observer(root, graphIn, jobEvidenceEnv())
}

// jobGraphCommand is `magus job graph`: the live plan as figures that fit
// magus/figure's budgets. -o svg writes them and prints each path.
func jobGraphCommand(ctx context.Context, root string, args []string) error {
	var gf *flaggen.JobGraphFlags
	pos, err := commandParse("job graph", args, func(fs *flag.FlagSet) {
		gf = flaggen.BindJobGraph(fs)
		fs.Usage = func() { jobGraphUsage(fs) }
	})
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return exit.Usagef("magus job graph: takes no arguments (got %q)", pos[0])
	}
	opts, err := output.Parse(global.output, outputSVG)
	if err != nil {
		return exit.Usagef("magus job graph: %s", err)
	}
	if gf.Dir != "" && opts.Format != outputSVG {
		return exit.Usagef("magus job graph: --dir names where -o svg writes; add -o svg")
	}
	root = resolveRootOrEmpty(root)
	store, err := openJobs(ctx, root, jobOpen{})
	if err != nil {
		return err
	}
	list, err := store.Report(ctx, time.Now().Unix(), globalCfg.Jobs.StaleAfter)
	if err != nil {
		return err
	}
	list, _ = job.WithoutEnded(list)
	plan := job.NewPlan(list.Jobs)
	figs := plan.Figures()
	switch opts.Format {
	case outputSVG:
		dir, err := evidence.PlanSVGDir(root, gf.Dir, globalCfg)
		if err != nil {
			return err
		}
		paths, err := evidence.WritePlanSVGs(ctx, plan, figs, dir, job.HolderCell)
		if err != nil {
			return err
		}
		return emitNames(os.Stdout, paths)
	case outputName:
		ids := make([]string, len(figs))
		for i, f := range figs {
			ids[i] = f.ID
		}
		return emitNames(os.Stdout, ids)
	case outputText:
		plan.Print(os.Stdout, figs, job.HolderCell)
		return nil
	default:
		return emitFormatted(opts, figs)
	}
}

func jobGraphUsage(fs *flag.FlagSet) {
	fmt.Fprintln(os.Stderr, "Usage: magus job graph [-o svg] [--dir <dir>] [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Draw the live jobs as a plan: one box per job, an edge per fork,")
	fmt.Fprintln(os.Stderr, "merge and declared dependency, each box tagged with its state and")
	fmt.Fprintln(os.Stderr, "naming its holder. A plan larger than one figure holds splits into an")
	fmt.Fprintln(os.Stderr, "overview, one box per group of jobs, and a figure per group.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Without -o it prints each figure's boxes and edges. -o svg writes")
	fmt.Fprintln(os.Stderr, "each figure to <id>.svg in --dir and prints one path per line,")
	fmt.Fprintln(os.Stderr, "the overview first. The same plan writes the same bytes.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Example:")
	fmt.Fprintln(os.Stderr, "  magus job graph -o svg --dir /tmp/plan")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
	printOwnDefaults(fs)
}

// outputSVG is `magus job graph`'s own -o format.
const outputSVG output.Format = "svg"
