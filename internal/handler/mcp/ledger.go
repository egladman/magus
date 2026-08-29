package mcp

import (
	"context"
	"errors"
	"strings"

	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// ledgerTool (magus_ledger) records the lease ledger an orchestrating agent
// declares: one row per lease, in the vocabulary the magus-multi-agent
// skill defines. It is one write door onto internal/ledger - magus\ledger's put, register
// and clear (internal/interp/bindings/ledger_ns.go) are the other - and the console's
// /api/v1/ledger endpoint is the read door onto the same file.
//
// It records and refuses nothing. This tool does not check that a worker stayed inside its
// owned paths and does not block a write outside them; the AGENT GUARD is what reads these
// rows to grade a write, and it is elsewhere. The one verdict here - register's, on whether
// a worker's reported base is the checkpoint its lease was handed - is returned and stored
// as a fact, and the registration succeeds whatever it says, because a ledger that started
// refusing is a ledger agents route around. See types.Lease.
type ledgerTool struct{ store *ledger.Store }

func (t *ledgerTool) Name() string { return ToolLedger.String() }

func (t *ledgerTool) Invoke(ctx context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	switch op := paramString(req.Params, "op", "list"); op {
	case "list":
		leases, err := t.store.List()
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		// The report, not the bare rows: the overlaps ride along, derived on this read
		// by the same constructor the console's route uses, so the two doors cannot
		// disagree about whether two leases claim the same path.
		return spells.InvokeResponse{Data: types.NewLeaseReport(leases)}, nil

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

	case "register":
		// Text as well as Data, and the only op here that sets it. A worker calls this to
		// learn where it stands, and "base_verdict":"diverged" in a record is a field it
		// has to know to look for; the sentence names both revisions and what to do next.
		stored, err := t.store.Register(ctx,
			strings.TrimSpace(paramString(req.Params, "id", "")),
			paramString(req.Params, "reported_base", ""))
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		// The verdict is a fact this tool records and never acts on, and counting it is the
		// same read one step further out: how often a fleet's workers land on the base they
		// were handed. types.LeaseBaseVerdict is a closed set of four, so it is safe as
		// an attribute; the lease id beside it is not, and stays off.
		if p := observability.FromContext(ctx); p != nil && stored.BaseVerdict != "" {
			p.RecordLeaseRegistration(ctx, string(stored.BaseVerdict))
		}
		return spells.InvokeResponse{Text: ledger.RegistrationAdvice(stored), Data: stored}, nil

	case "clear":
		// Report what was dropped. Clearing is how a fresh plan starts, and it is also
		// how one orchestrator silently erases another's plan; a count is the cheapest
		// way for the caller to notice it wiped rows it did not write.
		before, err := t.store.List()
		if err != nil {
			return spells.InvokeResponse{}, err
		}
		if err := t.store.Clear(ctx); err != nil {
			return spells.InvokeResponse{}, err
		}
		return spells.InvokeResponse{Data: map[string]any{"cleared": len(before)}}, nil

	default:
		return spells.InvokeResponse{}, errors.New("mcp: ledger op must be one of list, put, register, clear")
	}
}

var _ spells.Driver = (*ledgerTool)(nil)
