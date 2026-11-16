package codec

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"
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
	s.Equal("codec: cannot encode: invalid UTF-8 in a string", err.Error())
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
	s.Equal(`duplicate key "a" at byte offset 7`, Unmarshal([]byte(`{"a":1,"a":2}`), &m).Error())
	s.ErrorIs(Unmarshal([]byte(`{"a":1,"a":2}`), &m), ErrDuplicateKey)

	var str string
	err := Unmarshal([]byte("\"a\xffb\""), &str)
	s.ErrorIs(err, ErrInvalidUTF8)
	s.Equal("invalid UTF-8 in a string at byte offset 2", err.Error())
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

func (s *ConformanceSuite) TestUnmarshalStrictRefusesAsStrictDecoderDoes() {
	type doc struct {
		A int `json:"a"`
	}
	for _, c := range []struct{ in, want string }{
		{in: `{"a":1,"a":2}`, want: `duplicate key "a" at byte offset 7`},
		{in: `{"a":1,"b":2}`, want: `unknown field "b" at byte offset 7`},
		{in: `{"a":1} {}`, want: `trailing data at byte offset 8`},
	} {
		var got doc
		var strict *StrictError
		s.Require().ErrorAs(UnmarshalStrict([]byte(c.in), &got), &strict, c.in)
		s.Equal(c.want, strict.Error(), c.in)
		s.Positive(strict.Offset, c.in)
	}
	var got doc
	s.Require().NoError(UnmarshalStrict([]byte(`{"a":1}`), &got))
	s.Equal(1, got.A)
	s.ErrorIs(UnmarshalStrict(nil, &got), io.ErrUnexpectedEOF)
	s.ErrorIs(UnmarshalStrict([]byte(`{"a":1,"b":2}`), &got), ErrUnknownField)
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
	s.Require().NoError(UnmarshalLossless([]byte(`{"n":12345678901234567890}`), &got))
	s.Equal(map[string]any{"n": json.Number("12345678901234567890")}, got)
	s.ErrorIs(UnmarshalLossless([]byte(`{"a":1,"a":2}`), &got), ErrDuplicateKey)
	s.ErrorIs(UnmarshalLossless([]byte(`{} {}`), &got), ErrTrailingData)
}

func (s *ConformanceSuite) TestValid() {
	s.True(Valid([]byte(` {"a":[1,2.5e3,true,null,"x"]} `)))
	s.False(Valid([]byte(`{"a":1,"a":1}`)))
	s.False(Valid([]byte(`{"a":1`)))
	s.False(Valid([]byte("\"\xff\"")))
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
