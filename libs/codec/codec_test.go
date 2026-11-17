package codec

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/suite"
)

// ConformanceSuite runs unchanged under both builds (the magusfile's test targets run it
// with and without GOEXPERIMENT=jsonv2), so each exact byte and error string it asserts
// holds for both. The few cases a build is meant to answer differently say so and assert
// each build's answer.
type ConformanceSuite struct{ suite.Suite }

func TestConformance(t *testing.T) { suite.Run(t, new(ConformanceSuite)) }

type pair struct {
	K string `json:"k"`
	V int    `json:"v"`
}

func (s *ConformanceSuite) marshal(v any) string {
	b, err := Marshal(v)
	s.Require().NoError(err)
	return string(b)
}

func (s *ConformanceSuite) TestVersionNamesTheBuild() {
	s.Contains([]string{"v1", "v2"}, Version())
}

func (s *ConformanceSuite) TestMarshalSortsMapKeys() {
	s.Equal(`{"a":2,"b":1}`, s.marshal(map[string]int{"b": 1, "a": 2}))
}

func (s *ConformanceSuite) TestMarshalKeepsNilCollectionsNull() {
	s.Equal(`{"items":null,"attrs":null}`, s.marshal(struct {
		Items []string       `json:"items"`
		Attrs map[string]int `json:"attrs"`
	}{}))
}

// The byte-for-byte contract for everything the codec writes: a release index is signed
// over these bytes, so how the binary was built must not change them.
func (s *ConformanceSuite) TestMarshalWritesHTMLAndLineSeparatorsAsTyped() {
	s.Equal("{\"label\":\"query <ref> & more\u2028end\"}",
		s.marshal(map[string]string{"label": "query <ref> & more\u2028end"}))
	s.Equal(`"tab\tquote\"nul\u0000"`, s.marshal("tab\tquote\"nul\x00"))
}

// A RawMessage is written in the codec's form whatever its author spelled.
func (s *ConformanceSuite) TestMarshalNormalizesARawMessage() {
	s.Equal("{\"raw\":{\"a\":\"<\u2028\",\"n\":1.50}}",
		s.marshal(map[string]RawMessage{"raw": RawMessage(`{ "a" : "\u003c\u2028", "n": 1.50 }`)}))
}

func (s *ConformanceSuite) TestMarshalRefusesInvalidUTF8() {
	_, err := Marshal(map[string]string{"bad": "a\xffb"})
	s.Require().ErrorIs(err, ErrInvalidUTF8)
	var encode *EncodeError
	s.Require().ErrorAs(err, &encode)
	s.Equal("codec: cannot encode: writing a string: invalid UTF-8", err.Error())
}

func (s *ConformanceSuite) TestMarshalIndent() {
	b, err := MarshalIndent(map[string]any{"b": []int{1}, "a": map[string]int{}}, "", "  ")
	s.Require().NoError(err)
	s.Equal("{\n  \"a\": {},\n  \"b\": [\n    1\n  ]\n}", string(b))

	_, err = MarshalIndent(1, "", "x")
	s.Error(err, "an indent holds only spaces and tabs")
}

func (s *ConformanceSuite) TestRoundTrips() {
	in := pair{K: "hello", V: 42}
	var out pair
	s.Require().NoError(Unmarshal([]byte(s.marshal(in)), &out))
	s.Equal(in, out)
}

func (s *ConformanceSuite) TestDurationIsItsStringForm() {
	type ttl struct {
		TTL Duration `json:"ttl"`
	}
	s.Equal(`{"ttl":"6h0m0s"}`, s.marshal(ttl{Duration(6 * time.Hour)}))
	var got ttl
	s.Require().NoError(Unmarshal([]byte(`{"ttl":"1m30s"}`), &got))
	s.Equal(Duration(90*time.Second), got.TTL)
	s.Error(Unmarshal([]byte(`{"ttl":5}`), &got), "a duration is a string")
}

