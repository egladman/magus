package buzzgen

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// nestedLeaf stands in for a type a host forgot to register. It has no encoder method.
type nestedLeaf struct {
	N int
}

// nestedHolder carries one, which is the shape that used to emit a call to a method
// nothing declares.
type nestedHolder struct {
	Name string
	Leaf nestedLeaf
}

// encodedLeaf stands in for a type that writes its own encoder by hand, as several of
// magus's do. It is registered nowhere and must still be accepted.
type encodedLeaf struct {
	N int
}

func (encodedLeaf) BuzzObject() map[string]any { return nil }

// encodedHolder carries one.
type encodedHolder struct {
	Leaf encodedLeaf
}

func runtimeOpts(known func(reflect.Type) bool) RuntimeOptions {
	return RuntimeOptions{
		Package:    "fixture",
		Method:     "BuzzObject",
		ObjectType: "Object",
		Known:      known,
	}
}

// TestRuntimeMethodsRejectsAnUnregisteredNestedType is the whole point of the check: the
// failure used to arrive as a compile error against a DO-NOT-EDIT file, naming a method
// the reader never wrote, about a type they never mentioned.
func TestRuntimeMethodsRejectsAnUnregisteredNestedType(t *testing.T) {
	_, err := RuntimeMethods(
		[]reflect.Type{reflect.TypeFor[nestedHolder]()},
		runtimeOpts(func(reflect.Type) bool { return false }),
	)

	var unknown *UnknownStructError
	if !errors.As(err, &unknown) {
		t.Fatalf("want an *UnknownStructError, got %v", err)
	}
	if unknown.Type != reflect.TypeFor[nestedLeaf]() {
		t.Errorf("error must name the offending FIELD's type, got %s", unknown.Type)
	}
	if !strings.Contains(unknown.Field, "Leaf") {
		t.Errorf("error must name the field path so a reader can find it, got %q", unknown.Field)
	}
	if unknown.Method != "BuzzObject" {
		t.Errorf("error must name the encoder that would have been called, got %q", unknown.Method)
	}
}

// TestRuntimeMethodsAcceptsAHandWrittenEncoder pins the other half. Several types write
// their encoder by hand and appear in no registry; rejecting those would make the check
// unusable on the tree it was written for.
func TestRuntimeMethodsAcceptsAHandWrittenEncoder(t *testing.T) {
	known := func(rt reflect.Type) bool {
		_, has := rt.MethodByName("BuzzObject")
		return has
	}
	out, err := RuntimeMethods([]reflect.Type{reflect.TypeFor[encodedHolder]()}, runtimeOpts(known))
	if err != nil {
		t.Fatalf("a type carrying its own encoder must be accepted: %v", err)
	}
	if !strings.Contains(string(out), "BuzzObject()") {
		t.Error("the emitted encoder should call the nested type's own method")
	}
}

// TestRuntimeMethodsWithoutKnownKeepsTheOldBehavior guards the opt-in: every caller
// predating the check passes no predicate and must be unaffected.
func TestRuntimeMethodsWithoutKnownKeepsTheOldBehavior(t *testing.T) {
	out, err := RuntimeMethods([]reflect.Type{reflect.TypeFor[nestedHolder]()}, runtimeOpts(nil))
	if err != nil {
		t.Fatalf("a nil Known must disable the check entirely: %v", err)
	}
	if !strings.Contains(string(out), "BuzzObject()") {
		t.Error("the unchecked path still emits the call, which is what it did before")
	}
}
