package targetgraph

import (
	"reflect"
	"testing"

	"github.com/egladman/magus/types"
)

func nodeByName(nodes []types.TargetGraphNode, name string) (types.TargetGraphNode, bool) {
	for _, n := range nodes {
		if n.Name == name {
			return n, true
		}
	}
	return types.TargetGraphNode{}, false
}

func TestBuild(t *testing.T) {
	src := `import "magus";

// ── section divider ───────────────────────────────────────
// foo does a thing. This second sentence is dropped.
export fun foo_bar(args: [str]) > void {
    magus.depends_on(["baz"]);
    // magus.depends_on(["ignored"]) — a mention in a comment must not count
}

// separated by a blank line, so it must NOT attach

export fun baz(args: [str]) > void { magus.cmd(["noop"]); }

export fun gen_all(args: [str]) > void {
    magus.depends_on(magus.target.expand_globs(["*-gen"]));
}

export fun a_gen(args: [str]) > void { go["x"](); }
`
	g := Extract(src)

	foo, ok := nodeByName(g, "foo-bar")
	if !ok {
		t.Fatalf("missing foo-bar; got %v", g)
	}
	if foo.Doc != "foo does a thing." {
		t.Errorf("foo-bar doc = %q, want %q", foo.Doc, "foo does a thing.")
	}
	if !reflect.DeepEqual(foo.Dependencies, []string{"baz"}) {
		t.Errorf("foo-bar deps = %v, want [baz] (comment mention ignored)", foo.Dependencies)
	}
	if baz, _ := nodeByName(g, "baz"); baz.Doc != "" {
		t.Errorf("baz doc = %q, want empty (blank line breaks contiguity)", baz.Doc)
	}
	if genAll, _ := nodeByName(g, "gen-all"); !reflect.DeepEqual(genAll.Dependencies, []string{"a-gen"}) {
		t.Errorf("gen-all deps = %v, want [a-gen] (*-gen glob)", genAll.Dependencies)
	}
}

// TestCharms checks that a target's charm reads are extracted: the magus.has_charm
// names (including the built-in "rw"), sorted and deduped, while a has_charm
// mention in a comment or string does not count.
func TestCharms(t *testing.T) {
	g := Extract(`export fun build(args: [str]) > void {
    if (magus.has_charm("container")) { magus.depends_on(["image-build"]); }
    else { magus.depends_on(["go-build"]); }
}
export fun fmt(args: [str]) > void {
    if (magus.has_charm("rw")) { go["go-fmt"](); }
    // magus.has_charm("ignored") in a comment must not count
}
export fun plain(args: [str]) > void { go["x"](); }
`)
	build, _ := nodeByName(g, "build")
	if !reflect.DeepEqual(build.Charms, []string{"container"}) {
		t.Errorf("build charms = %v, want [container]", build.Charms)
	}
	fmtNode, _ := nodeByName(g, "fmt")
	if !reflect.DeepEqual(fmtNode.Charms, []string{"rw"}) {
		t.Errorf("fmt charms = %v, want [rw] (has_charm(\"rw\"), comment mention ignored)", fmtNode.Charms)
	}
	if plain, _ := nodeByName(g, "plain"); len(plain.Charms) != 0 {
		t.Errorf("plain charms = %v, want none", plain.Charms)
	}
}

// TestNameNormalization pins the fix for the node-vs-edge name mismatch: node
// names and depends_on names must both be normalized the way the run path
// registers targets (kebab-case), so a camelCase function and a hyphenated
// dependency reconcile.
func TestNameNormalization(t *testing.T) {
	g := Extract(`export fun goBuild(args: [str]) > void { go["x"](); }
export fun ci(args: [str]) > void { magus.depends_on(["goBuild"]); }
`)
	if _, ok := nodeByName(g, "go-build"); !ok {
		t.Fatalf("camelCase goBuild should normalize to go-build; got %v", g)
	}
	ci, _ := nodeByName(g, "ci")
	if !reflect.DeepEqual(ci.Dependencies, []string{"go-build"}) {
		t.Errorf("ci deps = %v, want [go-build] (dep name normalized to match node)", ci.Dependencies)
	}
}

