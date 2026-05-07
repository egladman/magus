package vm_test

import (
	"testing"

	"github.com/egladman/gopherbuzz/vm"
)

func TestGetFFIProviderNoPanic(t *testing.T) {
	// GetFFIProvider may return nil on unsupported platforms; it must not panic.
	_ = vm.GetFFIProvider()
}

func TestSetGetFFIProviderRoundTrip(t *testing.T) {
	// Save whatever is currently installed.
	original := vm.GetFFIProvider()
	t.Cleanup(func() { vm.SetFFIProvider(original) })

	// Install a mock provider and verify it is returned by GetFFIProvider.
	mock := &mockFFIProvider{}
	vm.SetFFIProvider(mock)
	got := vm.GetFFIProvider()
	if got != mock {
		t.Errorf("GetFFIProvider() after SetFFIProvider = %v, want mock", got)
	}
}

func TestSetFFIProviderNilClears(t *testing.T) {
	original := vm.GetFFIProvider()
	t.Cleanup(func() { vm.SetFFIProvider(original) })

	vm.SetFFIProvider(nil)
	if got := vm.GetFFIProvider(); got != nil {
		t.Errorf("GetFFIProvider() after SetFFIProvider(nil) = %v, want nil", got)
	}
}

func TestRegisterFFIProviderIgnoresNil(t *testing.T) {
	original := vm.GetFFIProvider()
	t.Cleanup(func() { vm.SetFFIProvider(original) })

	// Set a known provider, then call RegisterFFIProvider with nil; the known
	// provider should remain.
	mock := &mockFFIProvider{}
	vm.SetFFIProvider(mock)
	vm.RegisterFFIProvider(nil)
	if got := vm.GetFFIProvider(); got != mock {
		t.Errorf("GetFFIProvider() after RegisterFFIProvider(nil) = %v, want mock", got)
	}
}

func TestParseCDeclsEmptyInput(t *testing.T) {
	sigs, err := vm.ParseCDecls("")
	if err != nil {
		t.Fatalf("ParseCDecls('') error = %v, want nil", err)
	}
	if len(sigs) != 0 {
		t.Errorf("ParseCDecls('') len = %d, want 0", len(sigs))
	}
}

func TestParseCDeclsWhitespaceOnly(t *testing.T) {
	sigs, err := vm.ParseCDecls("   \n\t  ")
	if err != nil {
		t.Fatalf("ParseCDecls(whitespace) error = %v, want nil", err)
	}
	if len(sigs) != 0 {
		t.Errorf("ParseCDecls(whitespace) len = %d, want 0", len(sigs))
	}
}

func TestParseCDeclsSimpleVoidNoParams(t *testing.T) {
	sigs, err := vm.ParseCDecls("void foo(void);")
	if err != nil {
		t.Fatalf("ParseCDecls error = %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("ParseCDecls len = %d, want 1", len(sigs))
	}
	sig := sigs[0]
	if sig.Name != "foo" {
		t.Errorf("sig.Name = %q, want 'foo'", sig.Name)
	}
	if sig.Ret != vm.CVoid {
		t.Errorf("sig.Ret = %v, want CVoid", sig.Ret)
	}
	if len(sig.Params) != 0 {
		t.Errorf("sig.Params len = %d, want 0", len(sig.Params))
	}
}

func TestParseCDeclsIntParam(t *testing.T) {
	sigs, err := vm.ParseCDecls("int add(int a, int b);")
	if err != nil {
		t.Fatalf("ParseCDecls error = %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("ParseCDecls len = %d, want 1", len(sigs))
	}
	sig := sigs[0]
	if sig.Name != "add" {
		t.Errorf("sig.Name = %q, want 'add'", sig.Name)
	}
	if sig.Ret != vm.CInt {
		t.Errorf("sig.Ret = %v, want CInt", sig.Ret)
	}
	if len(sig.Params) != 2 {
		t.Fatalf("sig.Params len = %d, want 2", len(sig.Params))
	}
	if sig.Params[0].Type != vm.CInt {
		t.Errorf("sig.Params[0].Type = %v, want CInt", sig.Params[0].Type)
	}
}

func TestParseCDeclsMultipleDecls(t *testing.T) {
	sigs, err := vm.ParseCDecls("void foo(void); int bar(int x);")
	if err != nil {
		t.Fatalf("ParseCDecls error = %v", err)
	}
	if len(sigs) != 2 {
		t.Errorf("ParseCDecls len = %d, want 2", len(sigs))
	}
}

func TestParseCDeclsDoubleReturn(t *testing.T) {
	sigs, err := vm.ParseCDecls("double sqrt(double x);")
	if err != nil {
		t.Fatalf("ParseCDecls error = %v", err)
	}
	if len(sigs) != 1 {
		t.Fatalf("ParseCDecls len = %d, want 1", len(sigs))
	}
	if sigs[0].Ret != vm.CDouble {
		t.Errorf("sig.Ret = %v, want CDouble", sigs[0].Ret)
	}
}

// mockFFIProvider satisfies the FFIProvider interface for testing.
type mockFFIProvider struct{}

func (m *mockFFIProvider) OpenLibrary(libname string, sigs []vm.CFuncSig) (vm.Value, error) {
	return vm.NewMap(), nil
}
