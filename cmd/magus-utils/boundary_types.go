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
	"time"

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
	{Type: reflect.TypeFor[types.ModelRow](), Inbound: true},
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
	// Module scopes Name to one host module's declarations, for a mirror whose name
	// another module's mirror also takes: magus\CommandOptions and vcs\CommandOptions.
	// Empty declares Name for every module.
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

// key is the registry's name for the entry: Name, qualified as module\Name when Module
// scopes it.
func (b boundaryType) key() string {
	if b.Module == "" {
		return b.Name
	}
	return fmt.Sprintf(`%s\%s`, b.Module, b.Name)
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
	r := newReach(cfg)
	r.hostModules(hostmodules.All())
	for _, op := range providerOps {
		label := fmt.Sprintf("provider op %s.%s", op.Family, op.Op)
		r.walk(op.Req, crossOut, label)
		r.walk(op.Resp, crossIn, label)
	}
	for _, rt := range boundaryRoots {
		label := fmt.Sprintf("root %s", rt.Type)
		r.walk(rt.Type, crossNone, label)
		if rt.Inbound {
			r.walk(rt.Type, crossIn, label)
		}
		if rt.Outbound {
			r.walk(rt.Type, crossOut, label)
		}
	}
	if err := errors.Join(r.errs...); err != nil {
		return boundarySet{}, err
	}
	set := boundarySet{types: r.members(), open: map[reflect.Type]string{}}
	set.prog, err = r.load(root)
	if err != nil {
		return boundarySet{}, err
	}
	if set.enums, err = r.enums(set.prog, set.open); err != nil {
		return boundarySet{}, err
	}
	return set, errors.Join(r.checkNames(set), r.checkListed(set))
}

// crossing is the direction a reached type crosses in.
type crossing int

const (
	crossNone crossing = iota
	crossIn
	crossOut
)

// flip is the direction a callback's parameters cross in: opposite its own.
func (c crossing) flip() crossing {
	switch c {
	case crossIn:
		return crossOut
	case crossOut:
		return crossIn
	}
	return crossNone
}

// reach walks Go types from the roots, recording each struct and named
// string it meets and the root that first met it.
type reach struct {
	cfg     buzzgen.Config
	seen    map[crossing]map[reflect.Type]bool
	from    map[reflect.Type]string
	named   []reflect.Type
	errs    []error
}

func newReach(cfg buzzgen.Config) *reach {
	return &reach{
		cfg:  cfg,
		seen: map[crossing]map[reflect.Type]bool{crossNone: {}, crossIn: {}, crossOut: {}},
		from: map[reflect.Type]string{},
	}
}

// hostModules walks every method signature the modules declare.
func (r *reach) hostModules(mods []std.Module) {
	for _, mod := range mods {
		r.methods(mod.Methods, mod.Name)
		for _, n := range mod.Namespaces {
			r.methods(n.Methods, fmt.Sprintf("%s.%s", mod.Name, n.Name))
		}
		for _, o := range mod.Objects {
			r.methods(o.Methods, fmt.Sprintf("%s.%s", mod.Name, o.Name))
		}
		for _, p := range mod.Protocols {
			r.methods(p.Methods, fmt.Sprintf("%s.%s", mod.Name, p.Name))
		}
	}
}

// methods walks each signature as a value Go hands out: its parameters
// cross in and its results cross out.
func (r *reach) methods(ms []std.Method, owner string) {
	for _, m := range ms {
		if sig := m.Signature(); sig != nil {
			r.walk(sig, crossOut, fmt.Sprintf("%s.%s", owner, m.Name))
		}
	}
}

var (
	timeType     = reflect.TypeFor[time.Time]()
	durationType = reflect.TypeFor[time.Duration]()
	placeholder  = reflect.TypeFor[buzzgen.T]()
)

func (r *reach) walk(t reflect.Type, dir crossing, root string) {
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		r.walk(t.Elem(), dir, root)
		return
	case reflect.Map:
		r.walk(t.Key(), dir, root)
		r.walk(t.Elem(), dir, root)
		return
	case reflect.Func:
		for i := range t.NumIn() {
			r.walk(t.In(i), dir.flip(), root)
		}
		for i := range t.NumOut() {
			r.walk(t.Out(i), dir, root)
		}
		return
	}
	if t.PkgPath() == "" || t == timeType || t == durationType || t == placeholder {
		return
	}
	switch t.Kind() {
	case reflect.String:
		if _, ok := r.from[t]; !ok {
			r.from[t] = root
			r.named = append(r.named, t)
			r.admit(t, root)
		}
	case reflect.Struct:
		r.walkStruct(t, dir, root)
	}
}

func (r *reach) walkStruct(t reflect.Type, dir crossing, root string) {
	if r.seen[dir][t] {
		return
	}
	r.seen[dir][t] = true
	// A concrete generic instance mirrors as its generic object, so only
	// its arguments cross.
	instance := strings.Contains(t.Name(), "[") && len(buzzgen.TypeParams(t)) == 0
	mirrored := r.cfg.Ref(t.PkgPath(), goName(t)).MirrorName() != ""
	if !instance && !mirrored {
		if _, ok := r.from[t]; !ok {
			r.from[t] = root
			r.admit(t, root)
		}
	}
	if mirrored {
		return
	}
	for _, f := range emit.ObjectFields(t) {
		r.walk(f.Type, dir, root)
	}
}