// A bare time.Duration has no form encoding/json shares with v2: the v2 build writes its
// String form and the encoding/json build refuses the type.
func (s *ConformanceSuite) TestBareDurationIsAStringOrARefusal() {
	type ttl struct {
		TTL time.Duration `json:"ttl"`
	}
	b, err := Marshal(ttl{6 * time.Hour})
	if Version() == "v2" {
		s.Require().NoError(err)
		s.Equal(`{"ttl":"6h0m0s"}`, string(b))
		var got ttl
		s.Require().NoError(Unmarshal(b, &got))
		s.Equal(6*time.Hour, got.TTL)
		return
	}
	var unsupported *UnsupportedError
	s.Require().ErrorAs(err, &unsupported)
	s.Equal("TTL", unsupported.Field)
	s.Require().ErrorAs(Unmarshal([]byte(`{"ttl":"6h0m0s"}`), &ttl{}), &unsupported)
}

// omitempty means another thing to each library on a bool, a number or a struct, so the
// v2 build keeps the zero and the encoding/json build refuses the type; omitzero means
// the same to both.
func (s *ConformanceSuite) TestOmitEmptyOnABoolIsKeptOrRefused() {
	type flag struct {
		On bool `json:"on,omitempty"`
	}
	b, err := Marshal(flag{})
	if Version() == "v2" {
		s.Require().NoError(err)
		s.Equal(`{"on":false}`, string(b))
	} else {
		var unsupported *UnsupportedError
		s.Require().ErrorAs(err, &unsupported)
		s.Equal("On", unsupported.Field)
	}

	type zero struct {
		On    bool      `json:"on,omitzero"`
		Since time.Time `json:"since,omitzero"`
		Ptr   *string   `json:"ptr,omitzero"`
	}
	s.Equal(`{}`, s.marshal(zero{}))
}

// An interface hides its dynamic type from the walk over static types, so the check reads
// the value too.
func (s *ConformanceSuite) TestADurationBehindAnInterfaceIsAStringOrARefusal() {
	b, err := Marshal(map[string]any{"ttl": time.Second})
	if Version() == "v2" {
		s.Require().NoError(err)
		s.Equal(`{"ttl":"1s"}`, string(b))
		return
	}
	var unsupported *UnsupportedError
	s.ErrorAs(err, &unsupported)
}

func (s *ConformanceSuite) TestUnmarshalRefusesWhatIsAmbiguous() {
	var m map[string]int
	s.Equal(`duplicate key: "a" at byte offset 7`, Unmarshal([]byte(`{"a":1,"a":2}`), &m).Error())
	s.ErrorIs(Unmarshal([]byte(`{"a":1,"a":2}`), &m), ErrDuplicateKey)

	var str string
	err := Unmarshal([]byte("\"a\xffb\""), &str)
	s.ErrorIs(err, ErrInvalidUTF8)
	s.Equal("reading a string: invalid UTF-8 at byte offset 2", err.Error())
	s.ErrorIs(Unmarshal([]byte(`"\ud800"`), &str), ErrInvalidUTF8, "a lone surrogate")

	err = Unmarshal([]byte(`{} x`), &m)
	s.ErrorIs(err, ErrTrailingData)
	s.Equal("trailing data at byte offset 3", err.Error())

	err = Unmarshal(nil, &m)
	s.ErrorIs(err, io.ErrUnexpectedEOF)
	var syntax *SyntaxError
	s.Require().ErrorAs(err, &syntax)
	s.Equal(int64(0), syntax.Offset)

	s.Equal("invalid character 'x' looking for the start of a value at byte offset 5",
		Unmarshal([]byte(`{"a":x}`), &m).Error())
}

// Both builds answer null as v2 does: the target's zero value.
func (s *ConformanceSuite) TestNullZeroesAValue() {
	got := struct {
		N int    `json:"n"`
		S string `json:"s"`
	}{N: 5, S: "kept?"}
	s.Require().NoError(Unmarshal([]byte(`{"n":null,"s":null}`), &got))
	s.Zero(got.N)
	s.Zero(got.S)
}

