package vm

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func TestValueConstructorsAndPredicates(t *testing.T) {
	noop := func(_ context.Context, _ []Value) (Value, error) { return NullValue(), nil }

	t.Run("IntValue", func(t *testing.T) {
		v := IntValue(42)
		assert.True(t, v.IsInt())
		assert.Equal(t, "int", v.Kind())
	})
	t.Run("FloatValue", func(t *testing.T) {
		v := FloatValue(3.14)
		assert.True(t, v.IsFloat())
		assert.Equal(t, "double", v.Kind())
	})
	t.Run("BoolValue true", func(t *testing.T) {
		v := BoolValue(true)
		assert.True(t, v.IsBool())
		assert.Equal(t, "bool", v.Kind())
	})
	t.Run("BoolValue false", func(t *testing.T) {
		v := BoolValue(false)
		assert.True(t, v.IsBool())
		assert.Equal(t, "bool", v.Kind())
	})
	t.Run("StrValue", func(t *testing.T) {
		v := StringValue("hello")
		assert.True(t, v.IsString())
		assert.Equal(t, "str", v.Kind())
	})
	t.Run("ListValue", func(t *testing.T) {
		v := ListValue(nil)
		assert.True(t, v.IsList())
		assert.Equal(t, "list", v.Kind())
	})
	t.Run("NewMap", func(t *testing.T) {
		v := NewMap()
		assert.True(t, v.IsMap())
		assert.Equal(t, "map", v.Kind())
	})
	t.Run("NullValue", func(t *testing.T) {
		v := NullValue()
		assert.True(t, v.IsNull())
		assert.Equal(t, "null", v.Kind())
	})
	t.Run("DirectValue", func(t *testing.T) {
		v := DirectValue("myfn", noop)
		assert.True(t, v.IsDirect())
		assert.Equal(t, "direct", v.Kind())
	})
}

func TestValueAsInt(t *testing.T) {
	assert.Equal(t, int64(99), IntValue(99).AsInt())
}

func TestValueAsIntNegative(t *testing.T) {
	assert.Equal(t, int64(-7), IntValue(-7).AsInt())
}

func TestValueAsFloat(t *testing.T) {
	assert.Equal(t, 2.718, FloatValue(2.718).AsFloat())
}

func TestValueAsBool(t *testing.T) {
	assert.True(t, BoolValue(true).AsBool())
	assert.False(t, BoolValue(false).AsBool())
}

func TestValueAsString(t *testing.T) {
	assert.Equal(t, "world", StringValue("world").AsString())
}

func TestValueListItems(t *testing.T) {
	items := []Value{IntValue(1), IntValue(2), IntValue(3)}
	got := ListValue(items).ListItems()
	require.Len(t, got, 3)
	for i, item := range got {
		assert.Equalf(t, int64(i+1), item.AsInt(), "ListItems()[%d].AsInt()", i)
	}
}

func TestValueListItemsNil(t *testing.T) {
	got := ListValue(nil).ListItems()
	assert.Nil(t, got, "ListItems() on nil-backed list")
}

func TestValueString(t *testing.T) {
	assert.Equal(t, "null", NullValue().String())
	assert.Equal(t, "true", BoolValue(true).String())
	assert.Equal(t, "false", BoolValue(false).String())
	assert.Equal(t, "42", IntValue(42).String())
	assert.Equal(t, "hi", StringValue("hi").String())
	assert.Equal(t, "[1, 2]", ListValue([]Value{IntValue(1), IntValue(2)}).String())
}

// TestValueStringCircular is the regression for String recursing into a
// genuine reference cycle: Buzz lists are heap objects mutable in place
// (list.append), so `[any] l = mut []; l.append(l);` makes l[0] == l. String
// backs str()/print/string interpolation (upstream-visible), so it must render
// a placeholder for the revisited list instead of stack-overflowing (a FATAL,
// unrecoverable Go error) or erroring. Run on a goroutine and bounded with
// select + time.After (the repo's hang/crash-test idiom; see pool_test.go
// TestDispatchRejectsCycle) since a regression here is unsafe to call
// inline.
func TestValueStringCircular(t *testing.T) {
	items := make([]Value, 1)
	l := ListValue(items)
	items[0] = l // l[0] == l: the same cycle list.append(l) would create

	done := make(chan string, 1)
	go func() { done <- l.String() }()

	select {
	case s := <-done:
		assert.Equal(t, "[[...]]", s, "a cyclic list should render a placeholder for the revisit, not recurse")
	case <-time.After(5 * time.Second):
		t.Fatal("Value.String did not return within bound on a cyclic list")
	}
}

