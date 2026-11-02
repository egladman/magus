package main

import (
	"bytes"
	"errors"
	"testing"
	"text/template"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/cmdline/output"
)

type templateJSONSuite struct{ suite.Suite }

func TestTemplateJSON(t *testing.T) { suite.Run(t, new(templateJSONSuite)) }

func (s *templateJSONSuite) TestNestedRecordsUseJSONFieldNames() {
	value := struct {
		Nested struct {
			Paths []string `json:"paths"`
		} `json:"nested"`
	}{}
	value.Nested.Paths = []string{"a.go", "b.go"}
	var buf bytes.Buffer
	s.Require().NoError(renderTemplate(&buf, value, `{{json .nested}}`))
	s.JSONEq(`{"paths":["a.go","b.go"]}`, buf.String())
}

func (s *templateJSONSuite) TestNullIsJSONNull() {
	var buf bytes.Buffer
	s.Require().NoError(renderTemplate(&buf, map[string]any{"value": nil}, `{{json .value}}`))
	s.Equal("null", buf.String())
}

func (s *templateJSONSuite) TestStringsAreEscaped() {
	var buf bytes.Buffer
	s.Require().NoError(renderTemplate(&buf, map[string]string{"value": "line\n\"quoted\"\\path"}, `{{.value | json}}`))
	s.Equal(`"line\n\"quoted\"\\path"`, buf.String())
}

func (s *templateJSONSuite) TestExistingFunctionsRemainAvailable() {
	var buf bytes.Buffer
	s.Require().NoError(renderTemplate(&buf, map[string]any{"values": []string{"a", "b"}}, `{{upper "ok"}} {{json .values}}`))
	s.Equal(`OK ["a","b"]`, buf.String())
}

func (s *templateJSONSuite) TestConfiguredCodecErrorsArePreserved() {
	refused := errors.New("codec refused nested value")
	wr := output.Writer{Codec: output.Codec{
		Marshal: func(value any) ([]byte, error) {
			if _, shaped := value.(map[string]any); shaped {
				return nil, refused
			}
			return json.Marshal(value)
		},
		Unmarshal: json.Unmarshal,
	}}
	value := struct {
		Nested struct{} `json:"nested"`
	}{}
	var buf bytes.Buffer
	err := wr.Write(&buf, output.Options{Format: output.Template, Template: `{{json .nested}}`}, value)
	s.ErrorIs(err, refused)
	s.Empty(buf.String())
}

func (s *templateJSONSuite) TestCustomFunctionsArePreservedWithoutMutatingTheirMap() {
	funcs := template.FuncMap{"label": func() string { return "value" }}
	wr := output.Writer{Codec: output.Codec{Marshal: json.Marshal, Unmarshal: json.Unmarshal}, Funcs: funcs}
	var buf bytes.Buffer
	err := wr.Write(&buf, output.Options{Format: output.Template, Template: `{{label}}={{json .}}`}, []string{"a"})

	s.Require().NoError(err)
	s.Equal(`value=["a"]`, buf.String())
	s.NotContains(funcs, "json")
}
