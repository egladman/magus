package buzz

import (
	"context"
	"testing"
)

// These tests exercise the portable FFI surface — the C-decl parser and the
// FFIProvider boundary — without depending on the purego backend, so they run on
// every platform (including those where zdef() has no default provider).

func TestParseCDecls(t *testing.T) {
	sigs, err := ParseCDecls("double sqrt(double x); int abs(int v); void srand(unsigned seed);")
	if err != nil {
		t.Fatalf("ParseCDecls: %v", err)
	}
	if len(sigs) != 3 {
		t.Fatalf("got %d sigs, want 3", len(sigs))
	}
	if sigs[0].Name != "sqrt" || sigs[0].Ret != CDouble || len(sigs[0].Params) != 1 || sigs[0].Params[0].Type != CDouble {
		t.Errorf("sqrt sig wrong: %+v", sigs[0])
	}
	if sigs[1].Name != "abs" || sigs[1].Ret != CInt || sigs[1].Params[0].Type != CInt {
		t.Errorf("abs sig wrong: %+v", sigs[1])
	}
	if sigs[2].Name != "srand" || sigs[2].Ret != CVoid || sigs[2].Params[0].Type != CUint {
		t.Errorf("srand sig wrong: %+v", sigs[2])
	}
}

func TestParseCDeclsCharPtr(t *testing.T) {
	sigs, err := ParseCDecls("char* getenv(final char* name);")
	if err != nil {
		t.Fatalf("ParseCDecls: %v", err)
	}
	if sigs[0].Ret != CCharPtr || sigs[0].Params[0].Type != CCharPtr {
		t.Errorf("getenv sig wrong: %+v", sigs[0])
	}
}

// fakeFFI is a test FFIProvider: it binds every signature to a Go closure,
// proving an embedder can supply FFI on a platform the purego backend doesn't
// cover. It ignores the library and returns canned values per type.
type fakeFFI struct{ opened string }