// A member that matches a field only when case is ignored: v2 skips it and encoding/json
// would fill the field, so that build refuses it.
func (s *ConformanceSuite) TestACaseOnlyMatchIsSkippedOrRefused() {
	var got struct {
		Name string `json:"name"`
	}
	err := Unmarshal([]byte(`{"Name":"x"}`), &got)
	if Version() == "v2" {
		s.Require().NoError(err)
		s.Empty(got.Name)
		return
	}
	s.ErrorIs(err, ErrCaseOnlyMatch)
	s.Equal(`member "Name" matches a field only when case is ignored "name" at byte offset 1`, err.Error())
}

type strictDoc struct {
	A int `json:"a"`
}

func (s *ConformanceSuite) strictRefusal(in string) *StrictError {
	var got strictDoc
	var strict *StrictError
	s.Require().ErrorAs(UnmarshalStrict([]byte(in), &got), &strict)
	return strict
}

func (s *ConformanceSuite) TestUnmarshalStrictRefusesARepeatedKey() {
	strict := s.strictRefusal(`{"a":1,"a":2}`)
	s.Equal(`duplicate key: "a" at byte offset 7`, strict.Error())
	s.Equal(int64(7), strict.Offset)
	s.ErrorIs(strict, ErrDuplicateKey)
}

func (s *ConformanceSuite) TestUnmarshalStrictRefusesAnUnknownField() {
	strict := s.strictRefusal(`{"a":1,"b":2}`)
	s.Equal(`unknown field "b" at byte offset 7`, strict.Error())
	s.Equal(int64(7), strict.Offset)
	s.ErrorIs(strict, ErrUnknownField)
}

func (s *ConformanceSuite) TestUnmarshalStrictRefusesTrailingData() {
	strict := s.strictRefusal(`{"a":1} {}`)
	s.Equal(`trailing data at byte offset 8`, strict.Error())
	s.Equal(int64(8), strict.Offset)
	s.ErrorIs(strict, ErrTrailingData)
}

func (s *ConformanceSuite) TestUnmarshalStrictReadsADeclaredDocument() {
	var got strictDoc
	s.Require().NoError(UnmarshalStrict([]byte(`{"a":1}`), &got))
	s.Equal(1, got.A)
	s.ErrorIs(UnmarshalStrict(nil, &got), io.ErrUnexpectedEOF)
}

func (s *ConformanceSuite) TestStrictDecoderReadsTokenByToken() {
	d := NewStrictDecoder([]byte(`{"first": {"a": 1}, "second": {"a": 2, "z": 3}}`))
	s.Require().NoError(d.Delim('{'))
	var names []string
	var failed *StrictError
	for d.More() {
		name, err := d.Name()
		s.Require().NoError(err)
		names = append(names, name)
		var v struct {
			A int `json:"a"`
		}
		if err := d.Decode(&v); err != nil {
			s.Require().ErrorAs(err, &failed)
			break
		}
	}
	s.Equal([]string{"first", "second"}, names)
	s.Require().NotNil(failed)
	s.Equal(int64(39), failed.Offset, "the offset names the undeclared member")
}

func (s *ConformanceSuite) TestUnmarshalLosslessKeepsNumbersAndRefusesDuplicates() {
	var got any
	s.Require().NoError(UnmarshalLossless([]byte(`{"n":12345678901234567890,"l":[1.50]}`), &got))
	s.Equal(map[string]any{"n": json.Number("12345678901234567890"), "l": []any{json.Number("1.50")}}, got)
	s.ErrorIs(UnmarshalLossless([]byte(`{"a":1,"a":2}`), &got), ErrDuplicateKey)
	s.ErrorIs(UnmarshalLossless([]byte(`{} {}`), &got), ErrTrailingData)
}

// A struct target reads as Unmarshal reads it: names matched exactly and null zeroing a
// value, with only an interface field's number kept as its text.
func (s *ConformanceSuite) TestUnmarshalLosslessReadsAStructAsUnmarshalDoes() {
	got := struct {
		Any   any    `json:"any"`
		Count int    `json:"count"`
		Name  string `json:"name"`
	}{Name: "kept?"}
	s.Require().NoError(UnmarshalLossless([]byte(`{"any":{"n":9007199254740993},"count":3,"name":null}`), &got))
	s.Equal(map[string]any{"n": json.Number("9007199254740993")}, got.Any)
	s.Equal(3, got.Count)
	s.Empty(got.Name)

	err := UnmarshalLossless([]byte(`{"Count":4}`), &got)
	if Version() == "v2" {
		s.Require().NoError(err)
		s.Equal(3, got.Count, "a case-only name is skipped")
		return
	}
	s.ErrorIs(err, ErrCaseOnlyMatch)
}

