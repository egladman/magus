package spell_test

import (
	"sort"
	"strings"
	"testing"

	ispell "github.com/egladman/magus/internal/spell"
)

// mapObj is a test-only implementation of ispell.Obj backed by map[string]any.
type mapObj map[string]any

func (m mapObj) Str(key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func (m mapObj) Bool(key string) bool {
	v, ok := m[key]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

func (m mapObj) Strs(key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	ss, _ := v.([]string)
	return ss
}

func (m mapObj) Obj(key string) (ispell.Obj, bool) {
	v, ok := m[key]
	if !ok {
		return nil, false
	}
	sub, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	return mapObj(sub), true
}

func (m mapObj) Objs(key string) []ispell.Obj {
	v, ok := m[key]
	if !ok {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []ispell.Obj
	for _, it := range list {
		if sub, ok := it.(map[string]any); ok {
			out = append(out, mapObj(sub))
		}
	}
	return out
}

func (m mapObj) Keys() []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (m mapObj) CallStrs(key string, args ...string) ([]string, error) {
	v, ok := m[key]
	if !ok {
		return nil, nil
	}
	if ss, ok := v.([]string); ok {
		return ss, nil
	}
	if fn, ok := v.(func([]string) ([]string, error)); ok {
		return fn(args)
	}
	return nil, nil
}

// TestDecode_NoName ensures a missing name field returns an error.
func TestDecode_NoName(t *testing.T) {
	_, err := ispell.Decode(mapObj{})
	if err == nil {
		t.Fatal("Decode with no name: want error, got nil")
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Errorf("Decode with no name: error %q does not contain \"name is required\"", err.Error())
	}
}

// TestDecode_NameOnly checks that a spell with only a name and no ops is decoded correctly.
func TestDecode_NameOnly(t *testing.T) {
	src := mapObj{"name": "myspell"}
	m, err := ispell.Decode(src)
	if err != nil {
		t.Fatalf("Decode with name only: unexpected error: %v", err)
	}
	if m.Name != "myspell" {
		t.Errorf("Spec.Name = %q, want %q", m.Name, "myspell")
	}
	if m.Targets != nil {
		t.Errorf("Spec.Targets = %v, want nil", m.Targets)
	}
}

// TestDecode_ForkOp verifies a fork op (cmd and args, no fn) populates the Target correctly.
func TestDecode_ForkOp(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"build": map[string]any{
				"cmd":  "go",
				"args": []string{"build", "./..."},
			},
		},
	}
	m, err := ispell.Decode(src)
	if err != nil {
		t.Fatalf("Decode fork op: unexpected error: %v", err)
	}
	tgt, ok := m.Targets["build"]
	if !ok {
		t.Fatal("Targets[\"build\"] missing")
	}
	if tgt.Cmd != "go" {
		t.Errorf("Target.Cmd = %q, want \"go\"", tgt.Cmd)
	}
	if len(tgt.Args) != 2 || tgt.Args[0] != "build" || tgt.Args[1] != "./..." {
		t.Errorf("Target.Args = %v, want [build ./...]", tgt.Args)
	}
	if tgt.Func != "" {
		t.Errorf("Target.Func = %q, want empty (fork target)", tgt.Func)
	}
}

// TestDecode_CharmReplaceOp checks that a charm carrying a replace patch op is
// decoded into the canonical PatchOp.
func TestDecode_CharmReplaceOp(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"fmt": map[string]any{
				"cmd":  "gofmt",
				"args": []string{"-l", "."},
				"charms": map[string]any{
					"write": map[string]any{
						"ops": []any{
							map[string]any{"op": "replace", "path": "/0", "value": "-w"},
						},
					},
				},
			},
		},
	}
	m, err := ispell.Decode(src)
	if err != nil {
		t.Fatalf("Decode charm replace: unexpected error: %v", err)
	}
	tgt, ok := m.Targets["fmt"]
	if !ok {
		t.Fatal("Targets[\"fmt\"] missing")
	}
	charm, ok := tgt.Charms["write"]
	if !ok {
		t.Fatal("Charms[\"write\"] missing")
	}
	want := ispell.PatchOp{Op: "replace", Path: "/0", Value: "-w"}
	if len(charm.Ops) != 1 || charm.Ops[0] != want {
		t.Errorf("Charm.Ops = %v, want [%v]", charm.Ops, want)
	}
}

// TestDecode_CharmAddOp checks that a charm carrying an append patch op (add /-)
// is decoded into the canonical PatchOp.
func TestDecode_CharmAddOp(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"test": map[string]any{
				"cmd":  "go",
				"args": []string{"test", "./..."},
				"charms": map[string]any{
					"debug": map[string]any{
						"ops": []any{
							map[string]any{"op": "add", "path": "/-", "value": "-v"},
						},
					},
				},
			},
		},
	}
	m, err := ispell.Decode(src)
	if err != nil {
		t.Fatalf("Decode charm add: unexpected error: %v", err)
	}
	charm, ok := m.Targets["test"].Charms["debug"]
	if !ok {
		t.Fatal("Charms[\"debug\"] missing")
	}
	want := ispell.PatchOp{Op: "add", Path: "/-", Value: "-v"}
	if len(charm.Ops) != 1 || charm.Ops[0] != want {
		t.Errorf("Charm.Ops = %v, want [%v]", charm.Ops, want)
	}
}

// TestDecode_CharmRootRejected checks that a root-path op (whole-argv replace)
// is rejected — element-level only.
func TestDecode_CharmRootRejected(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"fmt": map[string]any{
				"cmd": "gofmt",
				"charms": map[string]any{
					"write": map[string]any{
						"ops": []any{
							map[string]any{"op": "replace", "path": "", "value": "x"},
						},
					},
				},
			},
		},
	}
	if _, err := ispell.Decode(src); err == nil {
		t.Fatal("Decode with root-path charm op: want error, got nil")
	}
}

// TestDecode_InvalidTargetName ensures ops with invalid names (e.g. containing spaces) are rejected.
func TestDecode_InvalidTargetName(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"has space": map[string]any{
				"cmd": "echo",
			},
		},
	}
	_, err := ispell.Decode(src)
	if err == nil {
		t.Fatal("Decode with invalid target name: want error, got nil")
	}
}

// TestDecode_NeedsResolved verifies that CallStrs("needs") is called and stored.
func TestDecode_NeedsResolved(t *testing.T) {
	src := mapObj{
		"name":  "myspell",
		"needs": []string{"**/*.go", "go.mod"},
	}
	m, err := ispell.Decode(src)
	if err != nil {
		t.Fatalf("Decode needs: unexpected error: %v", err)
	}
	if len(m.Needs) != 2 {
		t.Errorf("Spec.Needs = %v, want [**/*.go go.mod]", m.Needs)
	}
}
