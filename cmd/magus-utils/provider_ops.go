package main

import (
	"bytes"
	"fmt"
	"go/format"
	"reflect"
	"strconv"
	"strings"

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
}

// providerVar names the generated ProviderOp value of op: the op in Go case, prefixed by
// its family when another family shares the op name.
func providerVar(op providerOp) string {
	name := goCase(op.Op)
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
	return "target: " + buzzName(reflect.TypeFor[types.Target]()) + ", req: " + buzzName(op.Req), returns, nil
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
	var table, decoders bytes.Buffer
	fmt.Fprintln(&table, "// ProviderFuncs is every provider op a spell may export, one row per")
	fmt.Fprintln(&table, "// cmd/magus-utils providerOps entry and in its order. Required is false on every row:")
	fmt.Fprintln(&table, "// which ops a provider needs is its family's to say.")
	fmt.Fprintln(&table, "var ProviderFuncs = []ContractFunc{")
	for _, op := range providerOps {
		params, returns, err := providerSignature(op)
		if err != nil {
			return err
		}
		fn := "decodeProvider" + providerVar(op)
		fmt.Fprintf(&table, "\t{Name: %q, Returns: %q, Params: %q, Decode: func(v vm.Value) (any, error) { return %s(v) }},\n",
			op.Op, returns, params, fn)
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
	fmt.Fprintln(&table, "}")
	fmt.Fprintln(&table)
	e.funcs.Write(table.Bytes())
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
	for i, op := range providerOps {
		optional := "required"
		if op.Optional {
			optional = "optional"
		}
		req, resp := e.goType(op.Req), e.goType(op.Resp)
		fmt.Fprintf(&vars, "// %s is the %s provider's %s %s op.\n", providerVar(op), op.Family, optional, op.Op)
		fmt.Fprintf(&vars, "var %s = %s.ProviderOp[%s, %s]{Family: %q, Name: %q, Encode: %s.%s, Decode: decoded[%s](%d)}\n\n",
			providerVar(op), bindingsPkg, req, resp, op.Family, op.Op, bindingsPkg, objects.funcName(op.Req), resp, i)
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
	if e.pkgs["spells"] {
		fmt.Fprintln(&b, `	"github.com/egladman/magus/spells"`)
	}
	if e.pkgs["types"] {
		fmt.Fprintln(&b, `	"github.com/egladman/magus/types"`)
	}
	fmt.Fprintln(&b, ")")
	fmt.Fprintln(&b)
	b.Write(vars.Bytes())
	fmt.Fprintln(&b, "// decoded reads row i of decode.ProviderFuncs as Resp. The row decodes into exactly")
	fmt.Fprintln(&b, "// Resp, so the assertion holds by construction.")
	fmt.Fprintln(&b, "func decoded[Resp any](i int) func(vm.Value) (Resp, error) {")
	fmt.Fprintln(&b, "\treturn func(v vm.Value) (Resp, error) {")
	fmt.Fprintln(&b, "\t\tout, err := decode.ProviderFuncs[i].Decode(v)")
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