func (s *ConformanceSuite) TestValid() {
	s.True(Valid([]byte(` {"a":[1,2.5e3,true,null,"x"]} `)))
	s.False(Valid([]byte(`{"a":1,"a":1}`)))
	s.False(Valid([]byte(`{"a":1`)))
	s.False(Valid([]byte("\"\xff\"")))
}

// A U+FFFD a RawMessage spells as an escape is a character, not the mark of an invalid
// byte, so both builds write it; an invalid byte in a string is refused wherever it sits.
func (s *ConformanceSuite) TestAnEscapedReplacementCharacterIsWritten() {
	s.Equal("{\"raw\":\"a\ufffdb\"}", s.marshal(map[string]RawMessage{"raw": RawMessage(`"a\ufffdb"`)}))
	s.Equal("\"\ufffd\"", s.marshal("\ufffd"))

	var got string
	s.Require().NoError(Unmarshal([]byte(`"\ufffd"`), &got))
	s.Equal("\ufffd", got)
}

type badText struct{}

func (badText) MarshalText() ([]byte, error) { return []byte("a\xffb"), nil }

func (s *ConformanceSuite) refusesInvalidUTF8(v any) {
	_, err := Marshal(v)
	s.Require().ErrorIs(err, ErrInvalidUTF8)
	var encode *EncodeError
	s.Require().ErrorAs(err, &encode)
	s.Equal("codec: cannot encode: writing a string: invalid UTF-8", err.Error())
}

func (s *ConformanceSuite) TestMarshalRefusesInvalidUTF8InAMapKey() {
	s.refusesInvalidUTF8(map[string]int{"a\xffb": 1})
}

func (s *ConformanceSuite) TestMarshalRefusesInvalidUTF8FromMarshalText() {
	s.refusesInvalidUTF8(struct{ T badText }{})
}

func (s *ConformanceSuite) TestMarshalRefusesInvalidUTF8InARawMessage() {
	s.refusesInvalidUTF8(map[string]RawMessage{"raw": RawMessage("\"a\xffb\"")})
}

// An escaped U+FFFD elsewhere in the output does not hide an invalid byte.
func (s *ConformanceSuite) TestMarshalRefusesInvalidUTF8BesideAnEscapedReplacement() {
	s.refusesInvalidUTF8(map[string]any{"ok": RawMessage(`"\ufffd"`), "bad": "a\xffb"})
}

// A value its Go target cannot hold is the codec's TypeError in both builds, with the
// same words.
func (s *ConformanceSuite) TestAValueOfTheWrongTypeIsATypeError() {
	var got pair
	err := Unmarshal([]byte(`{"k":"a","v":"x"}`), &got)
	var typed *TypeError
	s.Require().ErrorAs(err, &typed)
	s.Equal("string", typed.JSON)
	s.Equal(reflect.TypeFor[int](), typed.GoType)
	s.Equal("cannot decode a JSON string into Go int", err.Error())
	s.Require().Error(typed.Err, "the library's own error stays reachable")

	var small struct {
		N int8 `json:"n"`
	}
	err = Unmarshal([]byte(`{"n":300}`), &small)
	s.Require().ErrorAs(err, &typed)
	s.Equal("cannot decode a JSON number into Go int8", err.Error())
}

