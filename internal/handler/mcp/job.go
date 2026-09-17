package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// jobTool (magus_job) declares jobs and lets a holder act on one: an orchestrating agent
// forks a job, a holder takes it (exec) and later returns its work, and anyone lists the
// plan. It is the AGENT's write door onto internal/job; magus\job and `magus job` are the
// others, and the daemon's JobService reads the same file.
//
// It blocks no write to the tree: the AGENT GUARD is what reads these rows to grade one,
// and exec's status is returned and stored as a fact rather than a refusal.
//
// WHAT IT DOES REFUSE is a write to a row the caller does not own, and that refusal comes
// from the STORE rather than from here, so every door carries it. See
// internal/job.authorizeRow.
//
// The op set is list | fork | exec | exit | wait, mirroring the CLI verbs
// (hint.LsJobs, hint.JobFork, hint.JobExec, hint.JobExit, hint.JobWait). Lifecycle
// operations delegate to internal/job, the single capability boundary shared with the
// CLI and Buzz; this adapter only decodes MCP's JSON-shaped result map.
type jobTool struct {
	store   *job.Store
	resolve job.AttemptResolver
	observe job.Observer
	// limits and symbols hold a fork to the same jobs limits and symbol-gate check the CLI
	// applies. A zero limits and a nil symbols check nothing.
	limits  config.Jobs
	symbols job.SymbolReader
}

func (t *jobTool) Name() string { return hint.ToolJob.String() }

func (t *jobTool) Invoke(ctx context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	switch op := paramString(req.Params, "op", "list"); op {
	case "list":
		jobs, err := t.store.List()
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		// The report, not the bare rows: the overlaps ride along, derived on this read
		// by the same constructor the console's route uses, so the two doors cannot
		// disagree about whether two jobs claim the same path.
		return spells.InvokeResponse{Data: types.NewJobList(jobs)}, nil

	case "fork":
		merge, err := job.ParseMerge(req.Params)
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		// Update, not List-then-Put: the merge has to read and write the row under one
		// lock. Two concurrent forks on one id (an orchestrator advancing state while a
		// worker records its checkpoint) would each read the row before the other wrote
		// it, and the second write would revert the first one's field.
		stored, err := job.ForkMerge(ctx, t.store, strings.TrimSpace(paramString(req.Params, "id", "")), merge, t.limits, t.symbols)
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		return spells.InvokeResponse{Data: stored}, nil

	case "exec":
		// Text as well as Data, and the only op here that sets it. A worker calls this to
		// learn where it stands, and "base_verdict":"diverged" in a record is a field it
		// has to know to look for; the sentence names both revisions and what to do next.
		stored, err := t.store.Exec(ctx,
			strings.TrimSpace(paramString(req.Params, "id", "")),
			paramString(req.Params, "reported_base", ""))
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		// The verdict is a fact this tool records and never acts on, and counting it is the
		// same read one step further out: how often a fleet's workers land on the base they
		// were handed. types.JobBaseVerdict is a closed set of four, so it is safe as
		// an attribute; the job id beside it is not, and stays off.
		if p := observability.FromContext(ctx); p != nil && stored.BaseVerdict != "" {
			p.RecordLeaseRegistration(ctx, string(stored.BaseVerdict))
		}
		return spells.InvokeResponse{Text: job.BaseAdvice(stored), Data: stored}, nil

	case "exit":
		result, err := jobResultParam(req.Params)
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		stored, err := job.Exit(ctx, t.store, strings.TrimSpace(paramString(req.Params, "id", "")), result, t.resolve)
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		return spells.InvokeResponse{Data: stored}, nil

	case "wait":
		result, err := jobResultParam(req.Params)
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		status, err := job.Wait(ctx, t.store, strings.TrimSpace(paramString(req.Params, "id", "")), result, t.resolve, t.observe)
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		return spells.InvokeResponse{Data: status}, nil

	case "clear":
		// Report what was dropped. Clearing is how a fresh plan starts, and it is also
		// how one orchestrator silently erases another's plan; a count is the cheapest
		// way for the caller to notice it wiped rows it did not write.
		dropped, err := t.store.Clear(ctx)
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		return spells.InvokeResponse{Data: map[string]any{"cleared": dropped}}, nil

	default:
		return spells.InvokeResponse{}, errors.New("mcp: job op must be one of list, fork, exec, exit, wait, clear")
	}
}

// jobResultParam preserves the strict, versioned JSON contract used by the CLI and
// Buzz. MCP maps are transport values, not a second result schema.
func jobResultParam(params map[string]any) (*types.JobResult, error) {
	v, present := params["result"]
	if !present || v == nil {
		return nil, errors.New("mcp: job result is required")
	}
	result, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("mcp: job result must be an object")
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("mcp: encode job result: %w", err)
	}
	decoded, err := job.DecodeResult(strings.NewReader(string(raw)))
	if err != nil {
		return nil, err
	}
	return &decoded, nil
}

var _ spells.Driver = (*jobTool)(nil)

// symbolReader answers a symbol gate from the graph this daemon already serves.
//
// Resolved per name rather than loaded once, because the resolver shards the symbol index
// and routes an exact symbol id to the shards that can hold it; asking for the whole graph
// to answer about one name would load every shard to read one.
func symbolReader(g graphResolver) job.SymbolReader {
	return func(ctx context.Context, name string) (job.SymbolFact, bool) {
		loaded, err := g.KnowledgeGraphWithSymbolsForRef(ctx, name)
		if err != nil {
			return job.SymbolFact{}, false
		}
		return job.GraphSymbols(loaded)(ctx, name)
	}
}
