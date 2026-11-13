package buzzgen

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
)

// Direction is which way a reached type crosses into or out of Buzz.
type Direction int

const (
	// Mirrored crosses in neither direction: a script builds it.
	Mirrored Direction = iota
	// Inbound is a value Buzz hands the host, which needs a decoder.
	Inbound
	// Outbound is a value the host hands Buzz, which needs an encoder.
	Outbound
)

// flip is the direction a callback's parameters cross in: opposite its own.
func (d Direction) flip() Direction {
	switch d {
	case Inbound:
		return Outbound
	case Outbound:
		return Inbound
	}
	return Mirrored
}

// Reach walks Go types from a host's roots and records every struct and
// named string they reach, the directions each crosses in, and the root
// that first reached it. Membership in the boundary is what a Reach finds;
// the config only adds settings.
//
// A Reach is not safe for concurrent use.
type Reach struct {
	cfg   Config
	seen  map[Direction]map[reflect.Type]bool
	from  map[reflect.Type]string
	named []reflect.Type
	errs  []error
}

// NewReach starts an empty reach over cfg's settings.
func NewReach(cfg Config) *Reach {
	return &Reach{
		cfg:  cfg,
		seen: map[Direction]map[reflect.Type]bool{Mirrored: {}, Inbound: {}, Outbound: {}},
		from: map[reflect.Type]string{},
	}
}

var (
	timeType     = reflect.TypeFor[time.Time]()
	durationType = reflect.TypeFor[time.Duration]()
	placeholder  = reflect.TypeFor[T]()
)

