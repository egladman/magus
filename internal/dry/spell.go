package dry

import (
	"context"
	"slices"
	"strings"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"

	ispell "github.com/egladman/magus/internal/spell"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
)

// mgsListTargets is the export a SPELL buffer must provide: a fun returning a map
// of op name -> handler. Its presence in the export set is what distinguishes a
// spell buffer from a magusfile (whose targets are the exported funs themselves).
const mgsListTargets = "mgs_listTargets"

// isSpell reports whether a session's exports look like a SPELL buffer rather than
// a magusfile: a spell exports mgs_listTargets (op name -> handler map) instead of
// exporting each target as a top-level fun.
func isSpell(sess *buzz.Session) bool {
	_, ok := sess.Exports()[mgsListTargets]
	return ok
}

// spellOp is one op discovered from a spell buffer: its name, resolved kind
// (types.OpKindService | types.OpKindCommand), declared command, any kind-coherence
// ward raised for it (e.g. MGS5002 for a detached service), and a decodeErr set when
// its Command could not be decoded (a malformed charm patch). The op is discovered
// even when warded or undecodable, so `ls`/`graph` list it; a `run` surfaces
// decodeErr rather than a blank command.
type spellOp struct {
	name      string
	kind      string
	cmd       types.Command
	wards     []*types.DiagnosticError
	decodeErr error
}

// probeSpell resolves a SPELL buffer to its ops. It mirrors internal/spell's
// resolve path (call mgs_listTargets, call each handler once with a null Target,
// MapView the returned Command/Service), but keeps every op even when warded, so a
// warded op (10-wards.buzz) still lists and surfaces its diagnostic rather than
// aborting the whole load. Handler-call errors are swallowed per op: a partial op set
// is more useful than none. Returns ops sorted by name.
func probeSpell(ctx context.Context, sess *buzz.Session) []spellOp {
	list, ok := sess.Exports()[mgsListTargets]
	if !ok {
		return nil
	}
	m, err := sess.CallValue(ctx, list, nil)
	if err != nil || !m.IsMap() {
		return nil
	}
	var ops []spellOp
	for _, name := range m.MapKeys() {
		handler, _ := m.MapGet(name)
		if !handler.IsFun() {
			continue
		}
		// The handler is `fun(t: Target) > Command|Service`. It must be straight-line
		// and not read the Target, so a null Target is passed (mirroring
		// internal/spell.traceOp); a value pulled from it would read as null.
		rv, err := sess.CallValue(ctx, handler, []vm.Value{vm.Null})
		if err != nil {
			continue
		}
		mv, ok := rv.MapView()
		if !ok {
			continue
		}
		op := decodeSpellOp(name, mv)
		op.wards = ward.Check(name, types.SpellOp{Kind: op.kind, Command: op.cmd})
		ops = append(ops, op)
	}
	sortSpellOps(ops)
	return ops
}

// decodeSpellOp classifies a handler's returned object into a spellOp. A Service is
// recognized by its `command` field (the supervised process); a Command op decodes
// bin/args/charms directly. Both route through the shared spell.DecodeCommandValue so
// the sandbox and the engine read a command identically; a decode error is carried on
// the op (decodeErr) so `run` can surface it. Mirrors the service-vs-command decision
// in internal/spell.traceOp / decode.
func decodeSpellOp(name string, mv vm.Value) spellOp {
	if cmdV, ok := mv.MapGet("command"); ok {
		// A Service: its `command` field is the process magus supervises.
		if cv, ok := cmdV.MapView(); ok {
			cmd, err := ispell.DecodeCommandValue(cv)
			return spellOp{name: name, kind: types.OpKindService, cmd: cmd, decodeErr: err}
		}
		return spellOp{name: name, kind: types.OpKindService}
	}
	cmd, err := ispell.DecodeCommandValue(mv)
	return spellOp{name: name, kind: types.OpKindCommand, cmd: cmd, decodeErr: err}
}

// renderCommand renders the op's command line for display: bin plus the argv reshaped
// by the active charms, via the same spell.ApplyCharms the engine's command binding
// uses, so the sandbox never drifts from the real reshaping. A charm-patch error (e.g.
// an out-of-range pointer that decode's structural check cannot catch) is returned, not
// swallowed, so the dry run refuses the plan exactly as the engine would rather than
// rendering the un-reshaped command as if the charm applied.
func (o spellOp) renderCommand(activeNames []string) (string, error) {
	args, err := ispell.ApplyCharms(o.cmd.Args, o.cmd.Charms, activeNames)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(args)+1)
	if o.cmd.Bin != "" {
		parts = append(parts, o.cmd.Bin)
	}
	parts = append(parts, args...)
	return strings.Join(parts, " "), nil
}

// sortSpellOps sorts ops by name for a deterministic ls/graph/run order.
func sortSpellOps(ops []spellOp) {
	slices.SortFunc(ops, func(a, b spellOp) int { return strings.Compare(a.name, b.name) })
}

// wardDetail renders a diagnostic as the single-line Detail an Op of Kind "ward"
// carries, leading with the MGSxxxx code so the console shows the code and message.
func wardDetail(d *types.DiagnosticError) string {
	return "[" + string(d.Code) + "] " + d.Msg
}

// dryRunSpell builds the dry-run plan for one spell op: the op line (Kind
// "service" or "command", Detail = bin + the charm-applied argv) followed by one
// "ward" op per kind-coherence diagnostic raised for it (e.g. MGS5002 for a detached
// service). A spell op has no dependency closure, so the order is just the op itself.
// An unknown op name, an undecodable command, or a charm patch that fails to apply is
// a diagnostic - the last two so the sandbox refuses exactly what the engine would
// rather than showing a wrong command.
func dryRunSpell(ops []spellOp, opName, output string, charms []string) Result {
	var op *spellOp
	for i := range ops {
		if ops[i].name == opName {
			op = &ops[i]
			break
		}
	}
	if op == nil {
		return Result{Output: output, Diag: &Diag{Msg: "unknown op: " + opName}}
	}
	if op.decodeErr != nil {
		return Result{Output: output, Diag: &Diag{Msg: op.name + ": " + op.decodeErr.Error()}}
	}
	detail, err := op.renderCommand(charms)
	if err != nil {
		return Result{Output: output, Diag: &Diag{Msg: op.name + ": " + err.Error()}}
	}
	trace := []Op{{Target: op.name, Kind: op.kind, Name: op.name, Detail: detail}}
	for _, w := range op.wards {
		trace = append(trace, Op{Target: op.name, Kind: "ward", Name: string(w.Code), Detail: wardDetail(w)})
	}
	return Result{OK: true, Order: []string{op.name}, Trace: trace, Output: output}
}
