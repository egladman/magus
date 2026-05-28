package buzz

import "testing"

// promoteOpts is SharedGlobals with top-level slot promotion enabled — the mode
// the magusfile entrypoint path uses. sharedOpts is
// the same without promotion (the REPL/incremental path).
var (
	promoteOpts = CompileOptions{SharedGlobals: true, PromoteTopLevel: true}
	sharedOpts  = CompileOptions{SharedGlobals: true}
)

// countOps tallies how many times each opcode appears in a freshly compiled chunk.
func countOps(t *testing.T, src string, opts CompileOptions) map[OpCode]int {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	chunk, err := CompileWith(prog, opts)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	got := map[OpCode]int{}
	for _, ins := range chunk.Code {
		got[ins.Op]++
	}
	return got
}

// TestPromoteEquivalence is the core soundness gate: enabling PromoteTopLevel must
// never change a single chunk's result versus plain SharedGlobals. Promotion only
// changes where a chunk-private var is stored (slot vs Env), not what it computes,
// and captured/exported names are left on the Env path, so the two modes must agree
// for every program — including ones with closures over top-level state.
func TestPromoteEquivalence(t *testing.T) {
	srcs := []string{
		`var a = 3; var b = 4; return a * a + b * b;`,
		`var sum = 0; var i = 0; while (i < 1000) { sum = sum + i; i = i + 1; } return sum;`,
		`var s = ""; var i = 0; while (i < 5) { s = "item {i}"; i = i + 1; } return s;`,
		`var s = 0; for (var i = 0; i < 5; i = i + 1) { s = s + i; } return s;`,
		`var total = 0; foreach (x in 0..10) { total = total + x; } return total;`,
		`var n = 0; { var n = 41; } return n + 1;`, // inner block shadows
		// Closures over top-level state: must stay live-Env, identical in both modes.
		`var x = 1; fun getx() int { return x; } x = 2; return getx();`,
		`var acc = 0; fun add(n) { acc = acc + n; } add(3); add(4); return acc;`,
		// Mix of promotable scratch and a captured config var in one chunk.
		`var cfg = 7; var t = 0; fun bump() { t = t + cfg; } bump(); bump(); var scratch = 0; foreach (k in 0..3) { scratch = scratch + k; } return t + scratch;`,
	}
	for _, src := range srcs {
		promote := runProg(t, src, promoteOpts)
		shared := runProg(t, src, sharedOpts)
		// RawEqual is raw-bits (pointer identity for heap values), so compare by
		// kind + rendered content to cover string results too.
		if promote.String() != shared.String() || promote.IsStr() != shared.IsStr() {
			t.Errorf("promote vs shared mismatch for %q: promote=%s shared=%s", src, promote.String(), shared.String())
		}
	}
}

// TestPromotePromotesChunkPrivate verifies that a chunk-private top-level var is
// actually slot-promoted: no OpDefName/OpLoadName/OpStoreName for it (those are the
// Env path), and the loop body uses the slot opcodes instead.
func TestPromotePromotesChunkPrivate(t *testing.T) {
	src := `var sum = 0; var i = 0; while (i < 1000) { sum = sum + i; i = i + 1; } return sum;`

	shared := countOps(t, src, sharedOpts)
	if shared[OpDefName] == 0 || shared[OpLoadName] == 0 {
		t.Fatalf("baseline SharedGlobals should use Env ops, got DefName=%d LoadName=%d", shared[OpDefName], shared[OpLoadName])
	}

	promote := countOps(t, src, promoteOpts)
	if promote[OpDefName] != 0 || promote[OpLoadName] != 0 || promote[OpStoreName] != 0 {
		t.Errorf("promoted chunk-private vars must not use Env ops: DefName=%d LoadName=%d StoreName=%d",
			promote[OpDefName], promote[OpLoadName], promote[OpStoreName])
	}
	if promote[OpSetLocal] == 0 || promote[OpGetLocal] == 0 {
		t.Errorf("promoted vars should use slot ops: SetLocal=%d GetLocal=%d", promote[OpSetLocal], promote[OpGetLocal])
	}
}

// TestPromoteKeepsCapturedInEnv verifies the closure carve-out: a top-level var
// referenced inside a function body is NOT promoted (stays an Env binding), so the
// closure keeps reading it live — and PromoteTopLevel does not silently flip it to
// a by-value upvalue snapshot.
func TestPromoteKeepsCapturedInEnv(t *testing.T) {
	src := `var x = 1; fun getx() int { return x; } x = 2; return getx();`
	ops := countOps(t, src, promoteOpts)
	if ops[OpDefName] == 0 {
		t.Errorf("captured top-level var must stay an Env binding (expected OpDefName), got none")
	}
	// Live-Env semantics: the post-definition mutation x = 2 is visible to getx().
	wantInt(t, runProg(t, src, promoteOpts), 2)
}

// TestPromoteKeepsExportedInEnv verifies exported top-level vars stay Env bindings
// (the cross-chunk/cross-module surface) even when promotion is on, and remain
// recorded in chunk.Exports.
func TestPromoteKeepsExportedInEnv(t *testing.T) {
	src := `export var version = 3; var scratch = 0; foreach (k in 0..3) { scratch = scratch + k; } return version + scratch;`
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	chunk, err := CompileWith(prog, promoteOpts)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	hasExport := false
	for _, n := range chunk.Exports {
		if n == "version" {
			hasExport = true
		}
	}
	if !hasExport {
		t.Errorf("exported var should be recorded in chunk.Exports, got %v", chunk.Exports)
	}
	ops := countOps(t, src, promoteOpts)
	if ops[OpDefName] == 0 {
		t.Errorf("exported var must stay an Env binding (expected OpDefName), got none")
	}
}
