package main

import (
	"bytes"
	"fmt"
	"go/format"
	"reflect"
	"strconv"
	"strings"

	queuetypes "github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/libs/gopherbuzz/buzzgen"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// providerOp is one function a provider spell exports and magus calls with a Target and a
// typed request. Req is a RuntimeObject in boundaryTypes, since magus encodes it, and Resp
// is listed in inboundTypes, since magus decodes it.
type providerOp struct {
	Family string
	Op     string
	Req    reflect.Type
	Resp   reflect.Type
	// Optional marks an op a provider may leave out: its caller reads the missing export
	// as a capability the provider lacks and says so.
	Optional bool
}

// queueFamily is the merge queue's provider script, which is not a spell: its ops take no
// Target, and their rows go to decode.QueueFuncs, so the spell contract check never holds
// a spell's own describe or mark to them.
const queueFamily = "queue"

// providerOps is every provider op, the one list the generated ProviderOp values, the
// contract check's ProviderFuncs and the Buzz signatures in the docs come from. Two
// families may share an op name only with one signature, since a spell carrying both
// contracts exports one function.
var providerOps = []providerOp{
	{Family: "secret", Op: "resolve_secret", Req: reflect.TypeFor[types.SecretRef](), Resp: reflect.TypeFor[spells.Secret]()},

	{Family: "ci", Op: "enabled", Req: reflect.TypeFor[types.Empty](), Resp: reflect.TypeFor[types.Ack](), Optional: true},
	{Family: "ci", Op: "group_start", Req: reflect.TypeFor[types.CIGroup](), Resp: reflect.TypeFor[types.Ack](), Optional: true},
	{Family: "ci", Op: "group_end", Req: reflect.TypeFor[types.CIGroupEnd](), Resp: reflect.TypeFor[types.Ack](), Optional: true},
	{Family: "ci", Op: "annotate", Req: reflect.TypeFor[types.CIAnnotation](), Resp: reflect.TypeFor[types.Ack](), Optional: true},
	{Family: "ci", Op: "quote_prefixes", Req: reflect.TypeFor[types.Empty](), Resp: reflect.TypeFor[types.Strings](), Optional: true},
	{Family: "ci", Op: "last_green_run", Req: reflect.TypeFor[types.Empty](), Resp: reflect.TypeFor[types.GreenRun](), Optional: true},

	{Family: "cache", Op: "enabled", Req: reflect.TypeFor[types.Empty](), Resp: reflect.TypeFor[types.Ack](), Optional: true},
	{Family: "cache", Op: "get_artifact", Req: reflect.TypeFor[types.ArtifactRequest](), Resp: reflect.TypeFor[types.Ack]()},
	{Family: "cache", Op: "has_artifact", Req: reflect.TypeFor[types.ArtifactRequest](), Resp: reflect.TypeFor[types.Ack](), Optional: true},
	{Family: "cache", Op: "put_artifact", Req: reflect.TypeFor[types.ArtifactRequest](), Resp: reflect.TypeFor[types.Ack]()},
	{Family: "cache", Op: "prune", Req: reflect.TypeFor[types.PruneRequest](), Resp: reflect.TypeFor[types.PruneResult](), Optional: true},

	{Family: "lifecycle", Op: "list_lifecycles", Req: reflect.TypeFor[types.LifecycleQuery](), Resp: reflect.TypeFor[[]spells.Lifecycle]()},

	{Family: "review", Op: "find_review", Req: reflect.TypeFor[types.FindReviewRequest](), Resp: reflect.TypeFor[types.ReviewTarget]()},
	{Family: "review", Op: "publish_review", Req: reflect.TypeFor[types.PublishReviewRequest](), Resp: reflect.TypeFor[types.Ack]()},
	{Family: "review", Op: "reply_review", Req: reflect.TypeFor[types.ReplyReviewRequest](), Resp: reflect.TypeFor[types.Ack]()},
	{Family: "review", Op: "review_threads", Req: reflect.TypeFor[types.ReviewRef](), Resp: reflect.TypeFor[[]types.ReviewComment](), Optional: true},

	{Family: "workspace", Op: "list_projects", Req: reflect.TypeFor[types.ProjectQuery](), Resp: reflect.TypeFor[[]spells.ProvidedProject]()},

	{Family: queueFamily, Op: "describe", Req: reflect.TypeFor[queuetypes.DescribeRequest](), Resp: reflect.TypeFor[queuetypes.DescribeResult]()},
	{Family: queueFamily, Op: "list_changes", Req: reflect.TypeFor[queuetypes.ListChangesRequest](), Resp: reflect.TypeFor[queuetypes.Changes]()},
	{Family: queueFamily, Op: "approval_at", Req: reflect.TypeFor[queuetypes.ApprovalRequest](), Resp: reflect.TypeFor[queuetypes.Approval]()},
	{Family: queueFamily, Op: "list_green", Req: reflect.TypeFor[queuetypes.ListGreenRequest](), Resp: reflect.TypeFor[[]queuetypes.GreenChange]()},
	{Family: queueFamily, Op: "post_status", Req: reflect.TypeFor[queuetypes.StatusRequest](), Resp: reflect.TypeFor[types.Ack]()},
	{Family: queueFamily, Op: "retarget", Req: reflect.TypeFor[queuetypes.RetargetRequest](), Resp: reflect.TypeFor[types.Ack]()},
	{Family: queueFamily, Op: "merge_change", Req: reflect.TypeFor[queuetypes.MergeRequest](), Resp: reflect.TypeFor[queuetypes.MergeAnswer]()},
	{Family: queueFamily, Op: "kick_back", Req: reflect.TypeFor[queuetypes.KickBackRequest](), Resp: reflect.TypeFor[types.Ack]()},
	{Family: queueFamily, Op: "mark", Req: reflect.TypeFor[queuetypes.MarkRequest](), Resp: reflect.TypeFor[types.Ack]()},
	{Family: queueFamily, Op: "list_artifacts", Req: reflect.TypeFor[queuetypes.ListArtifactsRequest](), Resp: reflect.TypeFor[queuetypes.ArtifactListing](), Optional: true},
	{Family: queueFamily, Op: "required_checks", Req: reflect.TypeFor[queuetypes.RequiredChecksRequest](), Resp: reflect.TypeFor[[]queuetypes.Check](), Optional: true},
	{Family: queueFamily, Op: "reviews", Req: reflect.TypeFor[queuetypes.ReviewsRequest](), Resp: reflect.TypeFor[queuetypes.Reviews](), Optional: true},
	{Family: queueFamily, Op: "dismiss_review", Req: reflect.TypeFor[queuetypes.DismissReviewRequest](), Resp: reflect.TypeFor[types.Ack](), Optional: true},
}

// providerVar names the generated ProviderOp value of op: the op in Go case, prefixed by
// its family when another family shares the op name.
func providerVar(op providerOp) string {
	name := goCase(op.Op)
	if op.Family == queueFamily {
		return "Queue" + name
	}
	for _, other := range providerOps {
		if other.Op == op.Op && other.Family != op.Family {
			return goCase(op.Family) + name
		}
	}
	return name
}

// goCase turns a snake_case name into an exported Go identifier, keeping the initialisms
// the family names use.
func goCase(snake string) string {
	var b strings.Builder
	for _, word := range strings.Split(snake, "_") {
		switch word {
		case "ci":
			b.WriteString("CI")
		case "":
		default:
			b.WriteString(strings.ToUpper(word[:1]) + word[1:])
		}
	}
	return b.String()
}

// providerSignature is the Buzz parameter list and return annotation op's export declares.
func providerSignature(op providerOp) (params, returns string, err error) {
	if _, ok := boundaryTypeNamed(buzzName(op.Req)); !ok {
		return "", "", fmt.Errorf("provider op %s: request %s is not registered in boundaryTypes", op.Op, op.Req)
	}
	returns, _, err = buzzgen.FieldType(op.Resp, mirrorOptions())
	if err != nil {
		return "", "", fmt.Errorf("provider op %s: %w", op.Op, err)
	}
	params = "req: " + buzzName(op.Req)
	if op.Family != queueFamily {
		params = "target: " + buzzName(reflect.TypeFor[types.Target]()) + ", " + params
	}
	return params, returns, nil
}

// checkProviderOps refuses a registry the generated code could not honor: a request that
// reaches an enum (its encoder would need the session's enum cases) or one op name read
// two ways.
func checkProviderOps() error {
	objects := newBuzzValueEmitter()
	seen := map[string]providerOp{}
	for _, op := range providerOps {
		entry, ok := boundaryTypeNamed(buzzName(op.Req))
		if !ok || !entry.RuntimeObject {
			return fmt.Errorf("provider op %s: request %s must be a RuntimeObject in boundaryTypes", op.Op, op.Req)
		}
		if objects.reachesEnum(op.Req) {
			return fmt.Errorf("provider op %s: request %s reaches an enum, which a ProviderOp encoder cannot write", op.Op, op.Req)
		}
		if prior, ok := seen[op.Op]; ok && (prior.Req != op.Req || prior.Resp != op.Resp) {
			return fmt.Errorf("provider op %s: families %s and %s declare it with different types", op.Op, prior.Family, op.Family)
		}
		seen[op.Op] = op
	}
	return nil
}

// emitProviderFuncs renders decode.ProviderFuncs, one ContractFunc per providerOps row in
// order, and the strict decoder each one's Decode calls.
func emitProviderFuncs(e *decodeEmitter) error {
	if err := checkProviderOps(); err != nil {
		return err
	}
	var spellTable, queueTable, decoders bytes.Buffer
	fmt.Fprintln(&spellTable, "// ProviderFuncs is every provider op a spell may export, one row per spell-family")
	fmt.Fprintln(&spellTable, "// cmd/magus-utils providerOps entry and in its order. Required is false on every row:")
	fmt.Fprintln(&spellTable, "// which ops a provider needs is its family's to say.")
	fmt.Fprintln(&spellTable, "var ProviderFuncs = []ContractFunc{")
	fmt.Fprintln(&queueTable, "// QueueFuncs is every op a merge queue provider script may export, one row per queue")
	fmt.Fprintln(&queueTable, "// providerOps entry and in its order. Its ops take no Target.")
	fmt.Fprintln(&queueTable, "var QueueFuncs = []ContractFunc{")
	for _, op := range providerOps {
		params, returns, err := providerSignature(op)
		if err != nil {
			return err
		}
		fn := "decodeProvider" + providerVar(op)
		table := &spellTable
		if op.Family == queueFamily {
			table = &queueTable
		}
		fmt.Fprintf(table, "\t{Name: %q, Returns: %q, Params: %q, Required: %t, Decode: func(v vm.Value) (any, error) { return %s(v) }},\n",
			op.Op, returns, params, op.Family == queueFamily && !op.Optional, fn)
		goType := e.goType(op.Resp)
		fmt.Fprintf(&decoders, "func %s(v vm.Value) (%s, error) {\n", fn, goType)
		fmt.Fprintf(&decoders, "\tvar out %s\n", goType)
		if err := e.assign(&decoders, "v", strconv.Quote(op.Op), "out", providerVar(op), op.Resp, "\t"); err != nil {
			return fmt.Errorf("provider op %s: %w", op.Op, err)
		}
		fmt.Fprintln(&decoders, "\treturn out, nil")
		fmt.Fprintln(&decoders, "}")
		fmt.Fprintln(&decoders)
	}
	for _, table := range []*bytes.Buffer{&spellTable, &queueTable} {
		fmt.Fprintln(table, "}")
		fmt.Fprintln(table)
		e.funcs.Write(table.Bytes())
	}
	e.funcs.Write(decoders.Bytes())
	return nil
}

// renderProviderOpType renders the bindgen file declaring ProviderOp. It holds no values:
// a value needs a decoder from package decode, whose round-trip test imports bindgen.
func renderProviderOpType(pkg string) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintln(&b, "// Code generated by magus-utils boundarydecode. DO NOT EDIT.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "package "+pkg)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, `import vm "github.com/egladman/magus/libs/gopherbuzz/vm"`)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "// ProviderOp is one function a provider spell exports: magus calls Name with a Target")
	fmt.Fprintln(&b, "// and Encode(req), and reads what it returns through Decode, which is strict.")
	fmt.Fprintln(&b, "type ProviderOp[Req, Resp any] struct {")
	fmt.Fprintln(&b, "\tFamily, Name string")
	fmt.Fprintln(&b, "\tEncode       func(Req) vm.Value")
	fmt.Fprintln(&b, "\tDecode       func(vm.Value) (Resp, error)")
	fmt.Fprintln(&b, "}")
	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, gofmtError(err, b.String())
	}
	return out, nil
}

