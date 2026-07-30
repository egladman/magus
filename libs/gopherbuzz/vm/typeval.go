package vm

// typeObj is a Buzz TYPE used as a value: what `<[str]>` denotes and what
// `typeof x` evaluates to. name is the canonical spelling of the type, and it is
// the whole payload - two type values are equal exactly when their canonical
// spellings match.
//
// A canonical spelling rather than a structural type graph is deliberate. Buzz's
// `typeof` is a STATIC operation: upstream compares the type DEFS the compiler
// resolved, so `final list = []` yields `[any]` while `final slist: [str] = []`
// yields `[str]` even though both are the same empty list at runtime. The VM
// therefore never inspects a value to answer `typeof`; the compiler hands it a
// constant the checker computed, and the VM only has to compare and print it.
// That makes the canonical string the entire contract, and it must be produced by
// exactly one function (canonicalTypeName in the checker) so the two sides of
// `typeof x == <T>` can never disagree on spacing.
type typeObj struct {
	name string
}

func (*typeObj) heapKind() valueTag { return tagType }

// TypeValue builds the Buzz type value whose canonical spelling is name.
func TypeValue(name string) Value { return heapValue(tagType, &typeObj{name: name}) }

// IsType reports whether v is a type value.
func (v Value) IsType() bool { return v.tag() == tagType }

// TypeName returns the canonical spelling of a type value, or "" for any other
// value.
func (v Value) TypeName() string {
	if v.tag() != tagType {
		return ""
	}
	return v.asType().name
}
