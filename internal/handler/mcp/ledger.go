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
		// A put updates only the fields it names. The second write of a unit's
		// lifecycle (op=put id=u1 state=running) must not erase the declared
		// row; presence with an empty value is an explicit clear.
		id := strings.TrimSpace(paramString(req.Params, "id", ""))
		unit := types.DelegationUnit{ID: id}
		if id != "" {
			existing, err := t.store.List()
			if err != nil {
				return spells.InvokeResponse{}, err
			}
			for _, u := range existing {
				if u.ID == id {
					unit = u
					break
				}
			}
		}
		if v, ok := ledgerString(req.Params, "parent"); ok {
			unit.Parent = strings.TrimSpace(v)
		}
		if v, ok := ledgerString(req.Params, "goal"); ok {
			unit.Goal = v
		}
		if v, ok := ledgerString(req.Params, "checkpoint"); ok {
			unit.Checkpoint = strings.TrimSpace(v)
		}
		if v, ok := ledgerList(req.Params, "owned_paths"); ok {
			unit.OwnedPaths = v
		}
		if v, ok := ledgerList(req.Params, "forbidden_paths"); ok {
			unit.ForbiddenPaths = v
		}
		if v, ok := ledgerList(req.Params, "depends_on"); ok {
			unit.DependsOn = v
		}
		if v, ok := ledgerString(req.Params, "tier"); ok {
			unit.Tier = strings.TrimSpace(v)
		}
		if v, ok := ledgerString(req.Params, "validation"); ok {
			unit.Validation = v
		}
		if v, ok := ledgerString(req.Params, "state"); ok {
			s := types.DelegationState(strings.TrimSpace(v))
			switch s {
			case types.StateDeclared, types.StateRunning, types.StatePass, types.StateFail, types.StateNoReturn:
				unit.State = s
			default:
				return spells.InvokeResponse{}, errors.New("mcp: state must be one of declared, running, pass, fail, no_return")
			}
		}
		if v, present := req.Params["read_only"]; present {
			b, ok := v.(bool)
			if !ok {
				return spells.InvokeResponse{}, errors.New("mcp: read_only must be a boolean")
			}
			unit.ReadOnly = b
		}
		stored, err := t.store.Put(unit)
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

// ledgerString distinguishes an absent key from an empty value, which is what
// lets a put carry only the fields it means to change.
func ledgerString(params map[string]any, key string) (string, bool) {
	v, present := params[key]
	if !present {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// ledgerList accepts the natural JSON array shape as well as the
// space-separated string the descriptor schema forces on typed clients
// (magus_describe_file's paths set the precedent). A client sending a real
// array must not silently record nothing.
func ledgerList(params map[string]any, key string) ([]string, bool) {
	v, present := params[key]
	if !present {
		return nil, false
	}
	switch t := v.(type) {
	case string:
		return strings.Fields(t), true
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out, true
	}
	return nil, false
}

var _ spells.Driver = (*ledgerTool)(nil)
