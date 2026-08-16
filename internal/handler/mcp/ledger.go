package mcp

import (
	"context"
	"errors"
	"fmt"
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
		merge, err := ledgerMerge(req.Params)
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

// ledgerMerge builds the field merge one put applies: only the keys the request names,
// so the second write of a unit's lifecycle (op=put id=u1 state=running) advances the
// state without erasing the declared row, and presence with an empty value is an
// explicit clear.
//
// Every value is read and validated HERE rather than inside the merge, because the merge
// runs while the store holds its lock and has no way to report a failure. Errors are
// joined rather than returned at the first one: a client that mistyped two params should
// learn about both in one round trip.
func ledgerMerge(params map[string]any) (func(*types.DelegationUnit), error) {
	var (
		set []func(*types.DelegationUnit)
		err error
	)
	str := func(key string, apply func(*types.DelegationUnit, string)) {
		v, ok, e := ledgerString(params, key)
		switch {
		case e != nil:
			err = errors.Join(err, e)
		case ok:
			set = append(set, func(u *types.DelegationUnit) { apply(u, v) })
		}
	}
	list := func(key string, apply func(*types.DelegationUnit, []string)) {
		v, ok, e := ledgerList(params, key)
		switch {
		case e != nil:
			err = errors.Join(err, e)
		case ok:
			set = append(set, func(u *types.DelegationUnit) { apply(u, v) })
		}
	}

	str("parent", func(u *types.DelegationUnit, v string) { u.Parent = strings.TrimSpace(v) })
	str("goal", func(u *types.DelegationUnit, v string) { u.Goal = v })
	str("checkpoint", func(u *types.DelegationUnit, v string) { u.Checkpoint = strings.TrimSpace(v) })
	list("owned_paths", func(u *types.DelegationUnit, v []string) { u.OwnedPaths = v })
	list("forbidden_paths", func(u *types.DelegationUnit, v []string) { u.ForbiddenPaths = v })
	list("depends_on", func(u *types.DelegationUnit, v []string) { u.DependsOn = v })
	str("tier", func(u *types.DelegationUnit, v string) { u.Tier = strings.TrimSpace(v) })
	str("validation", func(u *types.DelegationUnit, v string) { u.Validation = v })

	// state is the one param with a vocabulary, so it is checked against it here.
	if v, ok, e := ledgerString(params, "state"); e != nil {
		err = errors.Join(err, e)
	} else if ok {
		s := types.DelegationState(strings.TrimSpace(v))
		switch s {
		case types.StateDeclared, types.StateRunning, types.StatePass, types.StateFail, types.StateNoReturn:
			set = append(set, func(u *types.DelegationUnit) { u.State = s })
		default:
			err = errors.Join(err, errors.New("mcp: state must be one of declared, running, pass, fail, no_return"))
		}
	}
	if v, present := params["read_only"]; present {
		b, ok := v.(bool)
		if !ok {
			err = errors.Join(err, errors.New("mcp: read_only must be a boolean"))
		} else {
			set = append(set, func(u *types.DelegationUnit) { u.ReadOnly = b })
		}
	}
	if err != nil {
		return nil, err
	}
	return func(u *types.DelegationUnit) {
		for _, apply := range set {
			apply(u)
		}
	}, nil
}

// ledgerString distinguishes an absent key from an empty value, which is what
// lets a put carry only the fields it means to change. A present key holding
// something that is not a string is an ERROR, not an absent one: silently
// dropping it records a row the client did not ask for, and the client is then
// told the put succeeded.
func ledgerString(params map[string]any, key string) (string, bool, error) {
	v, present := params[key]
	if !present {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", false, fmt.Errorf("mcp: %s must be a string", key)
	}
	return s, true, nil
}

// ledgerList accepts the natural JSON array shape as well as the
// space-separated string the descriptor schema forces on typed clients
// (magus_describe_file's paths set the precedent). A client sending a real
// array must not silently record nothing - nor may one element of the wrong
// type quietly shorten the list, so both are reported.
func ledgerList(params map[string]any, key string) ([]string, bool, error) {
	v, present := params[key]
	if !present {
		return nil, false, nil
	}
	badType := fmt.Errorf("mcp: %s must be an array of strings or a space-separated string", key)
	switch t := v.(type) {
	case string:
		return strings.Fields(t), true, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, false, badType
			}
			if strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out, true, nil
	}
	return nil, false, badType
}

var _ spells.Driver = (*ledgerTool)(nil)
