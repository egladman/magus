package spell

import (
	"sort"
	"testing"

	"github.com/egladman/magus/types"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func (m mapObj) Obj(key string) (Obj, bool) {
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

func (m mapObj) Objs(key string) []Obj {
	v, ok := m[key]
	if !ok {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []Obj
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
	_, err := Decode(mapObj{})
	require.Error(t, err, "Decode with no name: want error, got nil")
	assert.Contains(t, err.Error(), "name is required")
}

// TestDecode_NameOnly checks that a spell with only a name and no ops is decoded correctly.
func TestDecode_NameOnly(t *testing.T) {
	src := mapObj{"name": "myspell"}
	m, err := Decode(src)
	require.NoError(t, err)
	assert.Equal(t, "myspell", m.Name)
	assert.Nil(t, m.Ops)
}

// TestDecode_CommandOp verifies a fork op (cmd and args, no fn) populates the Target correctly.
func TestDecode_CommandOp(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"build": map[string]any{
				"bin":  "go",
				"args": []string{"build", "./..."},
			},
		},
	}
	m, err := Decode(src)
	require.NoError(t, err)
	tgt, ok := m.Ops["build"]
	require.True(t, ok, `Targets["build"] missing`)
	assert.Equal(t, "go", tgt.Bin)
	assert.Equal(t, []string{"build", "./..."}, tgt.Args)
}

// TestDecode_CharmReplaceOp checks that a charm carrying a replace patch op is
// decoded into the canonical types.PatchOp.
func TestDecode_CharmReplaceOp(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"fmt": map[string]any{
				"bin":  "gofmt",
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
	m, err := Decode(src)
	require.NoError(t, err)
	tgt, ok := m.Ops["fmt"]
	require.True(t, ok, `Targets["fmt"] missing`)
	charm, ok := tgt.Charms["write"]
	require.True(t, ok, `Charms["write"] missing`)
	assert.Equal(t, []types.PatchOp{{Op: "replace", Path: "/0", Value: "-w"}}, charm.Ops)
}

// TestDecode_CharmAddOp checks that a charm carrying an append patch op (add /-)
// is decoded into the canonical types.PatchOp.
func TestDecode_CharmAddOp(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"test": map[string]any{
				"bin":  "go",
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
	m, err := Decode(src)
	require.NoError(t, err)
	charm, ok := m.Ops["test"].Charms["debug"]
	require.True(t, ok, `Charms["debug"] missing`)
	assert.Equal(t, []types.PatchOp{{Op: "add", Path: "/-", Value: "-v"}}, charm.Ops)
}

// TestDecode_CharmRootRejected checks that a root-path op (whole-argv replace)
// is rejected — element-level only.
func TestDecode_CharmRootRejected(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"fmt": map[string]any{
				"bin": "gofmt",
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
	_, err := Decode(src)
	assert.Error(t, err, "Decode with root-path charm op: want error, got nil")
}

// TestDecode_InvalidTargetName ensures ops with invalid names (e.g. containing spaces) are rejected.
func TestDecode_InvalidTargetName(t *testing.T) {
	src := mapObj{
		"name": "myspell",
		"ops": map[string]any{
			"has space": map[string]any{
				"bin": "echo",
			},
		},
	}
	_, err := Decode(src)
	assert.Error(t, err, "Decode with invalid target name: want error, got nil")
}

// TestDecode_NeedsResolved verifies that CallStrs("needs") is called and stored.
func TestDecode_NeedsResolved(t *testing.T) {
	src := mapObj{
		"name":  "myspell",
		"needs": []string{"**/*.go", "go.mod"},
	}
	m, err := Decode(src)
	require.NoError(t, err)
	assert.Equal(t, []string{"**/*.go", "go.mod"}, m.Needs)
}
