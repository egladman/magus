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
// skill defines. It is the write door onto internal/ledger; the console's
// /api/v1/ledger endpoint is the read door onto the same file.
//
// It records and never enforces. magus does not check that a worker stayed inside its
// owned paths, does not block a write outside them, and derives no verdict from a row.
// See types.DelegationUnit.
type ledgerTool struct{ store *ledger.Store }

func (t *ledgerTool) Name() string { return ToolLedger.String() }

func (t *ledgerTool) Invoke(_ context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	switch op := paramString(req.Params, "op", "list"); op {
	case "list":
		units, err := t.store.List()
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		return spells.InvokeResponse{Data: map[string]any{"units": units}}, nil

	case "put":
		stored, err := t.store.Put(types.DelegationUnit{
			ID:             strings.TrimSpace(paramString(req.Params, "id", "")),
			Parent:         strings.TrimSpace(paramString(req.Params, "parent", "")),
			Goal:           paramString(req.Params, "goal", ""),
			Checkpoint:     strings.TrimSpace(paramString(req.Params, "checkpoint", "")),
			OwnedPaths:     strings.Fields(paramString(req.Params, "owned_paths", "")),
			ForbiddenPaths: strings.Fields(paramString(req.Params, "forbidden_paths", "")),
			DependsOn:      strings.Fields(paramString(req.Params, "depends_on", "")),
			Tier:           strings.TrimSpace(paramString(req.Params, "tier", "")),
			Validation:     paramString(req.Params, "validation", ""),
			State:          types.DelegationState(strings.TrimSpace(paramString(req.Params, "state", ""))),
			ReadOnly:       paramBool(req.Params, "read_only", false),
		})
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
