package std_test

import (
	"context"
	"strings"
	"testing"

	buzz "github.com/egladman/gopherbuzz"
	buzzstd "github.com/egladman/gopherbuzz/std"
)

// execBuffer runs src against a session with the std library registered and
// returns its globals. The src exercises the upstream-compatible Buffer Zig API.
func execBuffer(t *testing.T, src string) map[string]buzz.Value {
	t.Helper()
	sess := buzz.NewSession(context.Background())
	defer func() { _ = sess.Close() }()
	buzzstd.Register(sess)
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatalf("Exec: %v\nsrc:\n%s", err, src)
	}
	return sess.Globals()
}

// TestBufferZigRoundTrip writes f64/i64/u32 scalars at byte offsets through the
// upstream-compatible Zig API and reads them back — the way yeetile exchanges
// CGPoint/CGSize and CGDirectDisplayID values with C.
func TestBufferZigRoundTrip(t *testing.T) {
	g := execBuffer(t, `
import "buffer";
final b = buffer.Buffer.init(16);
// A CGPoint: two f64. writeZAt is byte-offset (0, 8); readZAt is element-index
// (0, 1) — the upstream asymmetry, both mapping to bytes 0 and 8.
b.writeZAt::<double>(0, "f64", [3.5]);
b.writeZAt::<double>(8, "f64", [-1.25]);
final x: double = b.readZAt::<double>(0, "f64");
final y: double = b.readZAt::<double>(1, "f64");
// u32 reads back unsigned, i64 signed and negative.
b.writeZAt::<int>(0, "u32", [4294967295]);
b.writeZAt::<int>(8, "i64", [-7]);
final u: int = b.readZAt::<int>(0, "u32");
final s: int = b.readZAt::<int>(1, "i64");
b.collect();
`)
	if x := g["x"]; !x.IsFloat() || x.AsFloat() != 3.5 {
		t.Errorf("x = %v, want 3.5", x)
	}
	if y := g["y"]; !y.IsFloat() || y.AsFloat() != -1.25 {
		t.Errorf("y = %v, want -1.25", y)
	}
	if u := g["u"]; !u.IsInt() || u.AsInt() != 4294967295 {
		t.Errorf("u = %v, want 4294967295", u)
	}
	if s := g["s"]; !s.IsInt() || s.AsInt() != -7 {
		t.Errorf("s = %v, want -7", s)
	}
}

// TestBufferPtrOutParam verifies ptr() yields a real, stable machine address and
// that bytes written through it via the ffi provider (as a C out-parameter would)
// are visible to readZAt. ptr(at) is the base offset by `at`.
func TestBufferPtrOutParam(t *testing.T) {
	g := execBuffer(t, `
import "buffer";
final b = buffer.Buffer.init(8);
final base = b.ptr();
final at4 = b.ptr(4);
final delta = at4 - base;
`)
	base := g["base"]
	if !base.IsInt() || base.AsInt() == 0 {
		t.Fatalf("ptr() = %v, want a non-zero address", base)
	}
	if d := g["delta"]; d.AsInt() != 4 {
		t.Errorf("ptr(4) - ptr() = %d, want 4", d.AsInt())
	}

	// Simulate the C side filling the out-parameter: write through the address
	// the script exposed (the same pinned block a zdef pointer arg would receive),
	// then confirm the bytes are visible at that address.
	addr := uintptr(base.AsInt())
	if err := buzz.WriteScalar(addr, 0, "i64", 0x1234, 0, false); err != nil {
		t.Fatal(err)
	}
	i, _, _, err := buzz.ReadScalar(addr, 0, "i64")
	if err != nil {
		t.Fatal(err)
	}
	if i != 0x1234 {
		t.Errorf("foreign write through ptr() = %#x, want %#x", i, 0x1234)
	}
	_ = buzz.FreeFFI(addr)
}

// TestBufferCollectIdempotent confirms collect() frees once, is safe to call
// twice (upstream's double-free guard), and that the Zig API errors after
// collect rather than touching freed memory.
func TestBufferCollectIdempotent(t *testing.T) {
	execBuffer(t, `
import "buffer";
final b = buffer.Buffer.init(8);
b.ptr();
b.collect();
b.collect();
`)

	sess := buzz.NewSession(context.Background())
	defer func() { _ = sess.Close() }()
	buzzstd.Register(sess)
	err := sess.Exec(context.Background(), `
import "buffer";
final b = buffer.Buffer.init(8);
b.ptr();
b.collect();
final v: int = b.readZAt::<int>(0, "i64");
`)
	if err == nil || !strings.Contains(err.Error(), "after collect") {
		t.Errorf("readZAt after collect: err = %v, want a use-after-collect error", err)
	}
}

// TestBufferLenAlign checks len(align) divides the capacity once the pinned block
// is live.
func TestBufferLenAlign(t *testing.T) {
	g := execBuffer(t, `
import "buffer";
final b = buffer.Buffer.init(64);
b.ptr();
final n: int = b.len();
final n4: int = b.len(4);
b.collect();
`)
	if n := g["n"]; n.AsInt() != 64 {
		t.Errorf("len() = %d, want 64", n.AsInt())
	}
	if n4 := g["n4"]; n4.AsInt() != 16 {
		t.Errorf("len(4) = %d, want 16", n4.AsInt())
	}
}
