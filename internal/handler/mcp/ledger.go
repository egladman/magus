package mcp

import (
	"context"
	"errors"
	"strings"

	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// ledgerTool (magus_ledger) records the delegation ledger an orchestrating agent
// declares: one row per delegated unit, in the vocabulary the magus-delegate-multi-agent
// skill defines. It is one write door onto internal/ledger - magus\ledger.put and
// magus\ledger.clear (internal/interp/bindings/ledger_ns.go) are the other - and the
// console's /api/v1/ledger endpoint is the read door onto the same file.
//
// It records and never enforces. magus does not check that a worker stayed inside its
// owned paths, does not block a write outside them, and derives no verdict from a row.
// See types.DelegationUnit.
type ledgerTool struct{ store *ledger.Store }

func (t *ledgerTool) Name() string { return ToolLedger.String() }

func (t *ledgerTool) Invoke(ctx context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	switch op := paramString(req.Params, "op", "list"); op {
	case "list":
		units, err := t.store.List()
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		// The report, not the bare rows: the overlaps ride along, derived on this read
		// by the same constructor the console's route uses, so the two doors cannot
		// disagree about whether two units claim the same path.
		return spells.InvokeResponse{Data: types.NewDelegationReport(units)}, nil

	case "put":
		merge, err := ledger.Merge(req.Params)
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		// Update, not List-then-Put: the merge has to read and write the row under one
		// lock. Two concurrent puts on one id (an orchestrator advancing state while a
		// worker records its checkpoint) would each read the row before the other wrote
		// it, and the second write would revert the first one's field.
		stored, err := t.store.Update(ctx, strings.TrimSpace(paramString(req.Params, "id", "")), merge)
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		return spells.InvokeResponse{Data: stored}, nil

	case "clear":
		// Report what was dropped. Clearing is how a fresh plan starts, and it is also
		// how one orchestrator silently erases another's plan; a count is the cheapest
		// way for the caller to notice it wiped rows it did not write.
		before, err := t.store.List()
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		if err := t.store.Clear(); err != nil {
			return spells.InvokeResponse{}, err
		}
		return spells.InvokeResponse{Data: map[string]any{"cleared": len(before)}}, nil

	default:
		return spells.InvokeResponse{}, errors.New("mcp: ledger op must be one of list, put, clear")
	}
}

var _ spells.Driver = (*ledgerTool)(nil)
