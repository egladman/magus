package apiversion

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"
)

type widgetSpec struct {
	Size int `json:"size"`
}

func (widgetSpec) Kind() Kind             { return "Widget" }
func (widgetSpec) APIVersion() APIVersion { return "example.widget/v1alpha1" }

type ObjectSuite struct{ suite.Suite }

func TestObjectSuite(t *testing.T) { suite.Run(t, new(ObjectSuite)) }

func strictUnmarshal(data []byte, v any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(v)
}

func (s *ObjectSuite) TestSpecHeaderNamesSpecType() {
	s.Equal(TypeMeta{APIVersion: "example.widget/v1alpha1", Kind: "Widget"}, SpecHeader[widgetSpec]())
}

func (s *ObjectSuite) TestNewStampsHeader() {
	object := New(Metadata{Name: "w"}, widgetSpec{Size: 3})
	s.Equal(APIVersion("example.widget/v1alpha1"), object.APIVersion)
	s.Equal(Kind("Widget"), object.Kind)
}

func (s *ObjectSuite) TestMarshalUsesKubernetesKeys() {
	data, err := json.Marshal(New(Metadata{Name: "w", Generation: 2}, widgetSpec{Size: 3}))
	s.Require().NoError(err)
	s.JSONEq(`{"apiVersion":"example.widget/v1alpha1","kind":"Widget",
		"metadata":{"name":"w","generation":2},"spec":{"size":3}}`, string(data))
}

func (s *ObjectSuite) TestReadRoundTrips() {
	want := New(Metadata{Name: "w"}, widgetSpec{Size: 3})
	data, err := json.Marshal(want)
	s.Require().NoError(err)
	got, err := Read[widgetSpec](data, json.Unmarshal)
	s.Require().NoError(err)
	s.Equal(want, got)
}

func (s *ObjectSuite) TestReadHeaderIgnoresBody() {
	header, err := ReadHeader([]byte(`{"apiVersion":"example.widget/v1","kind":"Other","spec":{"x":1}}`), json.Unmarshal)
	s.Require().NoError(err)
	s.Equal(TypeMeta{APIVersion: "example.widget/v1", Kind: "Other"}, header)
}

func (s *ObjectSuite) TestReadHeaderWrapsDecodeError() {
	_, err := ReadHeader([]byte(`{`), json.Unmarshal)
	s.Error(err)
}

func (s *ObjectSuite) TestReadRefusesOtherKind() {
	_, err := Read[widgetSpec]([]byte(`{"apiVersion":"example.widget/v1alpha1","kind":"Gadget"}`), json.Unmarshal)
	var refused *RefusedError
	s.Require().True(errors.As(err, &refused))
	s.Equal(Kind("Gadget"), refused.Got.Kind)
	s.Equal(SpecHeader[widgetSpec](), refused.Want)
	s.NotErrorIs(err, ErrNewer)
}

func (s *ObjectSuite) TestReadRefusesNewerVersionAsNewerSentinel() {
	_, err := Read[widgetSpec]([]byte(`{"apiVersion":"example.widget/v1","kind":"Widget"}`), json.Unmarshal)
	s.ErrorIs(err, ErrNewer)
}

func (s *ObjectSuite) TestReadRefusesMissingHeader() {
	_, err := Read[widgetSpec]([]byte(`{"spec":{"size":1}}`), json.Unmarshal)
	var refused *RefusedError
	s.Require().True(errors.As(err, &refused))
	s.Empty(refused.Got.APIVersion)
}

func (s *ObjectSuite) TestReadHeaderLenientBodyStrict() {
	data := []byte(`{"apiVersion":"example.widget/v1alpha1","kind":"Widget","spec":{"size":1,"extra":2}}`)
	_, err := Read[widgetSpec](data, json.Unmarshal, WithBodyUnmarshal(strictUnmarshal))
	s.Error(err)
}

func (s *ObjectSuite) TestReadWithoutBodyOptionIsLenient() {
	data := []byte(`{"apiVersion":"example.widget/v1alpha1","kind":"Widget","spec":{"size":1,"extra":2}}`)
	object, err := Read[widgetSpec](data, json.Unmarshal)
	s.Require().NoError(err)
	s.Equal(1, object.Spec.Size)
}

func (s *ObjectSuite) TestRequiresReadsAnnotation() {
	m := Metadata{Annotations: map[string]string{"example.widget/requires": "a, b,,c"}}
	s.Equal([]Feature{"a", "b", "c"}, m.Requires("example.widget"))
}

func (s *ObjectSuite) TestRequiresIgnoresOtherGroups() {
	m := Metadata{Annotations: map[string]string{"example.gadget/requires": "a"}}
	s.Nil(m.Requires("example.widget"))
}

func (s *ObjectSuite) TestUnmetListsUnknownFeaturesInOrder() {
	m := Metadata{Annotations: map[string]string{"example.widget/requires": "a,b,c"}}
	s.Equal([]Feature{"b", "c"}, m.Unmet("example.widget", []Feature{"a"}))
}

func (s *ObjectSuite) TestWithRequiresWritesAnnotation() {
	m := Metadata{Annotations: map[string]string{"other": "x"}}.WithRequires("example.widget", []Feature{"a", "b"})
	s.Equal(map[string]string{"other": "x", "example.widget/requires": "a,b"}, m.Annotations)
}

func (s *ObjectSuite) TestWithRequiresEmptyDropsAnnotation() {
	m := Metadata{Annotations: map[string]string{"example.widget/requires": "a"}}.WithRequires("example.widget", nil)
	s.Nil(m.Annotations)
}

func (s *ObjectSuite) TestWithRequiresLeavesReceiverMap() {
	before := map[string]string{"example.widget/requires": "a"}
	Metadata{Annotations: before}.WithRequires("example.widget", []Feature{"b"})
	s.Equal(map[string]string{"example.widget/requires": "a"}, before)
}

func (s *ObjectSuite) TestEmbeddedTypeMetaStaysTopLevel() {
	type record struct {
		TypeMeta
		Status string `json:"status"`
	}
	data, err := json.Marshal(record{TypeMeta: TypeMeta{APIVersion: "example.widget/v1alpha1", Kind: "Widget"}, Status: "ok"})
	s.Require().NoError(err)
	s.JSONEq(`{"apiVersion":"example.widget/v1alpha1","kind":"Widget","status":"ok"}`, string(data))
}

func (s *ObjectSuite) TestUnmetNilWhenAllKnown() {
	m := Metadata{Annotations: map[string]string{"example.widget/requires": "a"}}
	s.Nil(m.Unmet("example.widget", []Feature{"a"}))
}
