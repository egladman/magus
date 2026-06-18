package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildModulesOutput_List covers the no-name list view: every module is
// present with a doc, and methods are NOT expanded (that's the detail view).
func TestBuildModulesOutput_List(t *testing.T) {
	out := buildModulesOutput("")
	require.NotZero(t, out.Count)
	assert.Equal(t, len(out.Modules), out.Count)
	var sawEnv bool
	for _, m := range out.Modules {
		if m.Name == "env" {
			sawEnv = true
		}
		assert.Empty(t, m.Methods, "list view expanded %q methods; detail-only", m.Name)
		assert.Empty(t, m.Fields, "list view expanded %q fields; detail-only", m.Name)
	}
	assert.True(t, sawEnv, "expected the env module in the list")
}

// TestBuildModulesOutput_Detail covers the named detail view: methods are
// expanded with both engine signatures, and the native-Buzz cross-reference is
// surfaced for overlap entries (env.get/lookup).
func TestBuildModulesOutput_Detail(t *testing.T) {
	out := buildModulesOutput("env")
	require.Equal(t, 1, out.Count)
	require.Equal(t, "env", out.Modules[0].Name)
	byName := map[string]struct{ buzz, native string }{}
	for _, m := range out.Modules[0].Methods {
		byName[m.Name] = struct{ buzz, native string }{m.Buzz, m.NativeBuzz}
	}
	lk, ok := byName["lookup"]
	require.True(t, ok, "env.lookup missing from detail view")
	assert.True(t, strings.HasPrefix(lk.buzz, "env.lookup("), "Buzz sig = %q, want env.lookup(...)", lk.buzz)
	assert.NotEmpty(t, lk.native, "env.lookup should carry a native Buzz cross-reference")
}

// TestBuildModulesOutput_Unknown: an unknown name yields an empty result so the
// command can report it (rather than silently listing all).
func TestBuildModulesOutput_Unknown(t *testing.T) {
	out := buildModulesOutput("definitely-not-a-module")
	assert.Empty(t, out.Modules)
}

// TestDescribeAlias pins that singular and plural resolve to the same canonical
// noun (kubectl-style interchangeability).
func TestDescribeAlias(t *testing.T) {
	pairs := [][2]string{
		{"module", "modules"},
		{"project", "projects"},
		{"spell", "spells"},
		{"target", "targets"},
		{"workspace", "workspaces"},
		{"mcp-tool", "mcp-tools"},
	}
	for _, p := range pairs {
		assert.Equal(t, describeAlias[p[0]], describeAlias[p[1]], "%q and %q resolve differently", p[0], p[1])
		assert.Equal(t, p[0], describeAlias[p[0]], "canonical of %q should be itself", p[0])
	}
}
