package vm_test

import (
	"testing"

	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// StdlibSuite shares a fresh env with the stdlib registered across all cases.
type StdlibSuite struct {
	suite.Suite
	e *vm.Env
}

func (s *StdlibSuite) SetupTest() {
	s.e = vm.NewEnv()
	vm.RegisterStdlib(s.e)
}

func TestStdlibSuite(t *testing.T) {
	suite.Run(t, new(StdlibSuite))
}

func (s *StdlibSuite) TestRegisterStdlibNoPanic() {
	// SetupTest already called RegisterStdlib without panicking.
	assert.NotPanics(s.T(), func() { vm.RegisterStdlib(vm.NewEnv()) })
}

func (s *StdlibSuite) TestRegisterStdlibPopulatesNames() {
	assert.NotEmpty(s.T(), s.e.Names(), "Names() is empty after RegisterStdlib, want non-empty")
}

func (s *StdlibSuite) TestRegisterStdlibKnownNames() {
	// zdef is the only VM-level intrinsic global.
	// resume/resolve are session-bound (registered in session.go, not stdlib).
	// All other stdlib functions (print, assert, toInt, …) live in
	// magus/buzz/std and require `import "std"` etc.
	for _, name := range []string{"zdef"} {
		_, ok := s.e.Get(name)
		assert.Truef(s.T(), ok, "stdlib name %q not found in env after RegisterStdlib", name)
	}
}

func (s *StdlibSuite) TestRegisterStdlibValuesAreDirect() {
	v, ok := s.e.Get("zdef")
	require.True(s.T(), ok, "'zdef' not found in env")
	assert.Truef(s.T(), v.IsDirect(), "stdlib 'zdef' value IsDirect() = false, want true (got kind %q)", v.Kind())
}

// TestRegisterStdlibLegacyGlobalsRemoved verifies that the old non-Buzz global
// functions (print, len, str, int, append, …) are NOT present after
// RegisterStdlib: they were removed as part of reconciling to Buzz's stdlib
// spec. These functions are now available via `import "std"` (buzz/std package).
func (s *StdlibSuite) TestRegisterStdlibLegacyGlobalsRemoved() {
	removed := []string{"print", "len", "str", "int", "double", "bool", "append", "type", "keys", "values", "range", "error", "assert"}
	for _, name := range removed {
		_, ok := s.e.Get(name)
		assert.Falsef(s.T(), ok, "legacy global %q still present after RegisterStdlib; it should have been removed (use `import \"std\"` instead)", name)
	}
}
