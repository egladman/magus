package mcp

import (
	mcpgen "github.com/egladman/magus/internal/handler/mcp/gen"
)

// The tool catalog is GENERATED from the std module descriptors, the same source
// the runtime bindings, the checker declarations and the reference docs come from.
// Add a tool by declaring an MCPTool on the module (std/magus.go), not here.
//
//go:generate go run ../../../cmd/magus-utils mcptools -out gen/registry.go -pkg mcpgen

// ParamDescriptor describes a single parameter on an MCP tool.
type ParamDescriptor = mcpgen.ParamDescriptor

// ToolDescriptor is a static description of one MCP tool, used both to
// register the tool with the server (registerTools in mcp.go) and to populate
// "magus describe mcp-tools" output. There is no build tag gating either use;
// the package always compiles in.
type ToolDescriptor = mcpgen.ToolDescriptor

// Registry is the canonical list of MCP tools the magus server exposes.
// registerTools pairs each entry with the SpellDriver of the same name and
// panics in either direction when one has no counterpart.
var Registry = mcpgen.Registry
