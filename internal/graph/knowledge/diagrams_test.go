package knowledge

import (
	"fmt"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// diagramWorkspace holds a project and four Buzz files of diagrams: one with literal
// ids and dirs, one built from parameters, a duplicate id, and an unrelated diagram
// namespace.
func diagramWorkspace(t *testing.T) string {
	t.Helper()
	ws := testkit.NewWorkspace(t)
	ws.Write("libs/engine/magusfile.buzz", "export fun ci() > void {}\n")
	ws.Write("diagrams/engine.buzz", `namespace engine;

import "magus";
import "magus/diagram";

final TOPIC = "engine";

export fun engineDiagram() > diagram\Diagram !> any {
    final engine = magus\graph().dir("libs/engine");
    final vm = magus\graph().dir("libs/engine/vm/");
    return diagram\of("engine",
            title: "One engine",
            eyebrow: TOPIC,
            desc: "Every program goes through "
                + "one pipeline.",
            direction: diagram\Direction.across)
        .box(engine, label: "Engine")
        .box(vm, label: "VM")
        .box(magus\graph().dir("libs/engine"), label: "again");
}
`)
	ws.Write("diagrams/session.buzz", `import "magus" as m;
import "magus/diagram" as fig;

fun sessionDiagram(name: str, dir: str) > fig\Diagram !> any {
    return fig\of("session-{name}",
            title: "Session",
            desc: "Jobs of " + name)
        .box(m\graph().dir(dir), label: "jobs");
}
`)
	ws.Write("diagrams/later.buzz", `import "magus/diagram";

fun again() > diagram\Diagram !> any {
    return diagram\of("engine", title: "A second engine");
}
`)
	// No magus/diagram import: an unrelated namespace called diagram draws nothing.
	ws.Write("diagrams/other.buzz", `import "diagram";

fun other() > void {
    diagram\of("not-a-diagram");
}
`)
	return ws.Root()
}

func TestDiagramsFromBuzz(t *testing.T) {
	shard := assembleBuzz(diagramWorkspace(t))
	out := mergeAll([]Shard{shard}).Output()

	var diagrams []types.KnowledgeNode
	for _, n := range out.Nodes {
		if n.Kind == types.KindDiagram {
			diagrams = append(diagrams, n)
		}
	}
	assert.Equal(t, []types.KnowledgeNode{
		{
			ID: "diagram:diagrams/session.buzz:sessionDiagram", Kind: types.KindDiagram, Label: "Session",
			Source: "diagrams/session.buzz:5", Attrs: map[string]string{attrUnresolved: "id,desc,dir"},
		},
		{
			ID: "diagram:engine", Kind: types.KindDiagram, Label: "One engine",
			Doc: "Every program goes through one pipeline.", Source: "diagrams/engine.buzz:11",
			Attrs: map[string]string{attrEyebrow: "engine", attrDuplicate: "diagrams/later.buzz:4"},
		},
	}, diagrams)

	assert.Equal(t, "engine\nEvery program goes through one pipeline.", shard.Text["diagram:engine"],
		"the lexical body carries the eyebrow and the whole desc")
	_, ok := shard.Text["diagram:diagrams/session.buzz:sessionDiagram"]
	assert.False(t, ok, "a diagram with no literal eyebrow or desc has no body")

	var depicts []string
	for _, e := range out.Links {
		if e.Relation == types.RelationDepicts {
			depicts = append(depicts, fmt.Sprintf("%s -> %s", e.Source, e.Target))
		}
	}
	assert.Equal(t, []string{
		"diagram:engine -> dir:libs/engine",
		"diagram:engine -> dir:libs/engine/vm",
		"diagram:engine -> project:libs/engine",
	}, depicts)

	e, ok := findEdge(out, "file:diagrams/engine.buzz", "diagram:engine", types.RelationContains)
	require.True(t, ok, "the file contains the diagram it builds")
	assert.Equal(t, types.ConfidenceExtracted, e.Confidence)
	assert.False(t, hasEdge(out, "file:diagrams/later.buzz", "diagram:engine", types.RelationContains),
		"a duplicate id is recorded on the first diagram, never merged into it")
}

func TestLiteralString(t *testing.T) {
	consts := map[string]string{"WHY": "because"}
	for _, tc := range []struct {
		src  string
		want string
		ok   bool
	}{
		{`"plain"`, "plain", true},
		{`"a" + "b" + WHY`, "abbecause", true},
		{`"n={n}"`, "", false},
		{`"a" + name`, "", false},
		{`"a" - "b"`, "", false},
	} {
		prog, err := buzz.ParseEmbedded(fmt.Sprintf("final x = %s;", tc.src))
		require.NoError(t, err, tc.src)
		decl, isDecl := prog.Stmts[0].(*ast.DeclStmt)
		require.True(t, isDecl, tc.src)
		got, ok := literalString(decl.Value, consts)
		assert.Equal(t, tc.ok, ok, tc.src)
		assert.Equal(t, tc.want, got, tc.src)
	}
}