func TestValueRawEqual(t *testing.T) {
	// This is a scalar-only spec: RawEqual compares raw tag+num bits, so heap
	// values (str, list, map, ...) are not covered here: under buzz_safe and
	// buzz_unsafe their num is 0 and any two same-tag heap values compare equal.
	// Use Equal (see TestValueEqual) for heap and language-level equality.
	// Scalars with same tag and payload must be equal.
	assert.True(t, IntValue(5).RawEqual(IntValue(5)))
	assert.False(t, IntValue(5).RawEqual(IntValue(6)))
	assert.True(t, NullValue().RawEqual(NullValue()))
	assert.True(t, BoolValue(true).RawEqual(BoolValue(true)))
	assert.False(t, BoolValue(true).RawEqual(BoolValue(false)))
	// Different types are not raw-equal even for the same numeric payload.
	assert.False(t, IntValue(0).RawEqual(NullValue()))
}

// TestValueEqual pins down Buzz `==` semantics as exposed by Value.Equal. This
// source runs under every value representation (nanbox, buzz_safe, buzz_unsafe)
// and must agree in all three: RawEqual would diverge here for the heap cases.
func TestValueEqual(t *testing.T) {
	// String content equality, including a string built at runtime (not a
	// compile-time literal) versus a literal of the same content.
	built := string([]byte{'b'})
	assert.True(t, StringValue(built).Equal(StringValue("b")))
	assert.True(t, StringValue("hello").Equal(StringValue("hello")))
	assert.False(t, StringValue("a").Equal(StringValue("b")))

	// int/float numeric coercion, matching the == operator.
	assert.True(t, IntValue(1).Equal(FloatValue(1.0)))
	assert.True(t, FloatValue(2.0).Equal(IntValue(2)))
	assert.False(t, IntValue(1).Equal(FloatValue(1.5)))
	assert.False(t, IntValue(1).Equal(IntValue(2)))

	// null and bool scalars.
	assert.True(t, NullValue().Equal(NullValue()))
	assert.True(t, BoolValue(true).Equal(BoolValue(true)))
	assert.False(t, BoolValue(true).Equal(BoolValue(false)))
	assert.False(t, NullValue().Equal(IntValue(0)))

	// Lists compare by reference identity: two distinct values with equal
	// content are NOT equal, but a value is equal to itself.
	l1 := ListValue([]Value{IntValue(1)})
	l2 := ListValue([]Value{IntValue(1)})
	assert.False(t, l1.Equal(l2), "distinct content-equal lists must not be Equal")
	assert.True(t, l1.Equal(l1), "a list value must be Equal to itself")

	// Maps compare by reference identity too.
	m1 := NewMap()
	m1.MapSet("a", IntValue(1))
	m2 := NewMap()
	m2.MapSet("a", IntValue(1))
	assert.False(t, m1.Equal(m2), "distinct content-equal maps must not be Equal")
	assert.True(t, m1.Equal(m1), "a map value must be Equal to itself")
}

// TestValueEqualFunctionIdentity pins reference identity for function values.
// These cases are load-bearing for magus.needs, which recovers which exported
// target a passed function value refers to by matching it against the exports
// it handed out.
func TestValueEqualFunctionIdentity(t *testing.T) {
	noop := func(_ context.Context, _ []Value) (Value, error) { return NullValue(), nil }

	// A function value equals itself, so host code can match a callable it
	// handed out earlier against one a script passes back.
	fnA := DirectValue("a", noop)
	assert.True(t, fnA.Equal(fnA), "a function value equals itself")

	// Two distinct function values are never equal, even with the same name and
	// underlying Go func: identity, not structure, for heap kinds.
	fnB := DirectValue("a", noop)
	assert.False(t, fnA.Equal(fnB), "distinct function values are not equal")
}

