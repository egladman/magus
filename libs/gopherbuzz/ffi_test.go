package buzz

import (
	"context"
	"testing"

	vmpackage "github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests exercise the portable FFI surface — the C-decl parser and the
// FFIProvider boundary — without depending on the purego backend, so they run on
// every platform (including those where zdef() has no default provider).

func TestParseCDecls(t *testing.T) {
	sigs, err := ParseCDecls("double sqrt(double x); int abs(int v); void srand(unsigned seed);")
	require.NoError(t, err)
	require.Len(t, sigs, 3)
	assert.Equal(t, "sqrt", sigs[0].Name, "sqrt sig wrong: %+v", sigs[0])
	assert.Equal(t, CDouble, sigs[0].Ret, "sqrt sig wrong: %+v", sigs[0])
	require.Len(t, sigs[0].Params, 1, "sqrt sig wrong: %+v", sigs[0])
	assert.Equal(t, CDouble, sigs[0].Params[0].Type, "sqrt sig wrong: %+v", sigs[0])
	assert.Equal(t, "abs", sigs[1].Name, "abs sig wrong: %+v", sigs[1])
	assert.Equal(t, CInt, sigs[1].Ret, "abs sig wrong: %+v", sigs[1])
	assert.Equal(t, CInt, sigs[1].Params[0].Type, "abs sig wrong: %+v", sigs[1])
	assert.Equal(t, "srand", sigs[2].Name, "srand sig wrong: %+v", sigs[2])
	assert.Equal(t, CVoid, sigs[2].Ret, "srand sig wrong: %+v", sigs[2])
	assert.Equal(t, CUint, sigs[2].Params[0].Type, "srand sig wrong: %+v", sigs[2])
}

func TestParseCDeclsCharPtr(t *testing.T) {
	sigs, err := ParseCDecls("char* getenv(final char* name);")
	require.NoError(t, err)
	assert.Equal(t, CCharPtr, sigs[0].Ret, "getenv sig wrong: %+v", sigs[0])
	assert.Equal(t, CCharPtr, sigs[0].Params[0].Type, "getenv sig wrong: %+v", sigs[0])
}

// fakeFFI is a test FFIProvider: it binds every signature to a Go closure,
// proving an embedder can supply FFI on a platform the purego backend doesn't
// cover. It ignores the library and returns canned values per type.
type fakeFFI struct{ opened string }

func (f *fakeFFI) OpenLibrary(libname string, sigs []CFuncSig) (vmpackage.Value, error) {
	f.opened = libname
	m := vmpackage.NewMap()
	for _, sig := range sigs {
		sig := sig
		m.MapSet(sig.Name, vmpackage.DirectValue(sig.Name, func(_ context.Context, args []vmpackage.Value) (vmpackage.Value, error) {
			// Echo: return the first int arg doubled, or a marker string.
			if len(args) > 0 && args[0].IsInt() {
				return vmpackage.IntValue(args[0].AsInt() * 2), nil
			}
			return vmpackage.StrValue("called:" + sig.Name), nil
		}))
	}
	return m, nil
}

func TestFFIProviderInjection(t *testing.T) {
	// Install a custom provider, then drive zdef() from Buzz and confirm the
	// injected callable runs. Save/restore the global so we don't disturb the
	// default backend other tests rely on.
	prev := GetFFIProvider()
	defer func() { SetFFIProvider(prev) }()
	fake := &fakeFFI{}
	SetFFIProvider(fake)

	sess := newSession(context.Background())
	src := `
final lib = zdef("mylib", "int dbl(int x);");
final r = lib.dbl(21);
`
	require.NoError(t, sess.Exec(context.Background(), src), "exec")
	assert.Equal(t, "mylib", fake.opened, "provider saw lib %q, want mylib", fake.opened)
	got := sess.GetGlobal("r")
	require.True(t, got.IsInt(), "r = %s, want 42", got.String())
	assert.Equal(t, int64(42), got.AsInt(), "r = %s, want 42", got.String())
}

func TestFFINoProvider(t *testing.T) {
	// With no provider installed, zdef() must return a clear error rather than
	// panic or silently no-op — this is the off-platform path.
	prev := GetFFIProvider()
	defer func() { SetFFIProvider(prev) }()
	SetFFIProvider(nil)

	sess := newSession(context.Background())
	err := sess.Exec(context.Background(), `final lib = zdef("libm", "double sqrt(double x);");`)
	assert.Error(t, err, "expected an error when no FFI provider is registered")
}

func TestRegisterFFIProviderIgnoresNil(t *testing.T) {
	prev := GetFFIProvider()
	defer func() { SetFFIProvider(prev) }()
	RegisterFFIProvider(nil) // must not clobber an installed provider
	assert.Equal(t, prev, GetFFIProvider(), "RegisterFFIProvider(nil) overwrote the provider")
}

func TestParseCDeclsExternVar(t *testing.T) {
	sigs, err := ParseCDecls(
		"extern void *kBoolTrue;" +
			"extern const char *greeting;" +
			"extern int answer;" +
			"extern struct Callbacks kCallbacks;" +
			"double sqrt(double x);")
	require.NoError(t, err)
	require.Len(t, sigs, 5)
	assert.True(t, sigs[0].IsVar, "kBoolTrue sig wrong: %+v", sigs[0])
	assert.Equal(t, "kBoolTrue", sigs[0].Name, "kBoolTrue sig wrong: %+v", sigs[0])
	assert.Equal(t, CVoidPtr, sigs[0].Ret, "kBoolTrue sig wrong: %+v", sigs[0])
	assert.True(t, sigs[1].IsVar, "greeting sig wrong: %+v", sigs[1])
	assert.Equal(t, "greeting", sigs[1].Name, "greeting sig wrong: %+v", sigs[1])
	assert.Equal(t, CCharPtr, sigs[1].Ret, "greeting sig wrong: %+v", sigs[1])
	assert.True(t, sigs[2].IsVar, "answer sig wrong: %+v", sigs[2])
	assert.Equal(t, "answer", sigs[2].Name, "answer sig wrong: %+v", sigs[2])
	assert.Equal(t, CInt, sigs[2].Ret, "answer sig wrong: %+v", sigs[2])
	assert.Equal(t, "int", sigs[2].VarTypeName, "answer sig wrong: %+v", sigs[2])
	assert.True(t, sigs[3].IsVar, "kCallbacks sig wrong: %+v", sigs[3])
	assert.Equal(t, "kCallbacks", sigs[3].Name, "kCallbacks sig wrong: %+v", sigs[3])
	assert.Equal(t, CAddr, sigs[3].Ret, "kCallbacks sig wrong: %+v", sigs[3])
	assert.False(t, sigs[4].IsVar, "sqrt sig wrong: %+v", sigs[4])
	assert.Equal(t, "sqrt", sigs[4].Name, "sqrt sig wrong: %+v", sigs[4])
}

func TestParseCDeclsExternVarErrors(t *testing.T) {
	_, err := ParseCDecls("extern int;")
	assert.Error(t, err, "extern without a name should fail")
	_, err = ParseCDecls("int loose;")
	assert.Error(t, err, "a variable without the extern keyword should fail")
}

func TestParseCDeclsPoint2D(t *testing.T) {
	sigs, err := ParseCDecls("CGPoint CGEventGetLocation(void *event); NSPoint mouseLocation(void);")
	require.NoError(t, err)
	assert.Equal(t, CPoint2D, sigs[0].Ret, "point returns wrong: %+v", sigs)
	assert.Equal(t, CPoint2D, sigs[1].Ret, "point returns wrong: %+v", sigs)
	_, err = ParseCDecls("void use(CGPoint p);")
	assert.Error(t, err, "by-value struct parameter should be rejected with advice")
}

func TestParseZigDecls(t *testing.T) {
	sigs, err := ParseZigDecls(
		"fn add(a: c_int, b: c_int) c_int;" +
			"fn sqrt(x: f64) f64;" +
			"fn hello(name: [*:0]const u8) void;" +
			"fn open(path: [*:0]const u8, out: **anyopaque, flags: ?*anyopaque) c_int;" +
			"fn location(event: *anyopaque) CGPoint;" +
			"var kCFBooleanTrue: *anyopaque;" +
			"var kCallbacks: anyopaque;")
	require.NoError(t, err)
	require.Len(t, sigs, 7)
	assert.Equal(t, CInt, sigs[0].Ret, "add sig wrong: %+v", sigs[0])
	require.Len(t, sigs[0].Params, 2, "add sig wrong: %+v", sigs[0])
	assert.Equal(t, CInt, sigs[0].Params[0].Type, "add sig wrong: %+v", sigs[0])
	assert.Equal(t, CDouble, sigs[1].Ret, "sqrt sig wrong: %+v", sigs[1])
	assert.Equal(t, CDouble, sigs[1].Params[0].Type, "sqrt sig wrong: %+v", sigs[1])
	assert.Equal(t, CVoid, sigs[2].Ret, "hello sig wrong: %+v", sigs[2])
	assert.Equal(t, CCharPtr, sigs[2].Params[0].Type, "hello sig wrong: %+v", sigs[2])
	assert.Equal(t, CVoidPtr, sigs[3].Params[1].Type, "open pointer params wrong: %+v", sigs[3])
	assert.Equal(t, CVoidPtr, sigs[3].Params[2].Type, "open pointer params wrong: %+v", sigs[3])
	assert.Equal(t, CPoint2D, sigs[4].Ret, "location sig wrong: %+v", sigs[4])
	assert.True(t, sigs[5].IsVar, "kCFBooleanTrue var wrong: %+v", sigs[5])
	assert.Equal(t, CVoidPtr, sigs[5].Ret, "kCFBooleanTrue var wrong: %+v", sigs[5])
	assert.True(t, sigs[6].IsVar, "kCallbacks address-of var wrong: %+v", sigs[6])
	assert.Equal(t, CAddr, sigs[6].Ret, "kCallbacks address-of var wrong: %+v", sigs[6])
}

func TestZigDeclSniffing(t *testing.T) {
	zig := "fn add(a: c_int) c_int;"
	c := "int add(int a);"
	cExtern := "extern void *kFoo;"
	zigVar := "var kFoo: *anyopaque;"
	for src, isZig := range map[string]bool{zig: true, c: false, cExtern: false, zigVar: true} {
		// parse through both entries to ensure each dialect accepts its own
		if isZig {
			_, err := ParseZigDecls(src)
			assert.NoErrorf(t, err, "zig dialect rejected %q", src)
		} else {
			_, err := ParseCDecls(src)
			assert.NoErrorf(t, err, "c dialect rejected %q", src)
		}
	}
}
