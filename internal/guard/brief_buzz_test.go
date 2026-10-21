package guard

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// briefDecls is the magus\Brief mirror as types-generate emits it from
// cmd/magus-utils/boundary_types.go: every field defaulted, kind a real enum.
const briefDecls = `export enum<str> BriefRefKind {
    output = "output",
    verdict = "verdict",
    invocation = "invocation",
    file = "file",
    plan = "plan",
    commit = "commit",
}

export object BriefDecision {
    what: str = "",
    why: str = "",
}

export object BriefRef {
    kind: BriefRefKind = BriefRefKind.output,
    id: str = "",
    says: str = "",
}

export object Brief {
    job: str = "",
    goal: str = "",
    why: str = "",
    decisions: [BriefDecision] = [],
    evidence: [BriefRef] = [],
}
`

func TestBuzzBriefIsTypeChecked(t *testing.T) {
	for _, tt := range []struct {
		name, brief string
		refused     []string // what the refusal names; none for a pass
	}{
		{"a full literal", "magus\\Brief{ job = \"api\", goal = \"move the store\", decisions = [magus\\BriefDecision{ what = \"keep the API\" }], " +
			"evidence = [magus\\BriefRef{ kind = magus\\BriefRefKind.output, id = \"out1a2b3c\" }] }", nil},
		{"in a buzz fence", "Here:\n```buzz\nmagus\\Brief{ job = \"api\", goal = \"move the store\" }\n```\n", nil},
		{"a missing goal", "magus\\Brief{ job = \"api\" }", []string{"magus\\Brief needs goal"}},
		{"a nested missing field", "magus\\Brief{ job = \"api\", goal = \"g\", evidence = [magus\\BriefRef{ kind = magus\\BriefRefKind.file }] }",
			[]string{"magus\\BriefRef needs id"}},
		{"a wrong type", "magus\\Brief{ job = 42, goal = \"g\" }", []string{"line 1:"}},
		{"a decision as a bare string", "magus\\Brief{ job = \"api\", goal = \"g\", decisions = [\"keep it\"] }", []string{"BZZ"}},
		{"an unknown field", "magus\\Brief{ job = \"api\", goal = \"g\", because = \"x\" }", []string{"because"}},
		{"an unknown ref kind", "magus\\Brief{ job = \"api\", goal = \"g\", evidence = [magus\\BriefRef{ kind = magus\\BriefRefKind.link, id = \"x\" }] }",
			[]string{"link"}},
		{"prose", "Fix the bug and report the sha.", []string{"line 1:"}},
	} {
		v := judgeBriefBuzz(t.Context(), tt.brief, briefDecls, "buzz, from row api")
		if len(tt.refused) == 0 {
			assert.Empty(t, v.Deny, tt.name)
			continue
		}
		assert.Contains(t, v.Deny, "buzz, from row api", "%s: the refusal names the format and its source", tt.name)
		for _, want := range tt.refused {
			assert.Contains(t, v.Deny, want, tt.name)
		}
	}
}

// The embedded magus declarations load as the module a brief imports. Until types-generate
// emits the Brief mirror into them, the fixture stands in for it.
func TestBuzzBriefChecksAgainstTheEmbeddedDeclarations(t *testing.T) {
	decls := magusDecls()
	require.NotEmpty(t, decls)
	if !strings.Contains(decls, "export object Brief {") {
		decls += "\n" + briefDecls
	}
	v := judgeBriefBuzz(t.Context(), "magus\\Brief{ job = \"api\", goal = \"move the store\" }", decls, "buzz, from magus.yaml")
	assert.Empty(t, v.Deny)
}

func TestBriefRequiredFieldsMatchTheTypes(t *testing.T) {
	got := map[string][]string{}
	for _, rt := range []reflect.Type{reflect.TypeFor[types.Brief](), reflect.TypeFor[types.BriefDecision](), reflect.TypeFor[types.BriefRef]()} {
		for i := range rt.NumField() {
			name, opts, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
			if !strings.Contains(opts, "omitempty") {
				got[rt.Name()] = append(got[rt.Name()], name)
			}
		}
	}
	assert.Equal(t, got, briefRequiredFields)
}

func TestBuzzBriefWithoutTheMirrorIsRefused(t *testing.T) {
	v := judgeBriefBuzz(t.Context(), "magus\\Brief{ job = \"api\", goal = \"g\" }", "", "buzz, from magus.yaml")
	assert.Contains(t, v.Deny, "declares no magus\\Brief")
}

func TestBriefCommandNamesAnExplicitFormat(t *testing.T) {
	deps := strict(testDependencies())
	v := judgeBriefCommand(deps, briefInput{text: "Fix the bug.", format: "markdown", formatFrom: "from magus.yaml"})
	assert.Empty(t, v.Deny)
	assert.Contains(t, v.Context, "brief format: markdown, from magus.yaml")

	v = judgeBriefCommand(deps, briefInput{text: "Fix the bug.", format: "markdown", formatFrom: "agents.brief_format unset"})
	assert.Empty(t, v.Context, "an unset format says nothing, so the prompt stays bare")
}
