package ffi

import (
	"context"
	"fmt"
	"strings"
	"sync"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// EnumCases hands a generated encoder the case a session's own enum definition
// interned for a Go value, so a field the magus declarations type as an enum crosses
// as that case. A case compares by identity, so a str in its place equals no case a
// magusfile names and has no .value.
type EnumCases interface {
	// Case returns the case of enum whose value is value, and null for an empty
	// value. It panics on any other value outside the enum: a Go value the
	// declaration does not list is a bug on the Go side, and the VM reports the
	// panic as an internal error.
	Case(enum, value string) vm.Value
}

// SessionEnums resolves cases through sess from the enums the module imported as
// module declares; a boundary type's mirror is bundled into the module that returns
// it. It reads each enum the first time an encoder asks for it. Build one per
// session: the cases belong to that session's definitions. ctx bounds the reads,
// past the cancellation of the call that built it.
func SessionEnums(ctx context.Context, sess *buzz.Session, module string) EnumCases {
	return &sessionEnums{
		ctx:    context.WithoutCancel(ctx),
		sess:   sess,
		module: module,
		cases:  map[string]map[string]vm.Value{},
	}
}

// EnumBacking writes each enum field as its backing str. It is only for a value Go
// decodes back by value and no Buzz code compares, such as a spell handle magus.project
// re-reads; a value Buzz code reads takes SessionEnums.
var EnumBacking EnumCases = enumBacking{}

type enumBacking struct{}

func (enumBacking) Case(_, value string) vm.Value { return vm.StrValue(value) }

type sessionEnums struct {
	ctx    context.Context
	sess   *buzz.Session
	module string

	mu    sync.Mutex
	cases map[string]map[string]vm.Value
}

func (s *sessionEnums) Case(enum, value string) vm.Value {
	if value == "" {
		return vm.Null
	}
	s.mu.Lock()
	cases, ok := s.cases[enum]
	s.mu.Unlock()
	if !ok {
		var err error
		if cases, err = s.read(enum); err != nil {
			panic(fmt.Sprintf("reading %s: %v", s.qualify(enum), err))
		}
		s.mu.Lock()
		s.cases[enum] = cases
		s.mu.Unlock()
	}
	c, ok := cases[value]
	if !ok {
		panic(fmt.Sprintf("%q is not a case value of %s", value, s.qualify(enum)))
	}
	return c
}

// qualify spells enum as a call site does: Buzz binds a slash-path import under its
// last segment.
func (s *sessionEnums) qualify(enum string) string {
	return fmt.Sprintf(`%s\%s`, s.module[strings.LastIndex(s.module, "/")+1:], enum)
}

// read evaluates the enum's cases in the session, since only the VM that holds a
// definition can intern its cases.
func (s *sessionEnums) read(enum string) (map[string]vm.Value, error) {
	def, err := s.value("def", enum, s.qualify(enum))
	if err != nil {
		return nil, err
	}
	_, names, ok := def.EnumDef()
	if !ok {
		return nil, fmt.Errorf("it is a %s, not an enum", def.Kind())
	}
	refs := make([]string, len(names))
	for i, name := range names {
		refs[i] = fmt.Sprintf("%s.%s", s.qualify(enum), name)
	}
	list, err := s.value("cases", enum, fmt.Sprintf("[%s]", strings.Join(refs, ", ")))
	if err != nil {
		return nil, err
	}
	out := make(map[string]vm.Value, len(names))
	for _, c := range list.ListItems() {
		backing, _ := c.EnumValue()
		if !backing.IsStr() {
			return nil, fmt.Errorf("case %s is not str-backed", describeCase(c))
		}
		out[backing.AsString()] = c
	}
	return out, nil
}

// value evaluates expr after importing the module and returns what it is. It goes
// through a function because a strict session, the kind `magus buzz` runs a script in,
// rejects a top-level return. Redeclaring the function for a second reader of the same
// session replaces the first, which is what the same expression asks for.
func (s *sessionEnums) value(part, enum, expr string) (vm.Value, error) {
	name := fmt.Sprintf("__enum_%s_%s", part, enum)
	decl := fmt.Sprintf("import %q; fun %s() > any { return %s; }", s.module, name, expr)
	if _, err := s.sess.Eval(s.ctx, decl); err != nil {
		return vm.Null, err
	}
	return s.sess.CallValue(s.ctx, s.sess.GetGlobal(name), nil)
}

func describeCase(v vm.Value) string {
	enum, name, _ := v.EnumCase()
	return fmt.Sprintf("%s.%s", enum, name)
}
