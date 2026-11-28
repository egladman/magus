package replay

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/suite"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

type CodecSuite struct {
	suite.Suite
	sess *buzz.Session
}

func TestCodec(t *testing.T) { suite.Run(t, new(CodecSuite)) }

func (s *CodecSuite) SetupTest() {
	s.sess = buzz.NewSession(context.Background(), buzz.WithEmbedded())
}

func (s *CodecSuite) eval(code string) vm.Value {
	v, err := s.sess.Eval(context.Background(), code)
	s.Require().NoError(err)
	return v
}

func (s *CodecSuite) roundTrip(v vm.Value) vm.Value {
	result, why := encode(v)
	s.Require().Empty(why)
	back, err := decodeWith(result, nil)
	s.Require().NoError(err)
	return back
}

func (s *CodecSuite) TestCodecRoundTrips() {
	m := vm.NewMap()
	m.MapSet("b", vm.FloatValue(3))
	m.MapSet("a", vm.ListValue([]vm.Value{vm.IntValue(1), vm.Null, vm.BoolValue(true), vm.StringValue("x\n\"y\"")}))
	back := s.roundTrip(m)
	s.Require().True(back.IsMap())
	s.Equal([]string{"b", "a"}, back.MapKeys(), "insertion order is kept")
	b, _ := back.MapGet("b")
	s.True(b.IsFloat(), "3.0 stays a float")
	s.InDelta(3.0, b.AsFloat(), 0)
	a, _ := back.MapGet("a")
	items := a.ListItems()
	s.Require().Len(items, 4)
	s.True(items[0].IsInt())
	s.Equal(int64(1), items[0].AsInt())
	s.True(items[1].IsNull())
	s.True(items[2].AsBool())
	s.Equal("x\n\"y\"", items[3].AsString())

	s.True(math.IsNaN(s.roundTrip(vm.FloatValue(math.NaN())).AsFloat()))
	s.True(math.IsInf(s.roundTrip(vm.FloatValue(math.Inf(-1))).AsFloat(), -1))
	s.Equal(int64(-(1 << 47)), s.roundTrip(vm.IntValue(-(1 << 47))).AsInt())
}

// std.JSONEncode writes 3.0 as 3, which JSONDecodeValue reads back as an int, so the
// codec tags floats itself instead of reusing it.
func (s *CodecSuite) TestStdJSONDoesNotKeepAFloatApart() {
	text, err := std.JSONEncode(vm.FloatValue(3))
	s.Require().NoError(err)
	back, err := std.JSONDecodeValue([]byte(text))
	s.Require().NoError(err)
	s.True(back.IsInt())
}

func (s *CodecSuite) TestAnInstanceEncodesAndDecodeNamesItsType() {
	inst := s.eval(`object Point { x: int = 0, y: double = 0.0 } return Point{ x = 2, y = 1.5 };`)
	s.Require().True(inst.IsObject())
	result, why := encode(inst)
	s.Require().Empty(why)
	s.JSONEq(`{"object":"Point","fields":[["x",2],["y",{"float":"1.5"}]]}`, result)
	_, err := decodeWith(result, nil)
	s.ErrorContains(err, "an instance of Point needs its object definition")
}

func (s *CodecSuite) TestDecodeWithRebuildsAnInstance() {
	def := s.eval(`object Point { x: int = 0, y: double = 0.0 } return Point;`)
	result, why := encode(s.eval(`object Point { x: int = 0, y: double = 0.0 } return Point{ x = 2, y = 1.5 };`))
	s.Require().Empty(why)

	back, err := decodeWith(result, func(name string) (vm.Value, bool) { return def, name == "Point" })
	s.Require().NoError(err)
	s.True(back.IsObject())
	s.Equal("Point", back.ObjectTypeName())
	x, _ := back.ObjectFieldAt(0)
	y, _ := back.ObjectFieldAt(1)
	s.Equal(int64(2), x.AsInt())
	s.InDelta(1.5, y.AsFloat(), 0)
}

func (s *CodecSuite) TestDecodeWithRefusesWhatItCannotRebuild() {
	result, why := encode(s.eval(`object Point { x: int = 0 } return Point{ x = 2 };`))
	s.Require().Empty(why)

	_, err := decodeWith(result, func(string) (vm.Value, bool) { return vm.Null, false })
	s.ErrorContains(err, "an instance of Point needs its object definition")

	renamed := s.eval(`object Point { z: int = 0 } return Point;`)
	_, err = decodeWith(result, func(string) (vm.Value, bool) { return renamed, true })
	s.ErrorContains(err, `Point was recorded with field "x" at 0 and declares "z"`)

	wider := s.eval(`object Point { x: int = 0, y: int = 0 } return Point;`)
	_, err = decodeWith(result, func(string) (vm.Value, bool) { return wider, true })
	s.ErrorContains(err, "Point was recorded with 1 fields and declares 2")
}

func (s *CodecSuite) TestAnInstanceWithANativePayloadIsUnencodable() {
	def := s.eval(`object Handle { id: int = 0 } return Handle;`)
	var key vm.NativePayloadKey
	inst, err := def.NewNativeInstance(&key, []vm.Value{vm.IntValue(1)}, vm.IntValue(9))
	s.Require().NoError(err)
	s.True(inst.HasNativePayload())

	_, why := encode(inst)
	s.Equal("Handle with a native payload at $", why)
}

func (s *CodecSuite) TestCodecNamesWhatItCannotHold() {
	direct := vm.DirectValue("io.write", func(context.Context, []vm.Value) (vm.Value, error) { return vm.Null, nil })
	_, why := encode(direct)
	s.Equal("direct at $", why)

	m := vm.NewMap()
	m.MapSet("cb", direct)
	_, why = encode(vm.ListValue([]vm.Value{vm.IntValue(1), m}))
	s.Equal(`direct at $[1]["cb"]`, why)

	_, why = encode(s.eval(`fun f() > int { return 1; } return f;`))
	s.Equal("fun at $", why)

	_, why = encode(vm.UDValue(0x10))
	s.Equal("ud at $", why)

	_, why = encode(s.eval(`return $"a+";`))
	s.Equal("pat at $", why)

	_, why = encode(s.eval(`fun g() > int { return 1; } return &g();`))
	s.Equal("fib at $", why)

	_, why = encode(s.eval(`object Holder { f: any = null } return Holder{ f = fun () > int => 1 };`))
	s.Equal("fun at $.f", why)

	self := s.eval(`final l: mut [any] = mut [1]; l.append(l); return l;`)
	_, why = encode(self)
	s.Equal("a cycle at $[1]", why)
}

func (s *CodecSuite) TestArgsDigestKeepsKindsApart() {
	s.NotEqual(argsDigest([]vm.Value{vm.IntValue(3)}), argsDigest([]vm.Value{vm.FloatValue(3)}))
	s.NotEqual(argsDigest([]vm.Value{vm.StringValue("a"), vm.StringValue("b")}), argsDigest([]vm.Value{vm.StringValue("ab")}))
	one := vm.DirectValue("a", func(context.Context, []vm.Value) (vm.Value, error) { return vm.Null, nil })
	two := vm.DirectValue("b", func(context.Context, []vm.Value) (vm.Value, error) { return vm.Null, nil })
	s.Equal(argsDigest([]vm.Value{one}), argsDigest([]vm.Value{two}), "an unencodable argument digests as its kind")
}
