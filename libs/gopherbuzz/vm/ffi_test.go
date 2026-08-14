package vm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetFFIProviderNoPanic(t *testing.T) {
	// GetFFIProvider may return nil on unsupported platforms; it must not panic.
	assert.NotPanics(t, func() { _ = GetFFIProvider() })
}

func TestSetGetFFIProviderRoundTrip(t *testing.T) {
	// Save whatever is currently installed.
	original := GetFFIProvider()
	t.Cleanup(func() { SetFFIProvider(original) })

	// Install a mock provider and verify it is returned by GetFFIProvider.
	mock := &mockFFIProvider{}
	SetFFIProvider(mock)
	assert.Equal(t, mock, GetFFIProvider(), "GetFFIProvider() after SetFFIProvider")
}

func TestSetFFIProviderNilClears(t *testing.T) {
	original := GetFFIProvider()
	t.Cleanup(func() { SetFFIProvider(original) })

	SetFFIProvider(nil)
	assert.Nil(t, GetFFIProvider(), "GetFFIProvider() after SetFFIProvider(nil)")
}

func TestRegisterFFIProviderIgnoresNil(t *testing.T) {
	original := GetFFIProvider()
	t.Cleanup(func() { SetFFIProvider(original) })

	// Set a known provider, then call RegisterFFIProvider with nil; the known
	// provider should remain.
	mock := &mockFFIProvider{}
	SetFFIProvider(mock)
	RegisterFFIProvider(nil)
	assert.Equal(t, mock, GetFFIProvider(), "GetFFIProvider() after RegisterFFIProvider(nil)")
}

func TestParseCDeclsEmptyInput(t *testing.T) {
	sigs, err := ParseCDecls("")
	require.NoError(t, err, "ParseCDecls('')")
	assert.Empty(t, sigs, "ParseCDecls('')")
}

func TestParseCDeclsWhitespaceOnly(t *testing.T) {
	sigs, err := ParseCDecls("   \n\t  ")
	require.NoError(t, err, "ParseCDecls(whitespace)")
	assert.Empty(t, sigs, "ParseCDecls(whitespace)")
}

func TestParseCDeclsSimpleVoidNoParams(t *testing.T) {
	sigs, err := ParseCDecls("void foo(void);")
	require.NoError(t, err, "ParseCDecls")
	require.Len(t, sigs, 1, "ParseCDecls")
	sig := sigs[0]
	assert.Equal(t, "foo", sig.Name)
	assert.Equal(t, CVoid, sig.Ret)
	assert.Empty(t, sig.Params)
}

func TestParseCDeclsIntParam(t *testing.T) {
	sigs, err := ParseCDecls("int add(int a, int b);")
	require.NoError(t, err, "ParseCDecls")
	require.Len(t, sigs, 1, "ParseCDecls")
	sig := sigs[0]
	assert.Equal(t, "add", sig.Name)
	assert.Equal(t, CInt, sig.Ret)
	require.Len(t, sig.Params, 2)
	assert.Equal(t, CInt, sig.Params[0].Type)
}

func TestParseCDeclsMultipleDecls(t *testing.T) {
	sigs, err := ParseCDecls("void foo(void); int bar(int x);")
	require.NoError(t, err, "ParseCDecls")
	assert.Len(t, sigs, 2, "ParseCDecls")
}

func TestParseCDeclsDoubleReturn(t *testing.T) {
	sigs, err := ParseCDecls("double sqrt(double x);")
	require.NoError(t, err, "ParseCDecls")
	require.Len(t, sigs, 1, "ParseCDecls")
	assert.Equal(t, CDouble, sigs[0].Ret)
}

// mockFFIProvider satisfies the FFIProvider interface for testing.
type mockFFIProvider struct{}

func (m *mockFFIProvider) OpenLibrary(libname string, sigs []CFuncSig) (Value, error) {
	return NewMap(), nil
}