// An array of another length than its Go array's is refused in both builds, as v2 refuses
// it, and reads the same in each.
func (s *ConformanceSuite) TestAnArrayOfTheWrongLengthIsATypeError() {
	var got struct {
		P [2]int `json:"p"`
	}
	err := Unmarshal([]byte(`{"p":[1,2,3]}`), &got)
	var typed *TypeError
	s.Require().ErrorAs(err, &typed)
	s.Equal("cannot decode a JSON array into Go [2]int: an array of 3 elements", err.Error())
	var syntax *SyntaxError
	s.NotErrorAs(err, &syntax)

	err = UnmarshalStrict([]byte(`{"p":[1]}`), &got)
	var strict *StrictError
	s.Require().ErrorAs(err, &strict)
	s.Require().ErrorAs(err, &typed)
	s.Equal("cannot decode a JSON array into Go [2]int: an array of 1 element", err.Error())
	s.Equal(int64(0), strict.Offset, "the offset is where the value being read starts")
}

// The stream's faults are the codec's in both builds, their offsets counted from the
// start of the stream.
func (s *ConformanceSuite) TestDecoderRefusesInTheCodecsWords() {
	dec := NewDecoder(strings.NewReader(`{"a":1} {"a":1,"a":2}`))
	var m map[string]int
	s.Require().NoError(dec.Decode(&m))
	err := dec.Decode(&m)
	s.Require().ErrorIs(err, ErrDuplicateKey)
	var syntax *SyntaxError
	s.Require().ErrorAs(err, &syntax)
	s.Equal(int64(15), syntax.Offset)
	s.Equal(err, dec.Decode(&m), "a malformed stream stays malformed")

	dec = NewDecoder(strings.NewReader(`{}x`))
	s.Require().NoError(dec.Decode(&m))
	err = dec.Decode(&m)
	s.Require().ErrorIs(err, ErrTrailingData)
	s.Equal("trailing data at byte offset 2", err.Error())

	err = NewDecoder(strings.NewReader(` {"a":x}`)).Decode(&m)
	s.Require().ErrorAs(err, &syntax)
	s.Equal("invalid character 'x' looking for the start of a value at byte offset 6", err.Error())

	err = NewDecoder(strings.NewReader(`{"a":`)).Decode(&m)
	s.Require().ErrorIs(err, io.ErrUnexpectedEOF)
	s.Require().ErrorAs(err, &syntax)

	var p pair
	err = NewDecoder(strings.NewReader(`{"v":"x"}`)).Decode(&p)
	var typed *TypeError
	s.Require().ErrorAs(err, &typed)
	s.Equal("cannot decode a JSON string into Go int", err.Error())
}

// A reader handing over one byte at a time still yields whole values, a number at the very
// end included.
func (s *ConformanceSuite) TestDecoderReadsAcrossShortReads() {
	dec := NewDecoder(iotest.OneByteReader(strings.NewReader(`{"k":"a","v":1}` + "\n" + `12`)))
	var p pair
	s.Require().NoError(dec.Decode(&p))
	s.Equal(pair{K: "a", V: 1}, p)
	var n int
	s.Require().NoError(dec.Decode(&n))
	s.Equal(12, n)
	s.ErrorIs(dec.Decode(&n), io.EOF)
}

func (s *ConformanceSuite) TestEncoderAndDecoderStream() {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	s.Require().NoError(enc.Encode(pair{K: "a<b", V: 1}))
	s.Require().NoError(enc.Encode(pair{K: "c", V: 2}))
	s.Equal(`{"k":"a<b","v":1}`+"\n"+`{"k":"c","v":2}`+"\n", buf.String())

	dec := NewDecoder(&buf)
	var got []pair
	for {
		var p pair
		err := dec.Decode(&p)
		if errors.Is(err, io.EOF) {
			break
		}
		s.Require().NoError(err)
		got = append(got, p)
	}
	s.Equal([]pair{{K: "a<b", V: 1}, {K: "c", V: 2}}, got)
}

// A type with its own MarshalJSON and UnmarshalJSON passes both builds, which is how a
// type that needs `,unknown` or `,inline` gets past the encoding/json build.
type bag struct {
	Name  string
	Extra map[string]RawMessage
}

func (b bag) MarshalJSON() ([]byte, error) {
	m := map[string]any{"name": b.Name}
	for k, v := range b.Extra {
		m[k] = v
	}
	return Marshal(m)
}

func (b *bag) UnmarshalJSON(data []byte) error {
	var m map[string]RawMessage
	if err := Unmarshal(data, &m); err != nil {
		return err
	}
	if err := Unmarshal(m["name"], &b.Name); err != nil {
		return err
	}
	delete(m, "name")
	b.Extra = m
	return nil
}