func TestValueEnumCases(t *testing.T) {
	def := EnumDefValue("Tier", []string{"low", "high"}, []Value{StringValue("lo"), StringValue("hi")})
	cases, ok := def.EnumCases()
	require.True(t, ok)
	require.Len(t, cases, 2)
	_, name, _ := cases[1].EnumCase()
	assert.Equal(t, "high", name)
	backing, _ := cases[1].EnumValue()
	assert.Equal(t, "hi", backing.AsString())
	assert.Equal(t, NewVM(context.Background()).enumCase(def, 1), cases[1], "the case Buzz code evaluates")

	cases[0] = NullValue()
	again, _ := def.EnumCases()
	assert.Equal(t, NewVM(context.Background()).enumCase(def, 0), again[0], "the returned cases are a copy")

	for _, v := range []Value{cases[1], NewMap(), StringValue("Tier"), NullValue()} {
		got, ok := v.EnumCases()
		assert.False(t, ok, "%s is not an enum definition", v.Kind())
		assert.Nil(t, got)
	}
}

func TestValueEnumDef(t *testing.T) {
	def := EnumDefValue("Tier", []string{"low", "high"}, nil)
	name, cases, ok := def.EnumDef()
	require.True(t, ok)
	assert.Equal(t, "Tier", name)
	assert.Equal(t, []string{"low", "high"}, cases, "cases come back in declaration order")

	cases[0] = "edited"
	_, again, _ := def.EnumDef()
	assert.Equal(t, "low", again[0], "the returned cases are a copy, not the definition's own")

	high := NewVM(context.Background()).enumCase(def, 1)
	for _, v := range []Value{high, NewMap(), StringValue("Tier"), NullValue()} {
		name, cases, ok := v.EnumDef()
		assert.False(t, ok, "%s is not an enum definition", v.Kind())
		assert.Empty(t, name)
		assert.Nil(t, cases)
	}
}

func TestValueEnumCase(t *testing.T) {
	vm := NewVM(context.Background())
	tier := EnumDefValue("Tier", []string{"low", "high"}, nil)
	enum, name, ok := vm.enumCase(tier, 1).EnumCase()
	require.True(t, ok)
	assert.Equal(t, "Tier", enum)
	assert.Equal(t, "high", name)

	// An enum<str> case may assign a value other than its name: EnumCase still
	// names the case, and EnumValue reads the assigned value.
	mode := EnumDefValue("Mode", []string{"fast"}, []Value{StringValue("f")})
	fast := vm.enumCase(mode, 0)
	_, name, ok = fast.EnumCase()
	require.True(t, ok)
	assert.Equal(t, "fast", name)
	backing, _ := fast.EnumValue()
	assert.Equal(t, "f", backing.AsString())

	for _, v := range []Value{tier, IntValue(1), StringValue("high"), NullValue()} {
		enum, name, ok := v.EnumCase()
		assert.False(t, ok, "%s is not an enum case", v.Kind())
		assert.Empty(t, enum)
		assert.Empty(t, name)
	}
}

// MapKeys reduces the int 1 and the str "1" to the same display string;
// MapKeyValues keeps each key, an enum case included.
func TestValueMapKeyValues(t *testing.T) {
	tier := EnumDefValue("Tier", []string{"low", "high"}, nil)
	high := NewVM(context.Background()).enumCase(tier, 1)
	m := NewMap()
	m.asMap().setVal(high, StringValue("by case"))
	m.asMap().setVal(IntValue(1), StringValue("by int"))
	m.asMap().setVal(StringValue("1"), StringValue("by str"))

	keys := m.MapKeyValues()
	require.Len(t, keys, 3)
	require.Len(t, m.MapValues(), len(keys), "keys and values are parallel")

	enum, name, ok := keys[0].EnumCase()
	require.True(t, ok)
	assert.Equal(t, "Tier", enum)
	assert.Equal(t, "high", name)
	assert.Equal(t, "by case", m.MapValues()[0].AsString())

	assert.True(t, keys[1].IsInt())
	assert.Equal(t, int64(1), keys[1].AsInt())
	assert.True(t, keys[2].IsString())
	assert.Equal(t, "1", keys[2].AsString())
	assert.Equal(t, m.MapKeys()[1], m.MapKeys()[2], "MapKeys is lossy where MapKeyValues is not")

	for _, v := range []Value{StringValue("m"), ListValue(nil), NullValue(), high} {
		assert.Nil(t, v.MapKeyValues(), "%s is not a map", v.Kind())
	}
}

