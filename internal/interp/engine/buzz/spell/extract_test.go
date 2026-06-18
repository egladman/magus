package buzzspell

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
	ispell "github.com/egladman/magus/internal/spell"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const minimalSpellSrc = `export fun mgs_getName() > str { return "testspell"; }`

func TestExtract_Name(t *testing.T) {
	spec, err := Extract(context.Background(), minimalSpellSrc)
	require.NoError(t, err)
	assert.Equal(t, "testspell", spec.Name)
}

func TestExtract_MissingGetName(t *testing.T) {
	_, err := Extract(context.Background(), `var x: int = 1;`)
	assert.Error(t, err, "expected error for missing mgs_getName")
}

func TestExtract_WithTargets(t *testing.T) {
	src := `
export fun mgs_getName() > str { return "mypkg"; }
export fun mgs_listTargets() > any {
    return {"build": {"cmd": "echo", "args": ["ok"]}};
}
`
	spec, err := Extract(context.Background(), src)
	require.NoError(t, err)
	assert.Equal(t, "mypkg", spec.Name)
	assert.Contains(t, spec.Ops, "build", "Targets[\"build\"] missing")
}

// TestExtract_FunctionValueTargets verifies the strictly-typed fork form:
// mgs_listTargets returning {str: fun(Target, fun(any)) bool} handlers, referenced
// by value, that hand a {cmd, args, charms} record to the magus-injected cb callback.
// A self-contained (fork) spell's handlers are called once at resolution to record
// their specs, so the result decodes to the same fork targets a plain data form
// would — proving the typed form is behaviorally identical to the old form.
func TestExtract_FunctionValueTargets(t *testing.T) {
	src := `
import "magus/target";
export fun mgs_getName() > str { return "fnpkg"; }
fun build(t: Target, cb: fun(any)) > bool { cb({"cmd": "go", "args": ["build"]}); return true; }
fun fmt(t: Target, cb: fun(any)) > bool {
    cb({"cmd": "gofmt", "args": ["-l", "."], "charms": {"write": {"ops": [{"op": "replace", "path": "/0", "value": "-w"}]}}}); return true;
}
export fun mgs_listTargets() > {str: fun(Target, fun(any)) bool} {
    return {"build": build, "fmt": fmt};
}
`
	spec, err := Extract(context.Background(), src)
	require.NoError(t, err)
	b := spec.Ops["build"]
	assert.Equal(t, "go", b.Cmd)
	assert.Equal(t, []string{"build"}, b.Args)

	f := spec.Ops["fmt"]
	assert.Equal(t, "gofmt", f.Cmd)
	ch, ok := f.Charms["write"]
	require.Truef(t, ok, "fmt missing charm \"write\": %+v", f)
	want := ispell.PatchOp{Op: "replace", Path: "/0", Value: "-w"}
	assert.Equal(t, []ispell.PatchOp{want}, ch.Ops)
}

// TestBuiltinBytecodeParity proves the embedded-bytecode pipeline end to end for
// every self-contained built-in: authored .buzz -> Compile -> Marshal ->
// UnmarshalChunk -> ExecChunk -> Resolve yields a Spec identical to the one in the
// in-process registry (ispell.Builtins(), keyed by runtime name). It walks the
// spells/ source tree, skipping function-op spells (e.g. github) that import host
// modules and so are not bare-compilable built-ins.
func TestBuiltinBytecodeParity(t *testing.T) {
	want := ispell.Builtins()
	ctx := context.Background()
	// spellsDir is relative to this package (magus/internal/interp/engine/buzz/spell).
	const spellsDir = "../../../../../spells"
	dirs, err := os.ReadDir(spellsDir)
	require.NoError(t, err)
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := d.Name()
		src, err := os.ReadFile(filepath.Join(spellsDir, dir, "spell.buzz"))
		if err != nil {
			continue // not a spell dir
		}
		// Build-time half: inline the magus/target module exactly as the built-in
		// generator does so the chunk is self-contained. A spell that imports host
		// modules (e.g. github) is not a bare-compilable built-in — skip it.
		combined, ok := ispell.SelfContainedBuiltinSource(string(src))
		if !ok {
			continue
		}
		t.Run(dir, func(t *testing.T) {
			cs := buzz.NewSession(ctx, buzz.WithEmbedded())
			defer cs.Close()
			chunk, err := cs.Compile(combined)
			require.NoError(t, err, "compile")
			blob, err := chunk.Marshal()
			require.NoError(t, err, "marshal")
			// Runtime half: recover from bytecode and resolve the spec.
			rechunk, err := vm.UnmarshalChunk(blob)
			require.NoError(t, err, "unmarshal")
			rs := buzz.NewSession(ctx, buzz.WithEmbedded())
			defer rs.Close()
			require.NoError(t, rs.ExecChunk(ctx, rechunk), "exec chunk")
			got, err := ispell.Resolve(ctx, rs, ispell.ForkExtract)
			require.NoError(t, err, "resolve")
			w, ok := want[got.Name]
			require.Truef(t, ok, "built-in %q (name %q) not in registry", dir, got.Name)
			assert.Equalf(t, w, got, "Spec mismatch for %q", dir)
		})
	}
}
