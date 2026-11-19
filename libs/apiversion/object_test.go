package apiversion

import (
	"encoding/json"
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

func (s *ObjectSuite) TestSpecHeaderNamesSpecType() {
	s.Equal(TypeMeta{APIVersion: "example.widget/v1alpha1", Kind: "Widget"}, SpecHeader[widgetSpec]())
}

func (s *ObjectSuite) TestNewStampsHeader() {
	object := New(Metadata{Name: "w"}, widgetSpec{Size: 3})
	s.Equal(APIVersion("example.widget/v1alpha1"), object.APIVersion)
	s.Equal(Kind("Widget"), object.Kind)
}

func (s *ObjectSuite) TestMarshalUsesSnakeCaseKeys() {
	data, err := json.Marshal(New(Metadata{Name: "w", Generation: 2}, widgetSpec{Size: 3}))
	s.Require().NoError(err)
	s.JSONEq(`{"api_version":"example.widget/v1alpha1","kind":"Widget",
		"metadata":{"name":"w","generation":2},"spec":{"size":3}}`, string(data))
}

func (s *ObjectSuite) TestARefusalOfAnotherKindIsNotNewer() {
	var err error = &RefusedError{Want: SpecHeader[widgetSpec](), Got: TypeMeta{APIVersion: "example.widget/v1alpha1", Kind: "Gadget"}}
	s.NotErrorIs(err, ErrNewer)
}

func (s *ObjectSuite) TestARefusalOfANewerVersionIsNewer() {
	var err error = &RefusedError{Want: SpecHeader[widgetSpec](), Got: TypeMeta{APIVersion: "example.widget/v1", Kind: "Widget"}}
	s.ErrorIs(err, ErrNewer)
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
	s.JSONEq(`{"api_version":"example.widget/v1alpha1","kind":"Widget","status":"ok"}`, string(data))
}

func (s *ObjectSuite) TestUnmetNilWhenAllKnown() {
	m := Metadata{Annotations: map[string]string{"example.widget/requires": "a"}}
	s.Nil(m.Unmet("example.widget", []Feature{"a"}))
}
