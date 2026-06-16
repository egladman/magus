package buzzspell

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
	ispell "github.com/egladman/magus/internal/spell"
)

const minimalSpellSrc = `export fun mgs_getName() > str { return "testspell"; }`

func TestExtract_Name(t *testing.T) {
	spec, err := Extract(context.Background(), minimalSpellSrc)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if spec.Name != "testspell" {
		t.Errorf("Name = %q, want %q", spec.Name, "testspell")
	}
}

func TestExtract_MissingGetName(t *testing.T) {
	_, err := Extract(context.Background(), `var x: int = 1;`)
	if err == nil {
		t.Error("Extract: expected error for missing mgs_getName, got nil")
	}
}

func TestExtract_WithTargets(t *testing.T) {
	src := `
export fun mgs_getName() > str { return "mypkg"; }
export fun mgs_listTargets() > any {
    return {"build": {"cmd": "echo", "args": ["ok"]}};
}
`
	spec, err := Extract(context.Background(), src)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if spec.Name != "mypkg" {
		t.Errorf("Name = %q, want %q", spec.Name, "mypkg")
	}
	if _, ok := spec.Targets["build"]; !ok {
		t.Error("Targets[\"build\"] missing")
	}
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
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if b := spec.Targets["build"]; b.Cmd != "go" || len(b.Args) != 1 || b.Args[0] != "build" {
		t.Errorf("build = %+v, want cmd=go args=[build]", b)
	}
	f := spec.Targets["fmt"]
	if f.Cmd != "gofmt" {
		t.Errorf("fmt cmd = %q, want gofmt", f.Cmd)
	}
	ch, ok := f.Charms["write"]
	if !ok {
		t.Fatalf("fmt missing charm \"write\": %+v", f)
	}
	want := ispell.PatchOp{Op: "replace", Path: "/0", Value: "-w"}
	if len(ch.Ops) != 1 || ch.Ops[0] != want {
		t.Errorf("fmt write charm = %+v, want ops=[%v]", ch, want)
	}
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
	if err != nil {
		t.Fatalf("read spells dir: %v", err)
	}
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
			cs := buzz.NewSession(ctx)
			defer cs.Close()
			chunk, err := cs.Compile(combined)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			blob, err := chunk.Marshal()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			// Runtime half: recover from bytecode and resolve the spec.
			rechunk, err := vm.UnmarshalChunk(blob)
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			rs := buzz.NewSession(ctx)
			defer rs.Close()
			if err := rs.ExecChunk(ctx, rechunk); err != nil {
				t.Fatalf("exec chunk: %v", err)
			}
			got, err := ispell.Resolve(ctx, rs, ispell.ForkExtract)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			w, ok := want[got.Name]
			if !ok {
				t.Fatalf("built-in %q (name %q) not in registry", dir, got.Name)
			}
			if !reflect.DeepEqual(got, w) {
				t.Errorf("Spec mismatch for %q:\n got: %#v\nwant: %#v", dir, got, w)
			}
		})
	}
}
