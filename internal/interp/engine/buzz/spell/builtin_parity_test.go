package buzzspell

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
	ispell "github.com/egladman/magus/internal/spell"
)

// TestBuiltinBytecodeParity proves the embedded-bytecode pipeline end to end for
// every self-contained built-in: authored .bzz -> Compile -> Marshal ->
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
		src, err := os.ReadFile(filepath.Join(spellsDir, dir, "spell.bzz"))
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
