package knowledge

import (
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFiguresFromBuzz(t *testing.T) {
	ws := testkit.NewWorkspace(t)
	ws.Write("libs/engine/magusfile.buzz", "export fun ci() > void {}\n")
	ws.Write("diagrams/engine.buzz", `namespace engine;

import "magus";
import "magus/figure";

final TOPIC = "engine";

export fun engineFigure() > figure\Figure !> any {
    final engine = magus\dir("libs/engine");
    final vm = magus\dir("libs/engine/vm/");
    return figure\of("engine",
            title: "One engine",
            eyebrow: TOPIC,
            desc: "Every program goes through "
                + "one pipeline.",
            direction: figure\Direction.across)
        .box(engine, label: "Engine")
        .box(vm, label: "VM")
        .box(magus\dir("libs/engine"), label: "again");
}
`)
	ws.Write("diagrams/session.buzz", `import "magus" as m;
import "magus/figure" as fig;

fun sessionFigure(name: str, dir: str) > fig\Figure !> any {
    return fig\of("session-{name}",
            title: "Session",
            desc: "Jobs of " + name)
        .box(m\dir(dir), label: "jobs");
}
`)
	ws.Write("diagrams/later.buzz", `import "magus/figure";

fun again() > figure\Figure !> any {
    return figure\of("engine", title: "A second engine");
}
`)
	// No magus/figure import: an unrelated namespace called figure draws nothing.
	ws.Write("diagrams/other.buzz", `import "figure";

fun other() > void {
    figure\of("not-a-figure");
}
`)

	shard := assembleBuzz(ws.Root())
	out := mergeAll([]Shard{shard}).Output()

	var figures []types.KnowledgeNode
	for _, n := range out.Nodes {
		if n.Kind == types.KindFigure {
			figures = append(figures, n)
		}
	}
	assert.Equal(t, []types.KnowledgeNode{
		{
			ID: "figure:diagrams/session.buzz:sessionFigure", Kind: types.KindFigure, Label: "Session",
			Source: "diagrams/session.buzz:5", Attrs: map[string]string{attrUnresolved: "id,desc,dir"},
		},
		{
			ID: "figure:engine", Kind: types.KindFigure, Label: "One engine",
			Doc: "Every program goes through one pipeline.", Source: "diagrams/engine.buzz:11",
			Attrs: map[string]string{attrEyebrow: "engine", attrDuplicate: "diagrams/later.buzz:4"},
		},
	}, figures)

	assert.Equal(t, "engine\nEvery program goes through one pipeline.", shard.Text["figure:engine"],
		"the lexical body carries the eyebrow and the whole desc")
	_, ok := shard.Text["figure:diagrams/session.buzz:sessionFigure"]
	assert.False(t, ok, "a figure with no literal eyebrow or desc has no body")

	var depicts []string
	for _, e := range out.Links {
		if e.Relation == types.RelationDepicts {
			depicts = append(depicts, e.Source+" -> "+e.Target)
		}
	}
	assert.Equal(t, []string{
		"figure:engine -> dir:libs/engine",
		"figure:engine -> dir:libs/engine/vm",
		"figure:engine -> project:libs/engine",
	}, depicts)

	e, ok := findEdge(out, "file:diagrams/engine.buzz", "figure:engine", types.RelationContains)
	require.True(t, ok, "the file contains the figure it builds")
	assert.Equal(t, types.ConfidenceExtracted, e.Confidence)
	assert.False(t, hasEdge(out, "file:diagrams/later.buzz", "figure:engine", types.RelationContains),
		"a duplicate id is recorded on the first figure, never merged into it")
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
		prog, err := buzz.ParseEmbedded("final x = " + tc.src + ";")
		require.NoError(t, err, tc.src)
		decl, isDecl := prog.Stmts[0].(*ast.DeclStmt)
		require.True(t, isDecl, tc.src)
		got, ok := literalString(decl.Value, consts)
		assert.Equal(t, tc.ok, ok, tc.src)
		assert.Equal(t, tc.want, got, tc.src)
	}
}