// strBufSuite pins appendString: a long string extended at its end shares
// one buffer, and every string over that buffer keeps its own text.
type strBufSuite struct {
	suite.Suite
	long string // exactly strBufThreshold bytes, so it buffers on first append
}

func TestStrBufSuite(t *testing.T) { suite.Run(t, new(strBufSuite)) }

func (s *strBufSuite) SetupTest() { s.long = strings.Repeat("a", strBufThreshold) }

// extend appends text to left through appendString.
func extend(left Value, text string) Value { return appendString(left, []byte(text)) }

func (s *strBufSuite) TestAppendToTheNewestPrefixSharesTheBuffer() {
	first := extend(StringValue(s.long), "b")
	second := extend(first, "c")
	s.Require().NotNil(first.asString().buf)
	s.Same(first.asString().buf, second.asString().buf)
	s.Equal(s.long+"bc", second.AsString())
}

func (s *strBufSuite) TestAppendToAnOlderPrefixCopies() {
	prefix := extend(StringValue(s.long), "b")
	newest := extend(prefix, "c")
	other := extend(prefix, "d")
	s.NotSame(prefix.asString().buf, other.asString().buf)
	s.Equal(s.long+"bc", newest.AsString())
	s.Equal(s.long+"bd", other.AsString())
}

func (s *strBufSuite) TestEarlierPrefixKeepsItsText() {
	v := StringValue(s.long)
	var kept []Value
	for range 2000 {
		v = extend(v, "0123456789")
		kept = append(kept, v)
	}
	for i, k := range kept {
		s.Require().Equal(s.long+strings.Repeat("0123456789", i+1), k.AsString(), "prefix %d", i)
	}
}

func (s *strBufSuite) TestEmptyAppendReturnsTheLeftOperand() {
	for _, left := range []Value{StringValue("ab"), extend(StringValue(s.long), "b")} {
		got := appendString(left, nil)
		s.True(sameObj(left, got), "%q", left.AsString())
	}
}

func (s *strBufSuite) TestShortLeftOperandIsInterned() {
	got := extend(StringValue("ab"), "c")
	s.Nil(got.asString().buf)
	s.Same(internString("abc"), got.asString())
}

func (s *strBufSuite) TestBufferedAndInternedStringsOfEqualTextAreEqual() {
	buffered := extend(StringValue(s.long), "b")
	interned := StringValue(s.long + "b")
	s.True(valuesEqual(buffered, interned))
	s.True(valuesEqual(interned, buffered))
	s.True(mapKeyEqual(buffered, interned))
	s.False(valuesEqual(buffered, StringValue(s.long+"c")))
}

func (s *strBufSuite) TestBufferedStringIsAMapKeyByContent() {
	m := newMapObj()
	m.setVal(extend(StringValue(s.long), "b"), IntValue(1))
	got, ok := m.getVal(StringValue(s.long + "b"))
	s.Require().True(ok)
	s.Equal(int64(1), got.AsInt())

	m.setVal(StringValue(s.long+"b"), IntValue(2))
	s.Len(m.Keys, 1, "an interned key of the same text replaces the entry")
}

func (s *strBufSuite) TestTwoGoroutinesExtendingOnePrefixBothKeepTheirText() {
	prefix := extend(StringValue(s.long), "b")
	got := make([]Value, 2)
	var wg sync.WaitGroup
	for g, tail := range []string{"x", "y"} {
		wg.Go(func() {
			v := prefix
			for range 500 {
				v = extend(v, tail)
			}
			got[g] = v
		})
	}
	wg.Wait()
	s.Equal(s.long+"b"+strings.Repeat("x", 500), got[0].AsString())
	s.Equal(s.long+"b"+strings.Repeat("y", 500), got[1].AsString())
	s.Equal(s.long+"b", prefix.AsString())
}