// TestBraceInString guards collectBody: a `}` inside a string literal must not
// truncate the body and drop the depends_on that follows it.
func TestBraceInString(t *testing.T) {
	g := Extract(`export fun build(args: [str]) > void {
    magus.cmd(["sh", "-c", "echo }"]);
    magus.depends_on(["fmt"]);
}
export fun fmt(args: [str]) > void { go["x"](); }
`)
	build, _ := nodeByName(g, "build")
	if !reflect.DeepEqual(build.Dependencies, []string{"fmt"}) {
		t.Errorf("build deps = %v, want [fmt] (brace in string must not truncate body)", build.Dependencies)
	}
}

// TestTrailingComment guards codeBody: a depends_on in a trailing inline comment
// is prose, not an edge.
func TestTrailingComment(t *testing.T) {
	g := Extract(`export fun build(args: [str]) > void {
    magus.depends_on(["real"]); // magus.depends_on(["fake"])
}
export fun real(args: [str]) > void { go["x"](); }
`)
	build, _ := nodeByName(g, "build")
	if !reflect.DeepEqual(build.Dependencies, []string{"real"}) {
		t.Errorf("build deps = %v, want [real] (trailing comment ignored)", build.Dependencies)
	}
}

// TestExpandGlobsMultiPattern guards expandRe: expand_globs takes a list, so
// every pattern in it must be honored, not just the first.
func TestExpandGlobsMultiPattern(t *testing.T) {
	g := Extract(`export fun all(args: [str]) > void {
    magus.depends_on(magus.target.expand_globs(["*-gen", "check-*"]));
}
export fun docs_gen(args: [str]) > void { go["x"](); }
export fun check_lint(args: [str]) > void { go["x"](); }
`)
	all, _ := nodeByName(g, "all")
	want := []string{"docs-gen", "check-lint"}
	if !reflect.DeepEqual(all.Dependencies, want) {
		t.Errorf("all deps = %v, want %v (both glob patterns honored)", all.Dependencies, want)
	}
}

// TestNeedsHandles guards the magus.needs handle edges: named is an exact dep,
// glob and regex resolve against sibling target names, a multi-arg needs call
// yields every edge (per-leaf extraction, not call-spanning), and a handle in a
// trailing comment is prose, not an edge.
func TestNeedsHandles(t *testing.T) {
	g := Extract(`export fun build(args: [str]) > void { go["x"](); }
export fun a_gen(args: [str]) > void { go["x"](); }
export fun b_gen(args: [str]) > void { go["x"](); }
export fun test(args: [str]) > void {
    magus.needs(magus.target.named("build"));
    magus.needs(magus.target.glob("*-gen"), magus.target.regex("^b-"));
    // magus.target.named("ignored") in a comment must not count
}
`)
	test, ok := nodeByName(g, "test")
	if !ok {
		t.Fatalf("missing test; got %v", g)
	}
	want := []string{"build", "a-gen", "b-gen"}
	if !reflect.DeepEqual(test.Dependencies, want) {
		t.Errorf("test deps = %v, want %v (named exact + glob + regex; comment ignored)", test.Dependencies, want)
	}
}

func TestCycle(t *testing.T) {
	acyclic := Extract(`export fun a(args: [str]) > void { magus.depends_on(["b"]); }
export fun b(args: [str]) > void { magus.depends_on(["c"]); }
export fun c(args: [str]) > void { go["x"](); }
`)
	if c := Cycle(acyclic); c != nil {
		t.Errorf("acyclic graph reported cycle %v", c)
	}

	cyclic := Extract(`export fun a(args: [str]) > void { magus.depends_on(["b"]); }
export fun b(args: [str]) > void { magus.depends_on(["c"]); }
export fun c(args: [str]) > void { magus.depends_on(["a"]); }
`)
	c := Cycle(cyclic)
	if c == nil {
		t.Fatal("cyclic graph reported no cycle")
	}
	if c[0] != c[len(c)-1] {
		t.Errorf("cycle %v should start and end at the same node", c)
	}
}

// TestCycleAcrossNormalization is the regression for the silent-pass bug: a real
// cycle written with mixed casing must still be detected once both sides are
// normalized.
func TestCycleAcrossNormalization(t *testing.T) {
	g := Extract(`export fun aB(args: [str]) > void { magus.depends_on(["bC"]); }
export fun bC(args: [str]) > void { magus.depends_on(["aB"]); }
`)
	if Cycle(g) == nil {
		t.Error("mixed-case cycle aB→bC→aB not detected")
	}
}
