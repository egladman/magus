package main

//go:generate go run . boundarylist -out ../../internal/interp/bindings/gen/boundary_list.go -pkg bindgen
//go:generate go run . boundaryobjects -out ../../internal/interp/bindings/gen/boundary_objects.go -pkg bindgen

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/generate/emit"
	"github.com/egladman/magus/internal/hostmodules"
	"github.com/egladman/magus/libs/gopherbuzz/buzzgen"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
	"github.com/egladman/magus/types/daemon"
)

// boundaryRoots are the types no typed host signature or provider op
// reaches. A type any of them reaches through a field crosses too.
// Reflect sees only types compiled code names (ADR 0035).
var boundaryRoots = []boundaryRoot{
	// The spell contract (internal/spell/contract.go). contract.go parses it
	// rather than linking it, so magus decodes these from a spell by name.
	{Type: reflect.TypeFor[spells.Manifest](), Inbound: true},
	{Type: reflect.TypeFor[spells.Service](), Inbound: true},
	{Type: reflect.TypeFor[spells.Coverage](), Inbound: true},
	{Type: reflect.TypeFor[types.Path](), Inbound: true},
	{Type: reflect.TypeFor[spells.Tool](), Inbound: true},
	{Type: reflect.TypeFor[spells.Language](), Inbound: true},
	// A resolved spell's handle carries these back to Buzz as well.
	{Type: reflect.TypeFor[spells.SymbolIndexer](), Inbound: true, Outbound: true},
	{Type: reflect.TypeFor[spells.Sandbox](), Inbound: true, Outbound: true},
	{Type: reflect.TypeFor[spells.Command](), Outbound: true},
	// The harness contract (types/harness_contract.go): a harness spell's
	// mgs_ functions return these, and magus hands it a ShippedAgent.
	{Type: reflect.TypeFor[types.Harness](), Inbound: true},
	{Type: reflect.TypeFor[types.HarnessSkills](), Inbound: true},
	{Type: reflect.TypeFor[types.HookCommand](), Inbound: true},
	{Type: reflect.TypeFor[types.HookBinding](), Inbound: true},
	{Type: reflect.TypeFor[types.ManagedEntries](), Inbound: true},
	{Type: reflect.TypeFor[types.McpServer](), Inbound: true},
	{Type: reflect.TypeFor[types.HarnessModel](), Inbound: true},
	{Type: reflect.TypeFor[types.HarnessTools](), Inbound: true},
	{Type: reflect.TypeFor[types.AgentFile](), Inbound: true},
	{Type: reflect.TypeFor[types.Prompt](), Inbound: true},
	{Type: reflect.TypeFor[types.Setting](), Inbound: true},
	{Type: reflect.TypeFor[types.Limits](), Inbound: true},
	{Type: reflect.TypeFor[types.ShippedAgent](), Outbound: true},
	// Returned by bindings internal/interp/bindings assembles by hand, so
	// no Impl signature names them.
	{Type: reflect.TypeFor[types.CommitRecord](), Outbound: true},
	{Type: reflect.TypeFor[types.StatusRecord](), Outbound: true},
	{Type: reflect.TypeFor[types.DriftResultRecord](), Outbound: true},
	{Type: reflect.TypeFor[types.ModuleEntry](), Outbound: true},
	{Type: reflect.TypeFor[types.ProjectsOutput](), Outbound: true},
	{Type: reflect.TypeFor[types.ToolReport](), Outbound: true},
	{Type: reflect.TypeFor[types.TargetGraphOutput](), Outbound: true},
	{Type: reflect.TypeFor[types.FileReport](), Outbound: true},
	{Type: reflect.TypeFor[types.Spell](), Outbound: true},
	{Type: reflect.TypeFor[types.CharmEntry](), Outbound: true},
	{Type: reflect.TypeFor[types.TargetEntry](), Outbound: true},
	{Type: reflect.TypeFor[types.EvaluatedTarget](), Outbound: true},
	{Type: reflect.TypeFor[types.EvaluatedProject](), Outbound: true},
	{Type: reflect.TypeFor[types.WorkspaceEntry](), Outbound: true},
	{Type: reflect.TypeFor[types.RuleDoc](), Outbound: true},
	{Type: reflect.TypeFor[types.HarnessInstallation](), Outbound: true},
	{Type: reflect.TypeFor[types.MCPTool](), Outbound: true},
	{Type: reflect.TypeFor[types.Conformance](), Outbound: true},
	{Type: reflect.TypeFor[daemon.Run](), Outbound: true},
	{Type: reflect.TypeFor[types.GuardBinary](), Outbound: true},
	{Type: reflect.TypeFor[types.ServiceLease](), Outbound: true},
	// allow, advise and deny build the verdict a guard rule returns.
	{Type: reflect.TypeFor[types.GuardVerdict](), Outbound: true},
	// A script builds these and nothing hands one back. Result is declared
	// at its placeholder; a method returning Result[X] brings its encoder.
	{Type: reflect.TypeFor[types.Declaration]()},
	{Type: reflect.TypeFor[types.Brief]()},
	{Type: reflect.TypeFor[types.Result[buzzgen.T]]()},
	{Type: reflect.TypeFor[agent.ModelTierBounds]()},
	// An Arg.Enum names these, and its Impl takes a plain string.
	{Type: reflect.TypeFor[types.SignAlgorithm]()},
	{Type: reflect.TypeFor[types.LogLevel]()},
	{Type: reflect.TypeFor[types.TimeLayout]()},
	{Type: reflect.TypeFor[types.PlatformStyle]()},
	{Type: reflect.TypeFor[types.TermStyle]()},
}