// renderProviderOpVars renders package providerops: one bindgen.ProviderOp value per
// providerOps row, its encoder the generated bindgen.Object<Req> and its decoder the
// matching decode.ProviderFuncs row.
func renderProviderOpVars(bindingsPkg string) ([]byte, error) {
	if err := checkProviderOps(); err != nil {
		return nil, err
	}
	objects := newBuzzValueEmitter()
	e := newDecodeEmitter(nil)
	var vars bytes.Buffer
	rows := map[string]int{}
	for _, op := range providerOps {
		optional := "required"
		if op.Optional {
			optional = "optional"
		}
		table := "ProviderFuncs"
		if op.Family == queueFamily {
			table = "QueueFuncs"
		}
		req, resp := e.goType(op.Req), e.goType(op.Resp)
		fmt.Fprintf(&vars, "// %s is the %s provider's %s %s op.\n", providerVar(op), op.Family, optional, op.Op)
		fmt.Fprintf(&vars, "var %s = %s.ProviderOp[%s, %s]{Family: %q, Name: %q, Encode: %s.%s, Decode: decoded[%s](decode.%s, %d)}\n\n",
			providerVar(op), bindingsPkg, req, resp, op.Family, op.Op, bindingsPkg, objects.funcName(op.Req), resp, table, rows[table])
		rows[table]++
	}

	var b bytes.Buffer
	fmt.Fprintln(&b, "// Code generated by magus-utils boundarydecode. DO NOT EDIT.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "// Package providerops holds one ProviderOp per provider op cmd/magus-utils lists in")
	fmt.Fprintln(&b, "// providerOps. It imports both bindgen, for the encoders, and decode, for the decoders,")
	fmt.Fprintln(&b, "// which neither of those packages may do.")
	fmt.Fprintln(&b, "package providerops")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "import (")
	fmt.Fprintf(&b, "\t%s \"github.com/egladman/magus/internal/interp/bindings/gen\"\n", bindingsPkg)
	fmt.Fprintln(&b, `	"github.com/egladman/magus/internal/interp/bindings/gen/decode"`)
	fmt.Fprintln(&b, `	vm "github.com/egladman/magus/libs/gopherbuzz/vm"`)
	b.WriteString(importLines(e.pkgs))
	fmt.Fprintln(&b, ")")
	fmt.Fprintln(&b)
	b.Write(vars.Bytes())
	fmt.Fprintln(&b, "// decoded reads row i of table as Resp. The row decodes into exactly Resp, so the")
	fmt.Fprintln(&b, "// assertion holds by construction.")
	fmt.Fprintln(&b, "func decoded[Resp any](table []decode.ContractFunc, i int) func(vm.Value) (Resp, error) {")
	fmt.Fprintln(&b, "\treturn func(v vm.Value) (Resp, error) {")
	fmt.Fprintln(&b, "\t\tout, err := table[i].Decode(v)")
	fmt.Fprintln(&b, "\t\tif err != nil {")
	fmt.Fprintln(&b, "\t\t\tvar zero Resp")
	fmt.Fprintln(&b, "\t\t\treturn zero, err")
	fmt.Fprintln(&b, "\t\t}")
	fmt.Fprintln(&b, "\t\treturn out.(Resp), nil")
	fmt.Fprintln(&b, "\t}")
	fmt.Fprintln(&b, "}")
	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, gofmtError(err, b.String())
	}
	return out, nil
}