func (f *fakeFFI) OpenLibrary(libname string, sigs []CFuncSig) (Value, error) {
	f.opened = libname
	m := NewMap()
	for _, sig := range sigs {
		sig := sig
		m.MapSet(sig.Name, DirectValue(sig.Name, func(_ context.Context, args []Value) (Value, error) {
			// Echo: return the first int arg doubled, or a marker string.
			if len(args) > 0 && args[0].IsInt() {
				return IntValue(args[0].AsInt() * 2), nil
			}
			return StrValue("called:" + sig.Name), nil
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
	if err := sess.Exec(context.Background(), src); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if fake.opened != "mylib" {
		t.Errorf("provider saw lib %q, want mylib", fake.opened)
	}
	if got := sess.GetGlobal("r"); !got.IsInt() || got.AsInt() != 42 {
		t.Fatalf("r = %s, want 42", got.String())
	}
}

func TestFFINoProvider(t *testing.T) {
	// With no provider installed, zdef() must return a clear error rather than
	// panic or silently no-op — this is the off-platform path.
	prev := GetFFIProvider()
	defer func() { SetFFIProvider(prev) }()
	SetFFIProvider(nil)

	sess := newSession(context.Background())
	err := sess.Exec(context.Background(), `final lib = zdef("libm", "double sqrt(double x);");`)
	if err == nil {
		t.Fatal("expected an error when no FFI provider is registered")
	}
}

func TestRegisterFFIProviderIgnoresNil(t *testing.T) {
	prev := GetFFIProvider()
	defer func() { SetFFIProvider(prev) }()
	RegisterFFIProvider(nil) // must not clobber an installed provider
	if GetFFIProvider() != prev {
		t.Fatal("RegisterFFIProvider(nil) overwrote the provider")
	}
}

func TestParseCDeclsExternVar(t *testing.T) {
	sigs, err := ParseCDecls(
		"extern void *kBoolTrue;" +
			"extern const char *greeting;" +
			"extern int answer;" +
			"extern struct Callbacks kCallbacks;" +
			"double sqrt(double x);")
	if err != nil {
		t.Fatalf("ParseCDecls: %v", err)
	}
	if len(sigs) != 5 {
		t.Fatalf("got %d sigs, want 5", len(sigs))
	}
	if !sigs[0].IsVar || sigs[0].Name != "kBoolTrue" || sigs[0].Ret != CVoidPtr {
		t.Errorf("kBoolTrue sig wrong: %+v", sigs[0])
	}
	if !sigs[1].IsVar || sigs[1].Name != "greeting" || sigs[1].Ret != CCharPtr {
		t.Errorf("greeting sig wrong: %+v", sigs[1])
	}
	if !sigs[2].IsVar || sigs[2].Name != "answer" || sigs[2].Ret != CInt || sigs[2].VarTypeName != "int" {
		t.Errorf("answer sig wrong: %+v", sigs[2])
	}
	if !sigs[3].IsVar || sigs[3].Name != "kCallbacks" || sigs[3].Ret != CAddr {
		t.Errorf("kCallbacks sig wrong: %+v", sigs[3])
	}
	if sigs[4].IsVar || sigs[4].Name != "sqrt" {
		t.Errorf("sqrt sig wrong: %+v", sigs[4])
	}
}

func TestParseCDeclsExternVarErrors(t *testing.T) {
	if _, err := ParseCDecls("extern int;"); err == nil {
		t.Error("extern without a name should fail")
	}
	if _, err := ParseCDecls("int loose;"); err == nil {
		t.Error("a variable without the extern keyword should fail")
	}
}

func TestParseCDeclsPoint2D(t *testing.T) {
	sigs, err := ParseCDecls("CGPoint CGEventGetLocation(void *event); NSPoint mouseLocation(void);")
	if err != nil {
		t.Fatalf("ParseCDecls: %v", err)
	}
	if sigs[0].Ret != CPoint2D || sigs[1].Ret != CPoint2D {
		t.Errorf("point returns wrong: %+v", sigs)
	}
	if _, err := ParseCDecls("void use(CGPoint p);"); err == nil {
		t.Error("by-value struct parameter should be rejected with advice")
	}
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
	if err != nil {
		t.Fatalf("ParseZigDecls: %v", err)
	}
	if len(sigs) != 7 {
		t.Fatalf("got %d sigs, want 7", len(sigs))
	}
	if sigs[0].Ret != CInt || len(sigs[0].Params) != 2 || sigs[0].Params[0].Type != CInt {
		t.Errorf("add sig wrong: %+v", sigs[0])
	}
	if sigs[1].Ret != CDouble || sigs[1].Params[0].Type != CDouble {
		t.Errorf("sqrt sig wrong: %+v", sigs[1])
	}
	if sigs[2].Ret != CVoid || sigs[2].Params[0].Type != CCharPtr {
		t.Errorf("hello sig wrong: %+v", sigs[2])
	}
	if sigs[3].Params[1].Type != CVoidPtr || sigs[3].Params[2].Type != CVoidPtr {
		t.Errorf("open pointer params wrong: %+v", sigs[3])
	}
	if sigs[4].Ret != CPoint2D {
		t.Errorf("location sig wrong: %+v", sigs[4])
	}
	if !sigs[5].IsVar || sigs[5].Ret != CVoidPtr {
		t.Errorf("kCFBooleanTrue var wrong: %+v", sigs[5])
	}
	if !sigs[6].IsVar || sigs[6].Ret != CAddr {
		t.Errorf("kCallbacks address-of var wrong: %+v", sigs[6])
	}
}

func TestZigDeclSniffing(t *testing.T) {
	zig := "fn add(a: c_int) c_int;"
	c := "int add(int a);"
	cExtern := "extern void *kFoo;"
	zigVar := "var kFoo: *anyopaque;"
	for src, sigs := range map[string]bool{zig: true, c: false, cExtern: false, zigVar: true} {
		// parse through both entries to ensure each dialect accepts its own
		if sigs {
			if _, err := ParseZigDecls(src); err != nil {
				t.Errorf("zig dialect rejected %q: %v", src, err)
			}
		} else {
			if _, err := ParseCDecls(src); err != nil {
				t.Errorf("c dialect rejected %q: %v", src, err)
			}
		}
	}
}
