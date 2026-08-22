package std

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// covSourceFixture is a Buzz-implemented module carrying every declaration shape
// describeSource has to tell apart: documented and undocumented exports, a
// defaulted parameter, a void and an unannotated return, a private helper, and an
// extern the runtime binds.
const covSourceFixture = `
/// A stdlib module implemented in Buzz, used only by these tests.
namespace covsrc;

/// percent renders the covered-line percentage.
///
/// Trailing prose the one-line summary must drop.
export fun percent(lcov: str, keep: str) > str {
    return keep;
}

/// bump adds a step, defaulting when the caller omits it.
export fun bump(value: str, step: str = "1") > str {
    return value;
}

export fun quiet(name: str) > void {
}

export fun unannotated(name: str) {
}

/// hidden is this module's own helper, not its API.
fun hidden() > str {
    return "x";
}

/// host is bound by the runtime, so it declares no implementation.
export extern fun host(a: str) > str;
`

// TestDescribeSourceDerivesTheMethodList is the whole argument for a Buzz stdlib
// module carrying no hand-written descriptor: the name, doc and signature are read
// off the AST, so the two cannot disagree.
func TestDescribeSourceDerivesTheMethodList(t *testing.T) {
	got, err := describeSource(SourceModule{Name: "covsrc", Source: covSourceFixture})
	require.NoError(t, err)

	assert.Equal(t, []Method{
		{
			Name: "percent", BuzzName: "percent",
			Doc:     "percent renders the covered-line percentage.",
			Args:    []Arg{{Name: "lcov", Type: TypeAny, Object: "str"}, {Name: "keep", Type: TypeAny, Object: "str"}},
			Returns: []Ret{{Type: TypeAny, Object: "str"}},
		},
		{
			Name: "bump", BuzzName: "bump",
			Doc:     "bump adds a step, defaulting when the caller omits it.",
			Args:    []Arg{{Name: "value", Type: TypeAny, Object: "str"}, {Name: "step", Type: TypeAny, Optional: true, Object: "str"}},
			Returns: []Ret{{Type: TypeAny, Object: "str"}},
		},
		{
			Name: "quiet", BuzzName: "quiet",
			Args: []Arg{{Name: "name", Type: TypeAny, Object: "str"}},
		},
		{
			Name: "unannotated", BuzzName: "unannotated",
			Args: []Arg{{Name: "name", Type: TypeAny, Object: "str"}},
		},
	}, got,
		"a private fun and an extern contribute nothing; a void or unannotated return contributes no Ret")
}

func TestDescribeSourceReportsAParseFailure(t *testing.T) {
	_, err := describeSource(SourceModule{Name: "covsrc", Source: "export fun ("})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse:")
}

// TestRegisterSourcePanicsRatherThanStoringABadModule covers all three refusals.
// None reaches the registry, so the global source-module set is left as the
// package's own init built it.
func TestRegisterSourcePanicsRatherThanStoringABadModule(t *testing.T) {
	assert.PanicsWithValue(t, `std: "uuid" is already registered as a Go module`, func() {
		RegisterSource(SourceModule{Name: "uuid", Source: covSourceFixture})
	})

	assert.PanicsWithValue(t, `std: duplicate source module registration: "lcov"`, func() {
		RegisterSource(SourceModule{Name: "lcov", Source: covSourceFixture})
	})

	assert.Panics(t, func() {
		RegisterSource(SourceModule{Name: "zzz-cov-unparseable", Source: "export fun ("})
	}, "a source that does not parse is a programmer error, caught at init")

	_, ok := GetSource("zzz-cov-unparseable")
	assert.False(t, ok, "the rejected module must not be stored")
}

func TestSourceModuleRegistryReads(t *testing.T) {
	got, ok := GetSource("lcov")
	require.True(t, ok, "lcov registers from std/lib.go's init")
	assert.Equal(t, "lcov", got.Name)
	assert.NotEmpty(t, got.Source)

	_, ok = GetSource("zzz-cov-no-such-source-module")
	assert.False(t, ok)

	names := make([]string, 0, len(AllSource()))
	for _, m := range AllSource() {
		names = append(names, m.Name)
	}
	assert.Contains(t, names, "lcov")
}

func TestSourceModuleImportPath(t *testing.T) {
	assert.Equal(t, "coverage/lcov", SourceModule{Name: "lcov", Path: "coverage/lcov"}.ImportPath())
	assert.Equal(t, "lcov", SourceModule{Name: "lcov"}.ImportPath())
}

// TestSourceModulesAsModules covers the projection that lets a docs or manifest
// renderer walk both kinds of module through one code path. Impl is nil on every
// derived Method - the module IS Buzz, so there is no Go function to name.
func TestSourceModulesAsModules(t *testing.T) {
	var lcov Module
	for _, m := range SourceModulesAsModules() {
		if m.Name == "lcov" {
			lcov = m
			break
		}
	}
	require.Equal(t, "lcov", lcov.Name, "lcov must project into the Module shape")

	require.NotEmpty(t, lcov.Methods)
	names := make([]string, 0, len(lcov.Methods))
	for _, meth := range lcov.Methods {
		names = append(names, meth.Name)
		assert.Nilf(t, meth.Impl, "method %q must carry no Go Impl", meth.Name)
		assert.Equalf(t, meth.Name, meth.BuzzName, "method %q is called by the name its source writes", meth.Name)
	}
	assert.Contains(t, names, "percent")
}
