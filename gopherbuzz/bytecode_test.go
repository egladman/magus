package buzz

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestBytecodeRoundTrip compiles every non-error conformance fixture, marshals
// the resulting chunk to bytes, unmarshals it back, executes the recovered
// chunk, and asserts the result matches the fixture's @expect value. This
// exercises the serializer across scalars, strings, enums, objects (including
// field defaults), closures, fibers, and control flow.
func TestBytecodeRoundTrip(t *testing.T) {
	files, err := filepath.Glob("testdata/*.bzz")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no conformance test files found in testdata/")
	}
	for _, path := range files {
		name := strings.TrimSuffix(filepath.Base(path), ".bzz")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			meta := parseConformanceMeta(string(src))
			if meta.skip != "" {
				t.Skipf("skip: %s", meta.skip)
			}
			// Only round-trip programs that compile and run cleanly with an
			// expected value; error fixtures have nothing to serialize.
			// Skip fixtures that use `import "std"` since newSession does not
			// register the std synthetic module (use TestConformance for that).
			if meta.errStr != "" || meta.expect == "" || containsStdImport(string(src)) {
				t.Skip("not a value-producing fixture or requires std import")
			}

			ctx := context.Background()
			sess := newSession(ctx)
			chunk, err := sess.Compile(string(src))
			if err != nil {
				t.Fatalf("compile: %v", err)
			}

			data, err := chunk.Marshal()
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got, err := UnmarshalChunk(data)
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if err := sess.ExecChunk(ctx, got); err != nil {
				t.Fatalf("exec recovered chunk: %v", err)
			}
			if r := sess.GetGlobal("__r"); r.String() != meta.expect {
				t.Errorf("__r = %q, want %q", r.String(), meta.expect)
			}
		})
	}
}

