package std

import (
	"context"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// execBuffer runs src against a session with the std library registered and
// returns its globals. The src exercises the upstream-compatible Buffer Zig API.
func execBuffer(t *testing.T, src string) map[string]vm.Value {
	t.Helper()
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	Register(sess)
	require.NoErrorf(t, sess.Exec(context.Background(), src), "Exec\nsrc:\n%s", src)
	return sess.Globals()
}

// TestBufferZigRoundTrip writes f64/i64/u32 scalars at byte offsets through the
// upstream-compatible Zig API and reads them back — the way bubblegum exchanges
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
	x := g["x"]
	assert.True(t, x.IsFloat(), "x IsFloat")
	assert.Equal(t, 3.5, x.AsFloat(), "x")
	y := g["y"]
	assert.True(t, y.IsFloat(), "y IsFloat")
	assert.Equal(t, -1.25, y.AsFloat(), "y")
	u := g["u"]
	assert.True(t, u.IsInt(), "u IsInt")
	assert.Equal(t, int64(4294967295), u.AsInt(), "u")
	s := g["s"]
	assert.True(t, s.IsInt(), "s IsInt")
	assert.Equal(t, int64(-7), s.AsInt(), "s")
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
	require.True(t, base.IsInt(), "ptr() = %v, want a non-zero address", base)
	require.NotEqual(t, int64(0), base.AsInt(), "ptr() = %v, want a non-zero address", base)
	assert.Equal(t, int64(4), g["delta"].AsInt(), "ptr(4) - ptr()")

	// Simulate the C side filling the out-parameter: write through the address
	// the script exposed (the same pinned block a zdef pointer arg would receive),
	// then confirm the bytes are visible at that address.
	addr := uintptr(base.AsInt())
	require.NoError(t, buzz.WriteScalar(addr, 0, "i64", 0x1234, 0, false))
	i, _, _, err := buzz.ReadScalar(addr, 0, "i64")
	require.NoError(t, err)
	assert.Equal(t, int64(0x1234), i, "foreign write through ptr()")
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

	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	Register(sess)
	err := sess.Exec(context.Background(), `
import "buffer";
final b = buffer.Buffer.init(8);
b.ptr();
b.collect();
final v: int = b.readZAt::<int>(0, "i64");
`)
	require.Error(t, err, "readZAt after collect: want a use-after-collect error")
	assert.Contains(t, err.Error(), "after collect", "readZAt after collect: want a use-after-collect error")
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
	assert.Equal(t, int64(64), g["n"].AsInt(), "len()")
	assert.Equal(t, int64(16), g["n4"].AsInt(), "len(4)")
}