// admit refuses a type generated code cannot name: an unexported one, or
// one from an internal package .buzzgen.yaml does not list.
func (r *reach) admit(t reflect.Type, root string) {
	name := goName(t)
	if name == "" || !isExported(name) {
		r.errs = append(r.errs, fmt.Errorf("%s reaches unexported type %s, which no generated code can name", root, t))
		return
	}
	if isInternal(t.PkgPath()) {
		if _, ok := r.cfg.Packages[t.PkgPath()]; !ok {
			r.errs = append(r.errs, fmt.Errorf("%s reaches %s from internal package %s: give the package a line in .buzzgen.yaml if it crosses, or stop the field reaching it", root, t, t.PkgPath()))
		}
	}
}

func isExported(name string) bool {
	return name[0] >= 'A' && name[0] <= 'Z'
}

func isInternal(pkgPath string) bool {
	return slices.Contains(strings.Split(pkgPath, "/"), "internal")
}

// goName is t's Go name without a generic's type arguments.
func goName(t reflect.Type) string {
	name, _, _ := strings.Cut(t.Name(), "[")
	return name
}

// members is every reached struct, ordered by key.
func (r *reach) members() []boundaryType {
	var out []boundaryType
	for t := range r.from {
		if t.Kind() != reflect.Struct {
			continue
		}
		ref := r.cfg.Ref(t.PkgPath(), goName(t))
		entry := boundaryType{
			Name:     ref.BuzzName(),
			Type:     t,
			Outbound: r.seen[crossOut][t],
			Inbound:  r.seen[crossIn][t],
			Required: ref.Settings.Required,
		}
		if ref.Settings.Module != nil {
			entry.Module = *ref.Settings.Module
		}
		if ref.Settings.TS != nil {
			entry.TS = *ref.Settings.TS
		}
		out = append(out, entry)
	}
	slices.SortFunc(out, func(a, b boundaryType) int {
		if c := strings.Compare(a.key(), b.key()); c != 0 {
			return c
		}
		return strings.Compare(a.Type.String(), b.Type.String())
	})
	return out
}

// checkNames refuses two crossing types that take one Buzz name in one
// scope, naming the root that reached each.
func (r *reach) checkNames(set boundarySet) error {
	owner := map[string]reflect.Type{}
	var errs []error
	claim := func(key string, t reflect.Type) {
		if t == nil {
			return
		}
		if prev, ok := owner[key]; ok && prev != t {
			errs = append(errs, fmt.Errorf("Buzz name %s is taken by %s (reached from %s) and %s (reached from %s): set name or module on one in .buzzgen.yaml",
				key, prev, r.from[prev], t, r.from[t]))
			return
		}
		owner[key] = t
	}
	for _, entry := range set.types {
		claim(entry.key(), entry.Type)
	}
	for _, e := range set.enums {
		claim(e.key(), e.Type)
	}
	return errors.Join(errs...)
}

// checkListed refuses a .buzzgen.yaml line for a type that does not cross,
// since its settings would apply to nothing.
func (r *reach) checkListed(set boundarySet) error {
	crossing := map[string]bool{}
	for t := range r.from {
		crossing[fmt.Sprintf("%s.%s", t.PkgPath(), goName(t))] = true
	}
	for _, e := range set.enums {
		crossing[e.goKey] = true
	}
	var errs []error
	for _, ref := range r.cfg.Types() {
		key := fmt.Sprintf("%s.%s", ref.Package, ref.Name)
		if ref.MirrorName() != "" {
			key = fmt.Sprintf("%s.%s", ref.Package, ref.MirrorName())
		}
		if !crossing[key] {
			errs = append(errs, fmt.Errorf(".buzzgen.yaml: packages.%s.types.%s names %s, which nothing crosses as: delete the line", ref.Package, ref.Name, key))
		}
	}
	return errors.Join(errs...)
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

// key is the enum's Buzz name, qualified as module\Name when Module scopes
// it, as boundaryType.key is.
func (e boundaryEnum) key() string {
	if e.Module == "" {
		return e.Name
	}
	return fmt.Sprintf(`%s\%s`, e.Module, e.Name)
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
func buzzName(rt reflect.Type) string {
	for _, entry := range boundaryTypes() {
		if entry.Type == rt {
			return entry.Name
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
	if entry, ok := boundaryTypeNamed(fmt.Sprintf(`%s\%s`, module, name)); ok {
		return entry, true
	}
	return boundaryTypeNamed(name)
}

// boundaryEnumNamed finds a declared enum by its Buzz name. The counterpart to
// boundaryTypeNamed, for the generator that has to declare an enum a signature
// references.
func boundaryEnumNamed(name string) (boundaryEnum, bool) {
	for _, e := range boundaryEnumList() {
		if e.Name == name {
			return e, true
		}
	}
	return boundaryEnum{}, false
}