func (s *strBufSuite) TestInterpolationStartingWithAStringAppends() {
	prefix := extend(StringValue(s.long), "b")
	c := &Chunk{}
	c.Emit(OpLoadConst, c.AddConst(prefix), 0)
	c.Emit(OpLoadConst, c.AddConst(IntValue(7)), 0)
	c.Emit(OpBuildStr, 2, 0)
	c.Emit(OpReturn, 0, 0)
	got, err := NewVM(context.Background()).Run(c, NewEnv())
	s.Require().NoError(err)
	s.Equal(s.long+"b7", got.AsString())
	s.Same(prefix.asString().buf, got.asString().buf)
}

// TestBufferStringDoesNotCopy pins what Go does not document: String hands
// out the builder's own bytes, which is what makes sharing a buffer cheap.
func (s *strBufSuite) TestBufferStringDoesNotCopy() {
	var b strings.Builder
	b.Grow(16)
	b.WriteString("abc")
	var sink string
	s.Zero(testing.AllocsPerRun(100, func() { sink = b.String() }))
	b.WriteString("d")
	s.Equal(unsafe.StringData(sink), unsafe.StringData(b.String()))
}

// heapCountersSuite pins the HeapStats counters every build reports.
type heapCountersSuite struct{ suite.Suite }

func TestHeapCountersSuite(t *testing.T) { suite.Run(t, new(heapCountersSuite)) }

// unique is text no other test interns.
func (s *heapCountersSuite) unique() string {
	return s.T().Name() + time.Now().Format(time.RFC3339Nano)
}

func (s *heapCountersSuite) TestInternedStringsCountEachNewTextOnce() {
	text := s.unique()
	before := ReadHeapStats()
	StringValue(text)
	StringValue(text)
	after := ReadHeapStats()
	s.Equal(1, after.InternedStrings-before.InternedStrings)
	s.Equal(len(text), after.InternedBytes-before.InternedBytes)
}

func (s *heapCountersSuite) TestBufferedStringIsNotInterned() {
	long := StringValue(s.unique() + strings.Repeat("a", strBufThreshold))
	before := ReadHeapStats()
	appendString(long, []byte("b"))
	s.Equal(before.InternedStrings, ReadHeapStats().InternedStrings)
}

func (s *heapCountersSuite) TestBufferedStringsCountEachBufferedString() {
	long := StringValue(s.unique() + strings.Repeat("a", strBufThreshold))
	before := ReadHeapStats().BufferedStrings
	appendString(appendString(long, []byte("b")), []byte("c"))
	s.Equal(2, ReadHeapStats().BufferedStrings-before)
}

func (s *heapCountersSuite) TestShortStringIsNotBuffered() {
	before := ReadHeapStats()
	appendString(StringValue(s.unique()), []byte("b"))
	after := ReadHeapStats()
	s.Equal(before.BufferedStrings, after.BufferedStrings)
	s.Equal(before.BufferedBytes, after.BufferedBytes)
}

func (s *heapCountersSuite) TestBufferedBytesCountsANewBuffersArray() {
	long := StringValue(s.unique() + strings.Repeat("a", strBufThreshold))
	before := ReadHeapStats().BufferedBytes
	v := appendString(long, []byte("b"))
	s.Equal(v.asString().buf.b.Cap(), ReadHeapStats().BufferedBytes-before)
}

func (s *heapCountersSuite) TestBufferedBytesCountsEachOutgrownArray() {
	v := appendString(StringValue(s.unique()+strings.Repeat("a", strBufThreshold)), []byte("b"))
	buf := v.asString().buf
	before := ReadHeapStats().BufferedBytes
	want, grows := 0, 0
	for range 64 {
		old := buf.b.Cap()
		v = appendString(v, []byte(strings.Repeat("c", 512)))
		if buf.b.Cap() != old {
			want += buf.b.Cap()
			grows++
		}
	}
	s.Positive(grows)
	s.Equal(want, ReadHeapStats().BufferedBytes-before)
}

func (s *heapCountersSuite) TestUnownedObjectsFollowsTheTable() {
	before := ReadHeapStats().UnownedObjects
	ListValue(nil)
	want := 1
	if HeapProfile() == nil {
		want = 0 // no table: the Go collector reclaims every object
	}
	s.Equal(want, ReadHeapStats().UnownedObjects-before)
}
