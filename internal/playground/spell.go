package playground

import (
	"context"
	"strings"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"

	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
)

// mgsListTargets is the export a SPELL buffer must provide: a fun returning a map
// of op name -> handler. Its presence in the export set is what distinguishes a
// spell buffer from a magusfile (whose targets are the exported funs themselves).
const mgsListTargets = "mgs_listTargets"

// mgsGetName is the required spell name export. Not load-bearing for the dry run,
// but its presence alongside mgs_listTargets confirms a well-formed spell buffer.
const mgsGetName = "mgs_getName"

// isSpell reports whether a session's exports look like a SPELL buffer rather than
// a magusfile: a spell exports mgs_listTargets (op name -> handler map) instead of
// exporting each target as a top-level fun.
func isSpell(sess *buzz.Session) bool {
	_, ok := sess.Exports()[mgsListTargets]
	return ok
}

// spellOp is one op discovered from a spell buffer: its name, its resolved kind
// (types.OpKindService | types.OpKindCommand), the command it declares, and any
// kind-coherence ward the ward package raised for it (e.g. MGS5002 for a detached
// service). The op is still discovered even when warded, so `ls`/`graph` list it.
type spellOp struct {
	name  string
	kind  string
	cmd   types.Command
	wards []*types.DiagnosticError
}

// probeSpell resolves a SPELL buffer to its ops. It mirrors internal/spell's
// resolve path (call mgs_listTargets, call each handler once with a null Target,
// MapView the returned Command/Service), but keeps every op even when the ward
// package flags it, so a warded op (10-wards.buzz) still lists and surfaces its
// diagnostic rather than aborting the whole load. Handler-call errors are swallowed
// per op: a partial op set is more useful than none. Returns ops sorted by name.
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
		// and not read the Target, so a null Target is passed here (mirroring
		// internal/spell.recordOp); a value pulled from it would read as null.
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
// recognized by its `command` field (the supervised process); its embedded Command
// mirrors that field. A Command op reads bin/args directly. Mirrors the
// service-vs-command decision in internal/spell.recordOp / decode.
func decodeSpellOp(name string, mv vm.Value) spellOp {
	if cmdV, ok := mv.MapGet("command"); ok {
		// A Service: its `command` field is the process magus supervises.
		if cv, ok := cmdV.MapView(); ok {
			return spellOp{name: name, kind: types.OpKindService, cmd: readCommand(cv)}
		}
		return spellOp{name: name, kind: types.OpKindService}
	}
	return spellOp{name: name, kind: types.OpKindCommand, cmd: readCommand(mv)}
}

// readCommand reads a Command field map (bin + args) into a types.Command. Charms
// are not modeled in the dry run (the ward checks read only bin/args).
func readCommand(m vm.Value) types.Command {
	c := types.Command{}
	if bin, ok := m.MapGet("bin"); ok && bin.IsStr() {
		c.Bin = bin.AsString()
	}
	if args, ok := m.MapGet("args"); ok {
		c.Args = valToStrings(args)
	}
	return c
}

// detail joins a command's bin and args into the display detail an Op carries.
func (o spellOp) detail() string {
	parts := make([]string, 0, len(o.cmd.Args)+1)
	if o.cmd.Bin != "" {
		parts = append(parts, o.cmd.Bin)
	}
	parts = append(parts, o.cmd.Args...)
	return strings.Join(parts, " ")
}

// sortSpellOps sorts ops by name for a deterministic ls/graph/run order.
func sortSpellOps(ops []spellOp) {
	for i := 1; i < len(ops); i++ {
		for j := i; j > 0 && ops[j-1].name > ops[j].name; j-- {
			ops[j-1], ops[j] = ops[j], ops[j-1]
		}
	}
}

// wardDetail renders a diagnostic as the single-line Detail an Op of Kind "ward"
// carries, leading with the MGSxxxx code so the console shows the code and message.
func wardDetail(d *types.DiagnosticError) string {
	return "[" + string(d.Code) + "] " + d.Msg
}

// dryRunSpell builds the dry-run plan for one spell op: the op line (Kind
// "service" or "command", Detail = bin + args) followed by one "ward" op per
// kind-coherence diagnostic the ward package raised for it (e.g. MGS5002 for a
// detached service). A spell op has no dependency closure, so the order is just the
// op itself. An unknown op name is a diagnostic, mirroring the magusfile path.
func dryRunSpell(ops []spellOp, opName, output string) DryRunResult {
	var op *spellOp
	for i := range ops {
		if ops[i].name == opName {
			op = &ops[i]
			break
		}
	}
	if op == nil {
		return DryRunResult{Output: output, Diag: &Diag{Msg: "unknown op: " + opName}}
	}
	trace := []Op{{Target: op.name, Kind: op.kind, Name: op.name, Detail: op.detail()}}
	for _, w := range op.wards {
		trace = append(trace, Op{Target: op.name, Kind: "ward", Name: string(w.Code), Detail: wardDetail(w)})
	}
	return DryRunResult{OK: true, Order: []string{op.name}, Trace: trace, Output: output}
}