// Walk records t and everything it reaches as crossing in dir, naming root
// as what reached it. A func type's results cross in dir and its
// parameters the other way. So a host method's signature walked Outbound
// decodes its parameters and encodes its results.
func (r *Reach) Walk(t reflect.Type, dir Direction, root string) {
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		r.Walk(t.Elem(), dir, root)
		return
	case reflect.Map:
		r.Walk(t.Key(), dir, root)
		r.Walk(t.Elem(), dir, root)
		return
	case reflect.Func:
		for i := range t.NumIn() {
			r.Walk(t.In(i), dir.flip(), root)
		}
		for i := range t.NumOut() {
			r.Walk(t.Out(i), dir, root)
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

func (r *Reach) walkStruct(t reflect.Type, dir Direction, root string) {
	if r.seen[dir][t] {
		return
	}
	r.seen[dir][t] = true
	// A concrete generic instance mirrors as its generic object, so only
	// its arguments cross.
	instance := strings.Contains(t.Name(), "[") && len(TypeParams(t)) == 0
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
	for _, f := range mirroredFields(t) {
		r.Walk(f.Type, dir, root)
	}
}

// mirroredFields are the fields renderFields declares: exported, not
// `buzz:"-"`, with an untagged embedded struct's fields inline.
func mirroredFields(t reflect.Type) []reflect.StructField {
	var out []reflect.StructField
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() || f.Tag.Get("buzz") == "-" {
			continue
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct && f.Tag.Get("buzz") == "" {
			out = append(out, mirroredFields(f.Type)...)
			continue
		}
		out = append(out, f)
	}
	return out
}

// admit refuses a type generated code cannot name: an unexported one, or
// one from an internal package the config does not list.
func (r *Reach) admit(t reflect.Type, root string) {
	name := goName(t)
	if name == "" || name[0] < 'A' || name[0] > 'Z' {
		r.errs = append(r.errs, fmt.Errorf("%s reaches unexported type %s, which no generated code can name", root, t))
		return
	}
	if !slices.Contains(strings.Split(t.PkgPath(), "/"), "internal") {
		return
	}
	if _, ok := r.cfg.Packages[t.PkgPath()]; !ok {
		r.errs = append(r.errs, fmt.Errorf("%s reaches %s from internal package %s: give the package a line in the config if it crosses, or stop the field reaching it", root, t, t.PkgPath()))
	}
}

// goName is t's Go name without a generic's type arguments.
func goName(t reflect.Type) string {
	name, _, _ := strings.Cut(t.Name(), "[")
	return name
}

// Err is every type the walks refused, joined.
func (r *Reach) Err() error { return errors.Join(r.errs...) }

// From is the root that first reached t, or "".
func (r *Reach) From(t reflect.Type) string { return r.from[t] }

// Named is every named string type reached, in the order first reached.
func (r *Reach) Named() []reflect.Type { return slices.Clone(r.named) }

// Packages is the import path of every reached type, sorted.
func (r *Reach) Packages() []string {
	var pkgs []string
	for t := range r.from {
		if !slices.Contains(pkgs, t.PkgPath()) {
			pkgs = append(pkgs, t.PkgPath())
		}
	}
	slices.Sort(pkgs)
	return pkgs
}

// Member is one reached struct with its layered settings and directions.
type Member struct {
	Type     reflect.Type
	Ref      TypeRef
	Inbound  bool
	Outbound bool
}

// Key is the member's Buzz name, qualified by its namespace when a module
// setting scopes it: http\Options, magus\agent\Step.
func (m Member) Key() string {
	if ns := m.Ref.Namespace(); ns != "" {
		return fmt.Sprintf(`%s\%s`, ns, m.Ref.BuzzName())
	}
	return m.Ref.BuzzName()
}

// Members is every reached struct, ordered by Key.
func (r *Reach) Members() []Member {
	var out []Member
	for t := range r.from {
		if t.Kind() != reflect.Struct {
			continue
		}
		out = append(out, Member{
			Type:     t,
			Ref:      r.cfg.Ref(t.PkgPath(), goName(t)),
			Inbound:  r.seen[Inbound][t],
			Outbound: r.seen[Outbound][t],
		})
	}
	slices.SortFunc(out, func(a, b Member) int {
		if c := strings.Compare(a.Key(), b.Key()); c != 0 {
			return c
		}
		return strings.Compare(a.Type.String(), b.Type.String())
	})
	return out
}

// Claim is one Buzz name a crossing type takes in one scope.
type Claim struct {
	Key  string
	Type reflect.Type
}

// CheckNames refuses two types that claim one key, naming the root that
// reached each. A claim with no Type is skipped.
func (r *Reach) CheckNames(claims []Claim) error {
	owner := map[string]reflect.Type{}
	var errs []error
	for _, c := range claims {
		if c.Type == nil {
			continue
		}
		prev, ok := owner[c.Key]
		if ok && prev != c.Type {
			errs = append(errs, fmt.Errorf("two crossing types take Buzz name %s, %s (reached from %s) and %s (reached from %s): set name or module on one in the config",
				c.Key, prev, r.from[prev], c.Type, r.from[c.Type]))
			continue
		}
		owner[c.Key] = c.Type
	}
	return errors.Join(errs...)
}

// CheckNamespaces refuses two packages that derive one module, and a nested
// module whose last segment members says its parent module already declares.
// members lists a module's functions and member groups by import path.
func (r *Reach) CheckNamespaces(members func(module string) []string) error {
	derivedBy := map[string]string{}
	modules := map[string]reflect.Type{}
	var errs []error
	for _, t := range sortedTypes(r.from) {
		pkg := t.PkgPath()
		module := r.cfg.Ref(pkg, goName(t)).Settings.Module
		if module == nil || *module == "" {
			continue
		}
		if _, seen := modules[*module]; !seen {
			modules[*module] = t
		}
		derived, ok := r.cfg.DerivedModule(pkg)
		if !ok || r.cfg.Packages[pkg].Config.Module != nil {
			continue
		}
		if prev, taken := derivedBy[derived]; taken && prev != pkg {
			errs = append(errs, fmt.Errorf("packages %s and %s both derive module %s: set packages.%s.config.module", prev, pkg, derived, pkg))
			continue
		}
		derivedBy[derived] = pkg
	}
	for _, module := range sortedKeys(modules) {
		parent, last, nested := cutLast(module, "/")
		if nested && slices.Contains(members(parent), last) {
			errs = append(errs, fmt.Errorf("namespace %s (reached from %s) is also the member %s of module %s: rename one, or set module on the package",
				strings.ReplaceAll(module, "/", `\`), r.from[modules[module]], last, parent))
		}
	}
	return errors.Join(errs...)
}

func sortedTypes(m map[reflect.Type]string) []reflect.Type {
	out := make([]reflect.Type, 0, len(m))
	for t := range m {
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b reflect.Type) int {
		return strings.Compare(a.PkgPath()+"."+a.Name(), b.PkgPath()+"."+b.Name())
	})
	return out
}

// CheckListed refuses a config line for a type that does not cross, since
// its settings would apply to nothing. crossing adds package.Name keys the
// reach did not meet but that cross anyway, such as a module-scoped enum.
func (r *Reach) CheckListed(crossing ...string) error {
	crosses := map[string]bool{}
	for _, key := range crossing {
		crosses[key] = true
	}
	for t := range r.from {
		crosses[fmt.Sprintf("%s.%s", t.PkgPath(), goName(t))] = true
	}
	var errs []error
	for _, ref := range r.cfg.Types() {
		key := fmt.Sprintf("%s.%s", ref.Package, ref.Name)
		if ref.MirrorName() != "" {
			key = fmt.Sprintf("%s.%s", ref.Package, ref.MirrorName())
		}
		if !crosses[key] {
			errs = append(errs, fmt.Errorf("packages.%s.types.%s names %s, which nothing crosses as: delete the line", ref.Package, ref.Name, key))
		}
	}
	return errors.Join(errs...)
}
