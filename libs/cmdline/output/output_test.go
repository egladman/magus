package output

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

var writer = Writer{Codec: Codec{Marshal: json.Marshal, Unmarshal: json.Unmarshal}}

type ref struct {
	Path string `json:"path"`
	Dir  string `json:"-"`
}

type listing struct {
	Projects []ref    `json:"projects" jsonl:"primary"`
	Tags     []string `json:"tags"`
}

func render(t *testing.T, o Options, v any) string {
	t.Helper()
	var buf bytes.Buffer
	if err := writer.Write(&buf, o, v); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestParse(t *testing.T) {
	cases := map[string]Options{
		"":                {Format: Text},
		"json":            {Format: JSON},
		"name":            {Format: Name},
		"template":        {Format: Template},
		"template=":       {Format: Template},
		"template={{.a}}": {Format: Template, Template: "{{.a}}"},
		"dot":             {Format: "dot"},
	}
	for in, want := range cases {
		got, err := Parse(in, "dot")
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	if _, err := Parse("dot"); err == nil || !strings.Contains(err.Error(), "text, json, yaml, jsonl, name") {
		t.Errorf("Parse without the extra: %v, want an error naming the choices", err)
	}
}

func TestWriteJSONIndentsTwoAndEndsInANewline(t *testing.T) {
	got := render(t, Options{Format: JSON}, ref{Path: "web", Dir: "/abs"})
	if want := "{\n  \"path\": \"web\"\n}\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteJSONLStreamsThePrimaryCollection(t *testing.T) {
	got := render(t, Options{Format: JSONL}, listing{Projects: []ref{{Path: "a"}, {Path: "b"}}, Tags: []string{"x"}})
	if want := "{\"path\":\"a\"}\n{\"path\":\"b\"}\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteJSONLRefusesAnUndeclaredChoice(t *testing.T) {
	type two struct {
		A []int `json:"a"`
		B []int `json:"b"`
	}
	err := writer.Write(&bytes.Buffer{}, Options{Format: JSONL}, two{})
	if err == nil || !strings.Contains(err.Error(), "none is marked") {
		t.Errorf("got %v, want the ambiguity named", err)
	}
}

func TestWriteTemplateReadsTheJSONNames(t *testing.T) {
	if got := render(t, Options{Format: Template, Template: "{{.path}}"}, ref{Path: "web"}); got != "web" {
		t.Errorf("got %q", got)
	}
	err := writer.Write(&bytes.Buffer{}, Options{Format: Template, Template: "{{.Path}}"}, ref{Path: "web"})
	if err == nil || !strings.Contains(err.Error(), "top-level fields are path") {
		t.Errorf("got %v, want the record's fields named", err)
	}
}

func TestWriteTemplateWithoutABodyListsTheFields(t *testing.T) {
	got := render(t, Options{Format: Template}, []listing{})
	for _, want := range []string{"listing:", "projects  []ref", "ref:", "path"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "Dir") {
		t.Errorf("%q lists a json:\"-\" field", got)
	}
}

func TestWriteRefusesWhatTheCommandRenders(t *testing.T) {
	for _, f := range []Format{Text, Name} {
		if err := writer.Write(&bytes.Buffer{}, Options{Format: f}, ref{}); err == nil {
			t.Errorf("Write rendered %s", f)
		}
	}
	if err := (Writer{}).Write(&bytes.Buffer{}, Options{Format: JSON}, ref{}); err == nil {
		t.Error("a Writer with no Codec wrote")
	}
}

func TestTypeLabelDropsPackageQualifiers(t *testing.T) {
	cases := map[string]any{
		"ref":              ref{},
		"[]ref":            []ref{},
		"*ref":             &ref{},
		"map[string][]int": map[string][]int{},
		"Buffer":           bytes.Buffer{},
	}
	for want, v := range cases {
		if got := typeLabel(reflect.TypeOf(v)); got != want {
			t.Errorf("typeLabel(%T) = %q, want %q", v, got, want)
		}
	}
}
