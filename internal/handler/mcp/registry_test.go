package mcp

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/std"
)

func TestValidateRegistryRejectsMalformedTools(t *testing.T) {
	tests := []struct {
		name string
		tool ToolDescriptor
		want string
	}{
		{"name is required", ToolDescriptor{Description: "d"}, "empty Name"},
		{"doc is required", ToolDescriptor{Name: "m_a"}, "empty description"},
		{
			"param type must have a schema shape",
			ToolDescriptor{Name: "m_a", Description: "d", Params: []ParamDescriptor{{Name: "p", Type: "unsupported"}}},
			"has no supported JSON schema shape",
		},
		{
			"param names are unique",
			ToolDescriptor{Name: "m_a", Description: "d", Params: []ParamDescriptor{{Name: "p", Type: schemaString}, {Name: "p", Type: schemaString}}},
			`param "p" declared twice`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRegistry([]ToolDescriptor{tc.tool})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	t.Run("duplicate tool names are refused", func(t *testing.T) {
		err := validateRegistry([]ToolDescriptor{{Name: "m_a", Description: "d"}, {Name: "m_a", Description: "d"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `mcp tool "m_a": declared twice`)
	})
}

func TestRegistry_NonEmpty(t *testing.T) {
	require.NotEmpty(t, Registry, "Registry is empty; every magus MCP deployment needs at least one tool")
}

func TestRegistry_AllToolsHaveNames(t *testing.T) {
	seen := map[string]bool{}
	for i, d := range Registry {
		assert.NotEmptyf(t, d.Name, "Registry[%d].Name", i)
		assert.Falsef(t, seen[d.Name], "Registry: duplicate tool name %q at index %d", d.Name, i)
		seen[d.Name] = true
	}
}

func TestRegistry_AllParamsHaveNames(t *testing.T) {
	for _, d := range Registry {
		for j, p := range d.Params {
			assert.NotEmptyf(t, p.Name, "Registry[%q].Params[%d].Name", d.Name, j)
			assert.NotEmptyf(t, p.Type, "Registry[%q].Params[%d].Type", d.Name, j)
		}
	}
}

// Catch missing drivers and missing descriptors before server startup.
func TestRegistry_EveryToolHasADriver(t *testing.T) {
	drivers := map[string]bool{}
	for _, d := range allToolDrivers(Options{Magus: fixtureMagus(t)}) {
		drivers[d.Name()] = true
	}

	described := map[string]bool{}
	for _, d := range Registry {
		described[d.Name] = true
		assert.Truef(t, drivers[d.Name],
			"tool %q is in the catalog with no SpellDriver behind it; add the driver to allToolDrivers", d.Name)
	}
	for name := range drivers {
		assert.Truef(t, described[name],
			"driver %q has no catalog entry; add its descriptor to Registry", name)
	}
}

// TestMCPToolNamesResolve holds the declared tool names and the catalog to each
// other in both directions. A Name() returning a string literal satisfies the
// driver interface and passes every check that only walks the constants.
func TestMCPToolNamesResolve(t *testing.T) {
	t.Parallel()

	registered := map[string]bool{}
	for _, d := range Registry {
		registered[d.Name] = true
	}
	declared := map[string]bool{}
	for _, tn := range hint.AllToolNames {
		declared[tn.String()] = true
		assert.Truef(t, registered[tn.String()], "tool constant %q is not a Registry[].Name", tn)
	}
	for _, d := range Registry {
		assert.Truef(t, declared[d.Name], "Registry tool %q is not a declared hint.ToolName", d.Name)
	}
}

// The instructions name magus\ members by their descriptor names, so each must be a
// member std.Magus declares; a renamed member fails here rather than in an agent.
func TestServerInstructionsNameRealMembers(t *testing.T) {
	t.Parallel()

	members := map[string]bool{}
	for _, m := range std.Magus.Methods {
		members[m.Name] = true
	}
	for _, ns := range std.Magus.Namespaces {
		members[ns.Name] = true
	}
	for _, root := range std.Magus.Methods {
		for _, ret := range root.Returns {
			for _, object := range std.Magus.Objects {
				if object.Name != ret.Object {
					continue
				}
				for _, method := range object.Methods {
					members[fmt.Sprintf("%s().%s", std.CamelCase(root.Name), std.BuzzMethodName(method))] = true
				}
			}
		}
	}
	for _, name := range clientMembers {
		assert.Truef(t, members[name], "the instructions name magus\\%s, which std.Magus does not declare", name)
	}
}

// A direct client call is bounded by hint.ClientCallBound whatever target_timeout
// says; the buzz tool keeps following target_timeout.
func TestClientTimeoutIsItsOwnBound(t *testing.T) {
	opts := Options{Magus: fixtureMagus(t)}
	opts.Config.TargetTimeout = time.Hour
	for _, d := range allToolDrivers(opts) {
		switch tool := d.(type) {
		case *clientTool:
			assert.Equal(t, hint.ClientCallBound, tool.timeout)
		case *buzzTool:
			assert.Equal(t, time.Hour, tool.timeout)
		}
	}
}

// Drivers read request parameters by name. Exclude the catalog itself so a
// declaration cannot supply the only occurrence of its own parameter name.
func TestRegistry_HandlersReadTheDeclaredParams(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	require.NoError(t, err)
	var sources []byte
	for _, e := range entries {
		name := e.Name()
		if name == "registry.go" || e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		require.NoError(t, err)
		sources = append(sources, b...)
	}
	for _, d := range Registry {
		for _, p := range d.Params {
			read := bytes.Contains(sources, []byte(fmt.Sprintf("\"%s\"", p.Name)))
			assert.Truef(t, read,
				"tool %q declares param %q but no handler in this package reads that name", d.Name, p.Name)
		}
	}
}