// boundaryRoot is one declared root and the directions it crosses in.
// Neither direction declares a mirror only.
type boundaryRoot struct {
	Type     reflect.Type
	Inbound  bool
	Outbound bool
}

// boundaryType is one Go struct that crosses into Buzz.
type boundaryType struct {
	Name string
	// Module is the import path Name is declared in, as for vcs\CommandOptions.
	// A nested one, magus/agent, has its own file. Empty declares Name in
	// each module reaching it.
	Module string
	Type   reflect.Type
	// Outbound marks a type Go hands to Buzz, which needs an encoder.
	Outbound bool
	// Inbound marks a type Buzz hands to Go, which needs a strict decoder.
	Inbound bool
	// TS marks a type tstypes also writes as TypeScript for the console and docs.
	TS bool
	// Required names the fields a value must set. The decoder refuses a
	// value without one, and the mirror gives it no default.
	Required []string
}

// key is the registry's name for the entry: Name, qualified by its
// namespace when Module scopes it.
func (b boundaryType) key() string { return qualifiedName(b.Module, b.Name) }

// nested reports whether the entry is declared in a nested namespace's file.
func (b boundaryType) nested() bool { return strings.Contains(b.Module, "/") }

// qualifiedName spells name in the namespace of the module at path, or bare
// for no module.
func qualifiedName(module, name string) string {
	if module == "" {
		return name
	}
	return fmt.Sprintf(`%s\%s`, strings.ReplaceAll(module, "/", `\`), name)
}

// nameIn is how a declaration in the module at path spells the entry: bare
// in its own namespace or when bundled, else qualified.
func (b boundaryType) nameIn(module string) string {
	if b.nested() && b.Module != module {
		return b.key()
	}
	return b.Name
}

// boundarySet is every type that crosses, derived once from the host
// modules, the provider ops and boundaryRoots.
type boundarySet struct {
	types []boundaryType
	enums []boundaryEnum
	// open holds each named string that crosses as str, with its reason.
	open map[reflect.Type]string
	// prog is the one load of every package a crossing type lives in.
	prog *buzzgen.Program
}

// boundary derives the set on first use, because most subcommands never
// read it. A derivation error stops the generator.
var boundary = sync.OnceValue(func() boundarySet {
	set, err := deriveBoundary()
	if err != nil {
		fmt.Fprintln(os.Stderr, "magus-utils:", err)
		os.Exit(1)
	}
	return set
})

// boundaryTypes lists every crossing struct, ordered by key.
func boundaryTypes() []boundaryType { return boundary().types }

// boundaryEnumList lists every crossing enum, ordered by package and Go
// name.
func boundaryEnumList() []boundaryEnum { return boundary().enums }

// inboundTypes lists every struct Buzz hands to Go, ordered by key.
func inboundTypes() []boundaryType {
	var out []boundaryType
	for _, entry := range boundaryTypes() {
		if entry.Inbound {
			out = append(out, entry)
		}
	}
	return out
}

func deriveBoundary() (boundarySet, error) {
	root, err := buzzgenRoot()
	if err != nil {
		return boundarySet{}, err
	}
	cfg, err := buzzgen.ReadConfig(filepath.Join(root, ".buzzgen.yaml"))
	if err != nil {
		return boundarySet{}, err
	}
	r := buzzgen.NewReach(cfg)
	for _, sig := range hostSignatures(hostmodules.All()) {
		r.Walk(sig.Type, buzzgen.Outbound, sig.Owner)
	}
	for _, op := range providerOps {
		label := fmt.Sprintf("provider op %s.%s", op.Family, op.Op)
		r.Walk(op.Req, buzzgen.Outbound, label)
		r.Walk(op.Resp, buzzgen.Inbound, label)
	}
	for _, rt := range boundaryRoots {
		label := fmt.Sprintf("root %s", rt.Type)
		r.Walk(rt.Type, buzzgen.Mirrored, label)
		if rt.Inbound {
			r.Walk(rt.Type, buzzgen.Inbound, label)
		}
		if rt.Outbound {
			r.Walk(rt.Type, buzzgen.Outbound, label)
		}
	}
	if err := r.Refused(); err != nil {
		return boundarySet{}, err
	}
	set := boundarySet{types: boundaryMembers(r.Members()), open: map[reflect.Type]string{}}
	if set.prog, err = buzzgen.Load(root, cfg, r.Packages()...); err != nil {
		return boundarySet{}, err
	}
	if set.enums, err = deriveEnums(r, set.prog, set.open); err != nil {
		return boundarySet{}, err
	}
	var claims []buzzgen.Claim
	for _, entry := range set.types {
		claims = append(claims, buzzgen.Claim{Key: entry.key(), Type: entry.Type})
	}
	var enumKeys []string
	for _, e := range set.enums {
		claims = append(claims, buzzgen.Claim{Key: e.key(), Type: e.Type})
		enumKeys = append(enumKeys, e.goKey)
	}
	return set, errors.Join(r.CheckNames(claims), r.CheckListed(enumKeys...), r.CheckNamespaces(hostMembers))
}

// hostMembers lists the functions and member groups of the host module at
// path, the names a nested namespace under it may not take.
func hostMembers(path string) []string {
	var out []string
	for _, mod := range hostmodules.All() {
		if mod.Name != path {
			continue
		}
		for _, m := range mod.Methods {
			out = append(out, m.Name, std.CamelCase(m.Name), m.BuzzName)
		}
		for _, n := range mod.Namespaces {
			out = append(out, n.Name)
		}
	}
	return out
}

// hostSignature is one method's Go func type and the member that owns it.
type hostSignature struct {
	Owner string
	Type  reflect.Type
}

// hostSignatures lists the signature of every method the modules declare,
// on the module, its namespaces, objects and protocols.
func hostSignatures(mods []std.Module) []hostSignature {
	var out []hostSignature
	add := func(owner string, ms []std.Method) {
		for _, m := range ms {
			if sig := m.Signature(); sig != nil {
				out = append(out, hostSignature{Owner: fmt.Sprintf("%s.%s", owner, m.Name), Type: sig})
			}
		}
	}
	for _, mod := range mods {
		add(mod.Name, mod.Methods)
		for _, n := range mod.Namespaces {
			add(fmt.Sprintf("%s.%s", mod.Name, n.Name), n.Methods)
		}
		for _, o := range mod.Objects {
			add(fmt.Sprintf("%s.%s", mod.Name, o.Name), o.Methods)
		}
		for _, p := range mod.Protocols {
			add(fmt.Sprintf("%s.%s", mod.Name, p.Name), p.Methods)
		}
	}
	return out
}

// boundaryMembers converts each reached struct to the entry the renderers
// read.
func boundaryMembers(members []buzzgen.Member) []boundaryType {
	out := make([]boundaryType, 0, len(members))
	for _, m := range members {
		entry := boundaryType{
			Name:     m.Ref.BuzzName(),
			Type:     m.Type,
			Outbound: m.Outbound,
			Inbound:  m.Inbound,
			Required: m.Ref.Settings.Required,
		}
		if m.Ref.Settings.Module != nil {
			entry.Module = *m.Ref.Settings.Module
		}
		if m.Ref.Settings.TS != nil {
			entry.TS = *m.Ref.Settings.TS
		}
		out = append(out, entry)
	}
	return out
}

// openStringTypes are the named string types that cross as a bare str on
// purpose, each with the reason .buzzgen.yaml's open setting gives.
func openStringTypes() map[reflect.Type]string { return boundary().open }

// checkNamedStrings refuses a field of rt whose type, or element type, is a named string
// that is neither an enum nor open.
func checkNamedStrings(rt reflect.Type) error {
	var unregistered []string
	for _, f := range emit.ObjectFields(rt) {
		if t, found := unregisteredString(f.Type); found {
			unregistered = append(unregistered, fmt.Sprintf("%s (%s)", f.Name, t))
		}
	}
	if len(unregistered) > 0 {
		return fmt.Errorf("%s: field %s is a named string type that does not cross: reach it from a root, or set open on it in .buzzgen.yaml",
			rt, strings.Join(unregistered, ", "))
	}
	return nil
}

// unregisteredString is the named string type ft holds through slices,
// pointers and maps, when it is neither an enum nor open.
func unregisteredString(ft reflect.Type) (reflect.Type, bool) {
	ft = innermost(ft)
	_, enum := buzzEnum(ft)
	_, listed := openStringTypes()[ft]
	return ft, ft.Kind() == reflect.String && ft.PkgPath() != "" && !enum && !listed
}

// innermost is the type ft holds through any slices, arrays, pointers and maps.
func innermost(ft reflect.Type) reflect.Type {
	for ft.Kind() == reflect.Slice || ft.Kind() == reflect.Array || ft.Kind() == reflect.Pointer || ft.Kind() == reflect.Map {
		ft = ft.Elem()
	}
	return ft
}

type boundaryEnum struct {
	Name string
	// Type is nil for an enum only its module setting declares, which no
	// signature or field reaches.
	Type  reflect.Type
	Cases []enumCase
	// Module declares the enum in that host module's bundle although no signature or
	// field names it: named values for a field that is a str because its set is open.
	Module string
	// goKey is package.Name, the line .buzzgen.yaml keys its settings by.
	goKey string
}

// key is the enum's Buzz name, qualified by its namespace when Module scopes
// it, as boundaryType.key is.
func (e boundaryEnum) key() string { return qualifiedName(e.Module, e.Name) }

// nested reports whether the enum is declared in a nested namespace's own file.
func (e boundaryEnum) nested() bool { return strings.Contains(e.Module, "/") }

// nameIn is how a declaration in the module at path spells the enum, as
// boundaryType.nameIn spells a mirror.
func (e boundaryEnum) nameIn(module string) string {
	if e.nested() && e.Module != module {
		return e.key()
	}
	return e.Name
}

// enumCase is one case: the Buzz identifier and the string it carries.
type enumCase struct {
	Name  string
	Value string
}

// buzzEnum returns the enum a Go type mirrors as, if any.
func buzzEnum(rt reflect.Type) (boundaryEnum, bool) {
	for _, e := range boundaryEnumList() {
		if e.Type == rt {
			return e, true
		}
	}
	return boundaryEnum{}, false
}

// buzzName returns the Buzz name a Go type mirrors as: its registry key,
// not always its Go name (types.ProjectsOutput is `Projects`). An
// unclaimed type falls back to its Go name, which the caller reports.
func buzzName(rt reflect.Type) string { return buzzNameIn("", rt) }

// buzzNameIn is buzzName as a declaration in the module at path spells it.
func buzzNameIn(module string, rt reflect.Type) string {
	for _, entry := range boundaryTypes() {
		if entry.Type == rt {
			return entry.nameIn(module)
		}
	}
	return rt.Name()
}

// requiredFields is mirrorOptions' Required: the fields a crossing struct
// requires.
func requiredFields(rt reflect.Type) []string {
	for _, entry := range boundaryTypes() {
		if entry.Type == rt {
			return entry.Required
		}
	}
	return nil
}

// hasMethods reports whether a module declares methods on rt's mirror. Such a
// mirror is encoded as an instance of the session's object, never a map: a map
// has no methods to dispatch.
func hasMethods(rt reflect.Type) bool {
	for _, mod := range hostmodules.All() {
		if _, ok := mirrorObject(mod, rt); ok {
			return true
		}
	}
	return false
}

// mirrorObject finds the std.Object mod declares on rt's mirror.
func mirrorObject(mod std.Module, rt reflect.Type) (std.Object, bool) {
	for _, o := range mod.Objects {
		if entry, ok := boundaryTypeIn(mod.Name, o.Name); ok && entry.Type == rt {
			return o, true
		}
	}
	return std.Object{}, false
}

// boundaryTypeNamed finds an entry by its key: a bare name, or module\Name for one a
// module scopes.
func boundaryTypeNamed(key string) (boundaryType, bool) {
	for _, entry := range boundaryTypes() {
		if entry.key() == key {
			return entry, true
		}
	}
	return boundaryType{}, false
}

// boundaryTypeIn finds the entry name means inside module's declarations: the one module
// scopes, else the workspace-wide one.
func boundaryTypeIn(module, name string) (boundaryType, bool) {
	if entry, ok := boundaryTypeNamed(qualifiedName(module, name)); ok {
		return entry, true
	}
	return boundaryTypeNamed(name)
}

// boundaryEnumNamed finds a declared enum by its Buzz name. The counterpart to
// boundaryTypeNamed, for the generator that has to declare an enum a signature
// references.
func boundaryEnumNamed(name string) (boundaryEnum, bool) {
	for _, e := range boundaryEnumList() {
		if e.Name == name || e.key() == name {
			return e, true
		}
	}
	return boundaryEnum{}, false
}

// nestedModules lists, sorted, the import path of every nested namespace a
// crossing type or enum is declared in.
func nestedModules() []string {
	var out []string
	for _, entry := range boundaryTypes() {
		if entry.nested() && !slices.Contains(out, entry.Module) {
			out = append(out, entry.Module)
		}
	}
	for _, e := range boundaryEnumList() {
		if e.nested() && !slices.Contains(out, e.Module) {
			out = append(out, e.Module)
		}
	}
	slices.Sort(out)
	return out
}