func (s *ConformanceSuite) TestATypeWithItsOwnMethodsPassesBothBuilds() {
	in := bag{Name: "x", Extra: map[string]RawMessage{"z": RawMessage(`[1]`)}}
	b := s.marshal(in)
	s.Equal(`{"name":"x","z":[1]}`, b)
	var out bag
	s.Require().NoError(Unmarshal([]byte(b), &out))
	s.Equal(in, out)
}

// keeper keeps the members it does not declare the way a record envelope does: a bag
// field tagged `codec:"unknown"` and methods built on the two helpers.
type keeper struct {
	keeperEnvelope
	Name string `json:"name"`
	Pair pair   `json:"pair,omitzero"`
}

type keeperEnvelope struct {
	Version string                `json:"version"`
	Unknown map[string]RawMessage `json:"-" codec:"unknown"`
}

func (k keeper) MarshalJSON() ([]byte, error) {
	type plain keeper
	return MarshalWithUnknown(plain(k), k.Unknown)
}

func (k *keeper) UnmarshalJSON(data []byte) error {
	type plain keeper
	var p plain
	unknown, err := UnmarshalWithUnknown(data, &p)
	if err != nil {
		return err
	}
	*k = keeper(p)
	k.Unknown = unknown
	return nil
}

func (s *ConformanceSuite) TestUnknownMembersRoundTripThroughBothLibraries() {
	in := `{"version":"v1","name":"x","later":{"b":[1,2]},"added":true}`
	var got keeper
	s.Require().NoError(Unmarshal([]byte(in), &got))
	s.Equal("x", got.Name)
	s.Equal(map[string]RawMessage{"added": RawMessage(`true`), "later": RawMessage(`{"b":[1,2]}`)}, got.Unknown)
	s.Equal(`{"version":"v1","name":"x","added":true,"later":{"b":[1,2]}}`, s.marshal(got))

	var plain keeper
	s.Require().NoError(json.Unmarshal([]byte(in), &plain), "a caller on encoding/json alone reads the bag")
	s.Equal(got, plain)
	out, err := json.Marshal(plain)
	s.Require().NoError(err)
	s.JSONEq(in, string(out), "and writes it back")
}

// A member whose name matches a declared field only when case is ignored is one the type
// does not declare: both builds keep it in the bag and leave the field to its own member.
func (s *ConformanceSuite) TestAKeeperKeepsACaseOnlyMatch() {
	in := `{"version":"v1","name":"x","Name":1}`
	var got keeper
	s.Require().NoError(Unmarshal([]byte(in), &got))
	s.Equal("x", got.Name)
	s.Equal(map[string]RawMessage{"Name": RawMessage(`1`)}, got.Unknown)
	s.Equal(`{"version":"v1","name":"x","Name":1}`, s.marshal(got))
}

func (s *ConformanceSuite) TestUnmarshalStrictRefusesWhatAKeeperWouldKeep() {
	var got keeper
	var strict *StrictError
	s.Require().ErrorAs(UnmarshalStrict([]byte(`{"version":"v1","later":1}`), &got), &strict)
	s.ErrorIs(strict, ErrUnknownField)
	s.Equal(`unknown field "later" at byte offset 16`, strict.Error())
	s.Require().ErrorIs(UnmarshalStrict([]byte(`{"version":"v1","pair":{"k":"a","z":1}}`), &got), ErrUnknownField,
		"the walk refuses a nested member too")
	s.Require().NoError(UnmarshalStrict([]byte(`{"version":"v1","name":"x"}`), &got))
	s.Equal("x", got.Name)
	s.Nil(got.Unknown)
}

func (s *ConformanceSuite) TestMarshalWithUnknownRefusesADeclaredName() {
	_, err := Marshal(keeper{Name: "x", keeperEnvelope: keeperEnvelope{Unknown: map[string]RawMessage{"name": RawMessage(`"y"`)}}})
	s.ErrorIs(err, ErrDuplicateKey)
}
