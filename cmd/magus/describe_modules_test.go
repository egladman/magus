package main

import (
	"strings"
	"testing"
)

// TestBuildModulesOutput_List covers the no-name list view: every module is
// present with a doc, and methods are NOT expanded (that's the detail view).
func TestBuildModulesOutput_List(t *testing.T) {
	out := buildModulesOutput("")
	if out.Count == 0 || out.Count != len(out.Modules) {
		t.Fatalf("Count=%d, len(Modules)=%d", out.Count, len(out.Modules))
	}
	var sawEnv bool
	for _, m := range out.Modules {
		if m.Name == "env" {
			sawEnv = true
		}
		if len(m.Methods) != 0 || len(m.Fields) != 0 {
			t.Errorf("list view expanded %q (methods=%d fields=%d); detail-only", m.Name, len(m.Methods), len(m.Fields))
		}
	}
	if !sawEnv {
		t.Error("expected the env module in the list")
	}
}

// TestBuildModulesOutput_Detail covers the named detail view: methods are
// expanded with both engine signatures, and the native-Buzz cross-reference is
// surfaced for overlap entries (env.get/lookup).
func TestBuildModulesOutput_Detail(t *testing.T) {
	out := buildModulesOutput("env")
	if out.Count != 1 || out.Modules[0].Name != "env" {
		t.Fatalf("want single env module, got %+v", out.Modules)
	}
	byName := map[string]struct{ teal, buzz, native string }{}
	for _, m := range out.Modules[0].Methods {
		byName[m.Name] = struct{ teal, buzz, native string }{m.Teal, m.Buzz, m.NativeBuzz}
	}
	lk, ok := byName["lookup"]
	if !ok {
		t.Fatal("env.lookup missing from detail view")
	}
	if !strings.HasPrefix(lk.teal, "env.lookup(") {
		t.Errorf("Teal sig = %q, want env.lookup(...)", lk.teal)
	}
	if !strings.HasPrefix(lk.buzz, "extra.env.lookup(") {
		t.Errorf("Buzz sig = %q, want extra.env.lookup(...)", lk.buzz)
	}
	if lk.native == "" {
		t.Error("env.lookup should carry a native Buzz cross-reference")
	}
}

// TestBuildModulesOutput_Unknown: an unknown name yields an empty result so the
// command can report it (rather than silently listing all).
func TestBuildModulesOutput_Unknown(t *testing.T) {
	out := buildModulesOutput("definitely-not-a-module")
	if len(out.Modules) != 0 {
		t.Errorf("unknown module returned %d entries, want 0", len(out.Modules))
	}
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
		if describeAlias[p[0]] != describeAlias[p[1]] {
			t.Errorf("%q and %q resolve differently (%q vs %q)", p[0], p[1], describeAlias[p[0]], describeAlias[p[1]])
		}
		if describeAlias[p[0]] != p[0] {
			t.Errorf("canonical of %q should be itself, got %q", p[0], describeAlias[p[0]])
		}
	}
}