// TestBytecodeObjectDefault round-trips an object whose field defaults are
// non-trivial expressions (an interpolated string and a list literal),
// exercising the AST-node codec path that backs tagObjDecl constants.
func TestBytecodeObjectDefault(t *testing.T) {
	src := `
const WHO = "world";
object Config {
    label: str = "hi {WHO}",
    tags: [str] = ["a", "b"],
    fun describe() str {
        return this.label;
    }
}
const c = Config{};
const __r = c.describe() + " " + c.tags[0];
`
	ctx := context.Background()

	// Baseline: run directly.
	base := newSession(ctx)
	if err := base.Exec(ctx, src); err != nil {
		t.Fatalf("baseline exec: %v", err)
	}
	want := base.GetGlobal("__r").String()

	// Round-trip via ExecBytecode (exercises UnmarshalChunk + ExecChunk).
	sess := newSession(ctx)
	chunk, err := sess.Compile(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	data, err := chunk.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := sess.ExecBytecode(ctx, data); err != nil {
		t.Fatalf("ExecBytecode: %v", err)
	}
	if r := sess.GetGlobal("__r").String(); r != want {
		t.Errorf("round-trip __r = %q, want %q", r, want)
	}
}

// TestBytecodeExportsRoundTrip asserts a SharedGlobals module's exported names
// survive marshal/unmarshal: ExecChunk repopulates the session's export set from
// chunk.Exports, so a spell module loaded from bytecode still exposes its mgs_
// contract. Regression for the v2 format addition — before it, exports were
// dropped and a bytecode-loaded spell exported nothing.
func TestBytecodeExportsRoundTrip(t *testing.T) {
	const src = `export fun mgs_getName() > str { return "go"; }`
	ctx := context.Background()
	sess := newSession(ctx)
	chunk, err := sess.Compile(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if !slices.Contains(chunk.Exports, "mgs_getName") {
		t.Fatalf("compiled chunk Exports = %v, want to contain mgs_getName", chunk.Exports)
	}
	data, err := chunk.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Load into a fresh session so exports can only come from the bytecode.
	fresh := newSession(ctx)
	if err := fresh.ExecBytecode(ctx, data); err != nil {
		t.Fatalf("ExecBytecode: %v", err)
	}
	if _, ok := fresh.Exports()["mgs_getName"]; !ok {
		t.Fatalf("mgs_getName not exported after bytecode round-trip; exports=%v", fresh.Exports())
	}
}

// TestBytecodeDebugRoundTrip marshals a chunk's bytecode (.bo) and debug info
// (.bdb) separately, recovers the chunk from the .bo alone, and asserts
// AttachDebug folds the source lines back onto the function tree to match the
// originally compiled chunk.
func TestBytecodeDebugRoundTrip(t *testing.T) {
	src := `
fun add(a, b) { return a + b; }
const __r = add(2, 3);
`
	ctx := context.Background()
	sess := newSession(ctx)
	chunk, err := sess.Compile(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if chunk.Lines == nil {
		t.Fatal("expected Compile to populate debug lines")
	}

	bo, err := chunk.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	bdb, err := chunk.Marshal(DebugOnly())
	if err != nil {
		t.Fatalf("marshal debug: %v", err)
	}

	got, err := UnmarshalChunk(bo)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Lines != nil {
		t.Fatal("bytecode-only chunk should carry no debug lines before AttachDebug")
	}
	if err := got.AttachDebug(bdb); err != nil {
		t.Fatalf("attach debug: %v", err)
	}
	if !reflect.DeepEqual(got.Lines, chunk.Lines) {
		t.Errorf("top-level lines = %v, want %v", got.Lines, chunk.Lines)
	}
	if len(got.Funs) != len(chunk.Funs) {
		t.Fatalf("funs len = %d, want %d", len(got.Funs), len(chunk.Funs))
	}
	for i := range got.Funs {
		if !reflect.DeepEqual(got.Funs[i].Lines, chunk.Funs[i].Lines) {
			t.Errorf("fun[%d] lines = %v, want %v", i, got.Funs[i].Lines, chunk.Funs[i].Lines)
		}
	}

	// A .bdb with the wrong magic must be rejected, not silently ignored.
	fresh, err := UnmarshalChunk(bo)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	bad := append([]byte(nil), bdb...)
	bad[0] ^= 0xFF
	if err := fresh.AttachDebug(bad); err == nil {
		t.Fatal("expected error for corrupt .bdb magic, got nil")
	}
}

func TestBytecodeVersionGuard(t *testing.T) {
	sess := newSession(context.Background())
	chunk, err := sess.Compile("const __r = 1 + 2;")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	data, err := chunk.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	t.Run("bad magic", func(t *testing.T) {
		bad := append([]byte(nil), data...)
		bad[0] ^= 0xFF
		if _, err := UnmarshalChunk(bad); err == nil {
			t.Fatal("expected error for corrupted magic, got nil")
		}
	})

	t.Run("version mismatch", func(t *testing.T) {
		bad := append([]byte(nil), data...)
		// Version is the 2 bytes immediately after the 4-byte magic.
		bad[4] ^= 0xFF
		if _, err := UnmarshalChunk(bad); err == nil {
			t.Fatal("expected error for version mismatch, got nil")
		}
	})

	t.Run("truncated", func(t *testing.T) {
		if _, err := UnmarshalChunk(data[:3]); err == nil {
			t.Fatal("expected error for truncated data, got nil")
		}
	})

	t.Run("huge_count", func(t *testing.T) {
		// Valid header + empty Name, then Params count = 0xFFFFFFFF.
		// checkCount must reject this before make([]string, n) fires.
		var buf []byte
		buf = append(buf, 'B', 'Z', 'B', 'C')                              // magic
		buf = append(buf, byte(BytecodeVersion), byte(BytecodeVersion>>8)) // version LE
		buf = append(buf, 0, 0, 0, 0)                                      // Name: u32(0) = ""
		buf = append(buf, 0xFF, 0xFF, 0xFF, 0xFF)                          // Params count = 0xFFFFFFFF
		if _, err := UnmarshalChunk(buf); err == nil {
			t.Fatal("expected error for huge count, got nil")
		}
	})
}
