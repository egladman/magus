package mcp

// MCPToolDefinition is the human-readable description of what an MCP tool is.
const MCPToolDefinition = "An MCP tool is a function the magus daemon exposes to AI " +
	"agents via the Model Context Protocol. Agents call these tools to discover, " +
	"build, and diagnose the workspace without running shell commands. Start the " +
	"daemon with `magus server start` to enable MCP."

// MCPToolEntry is the structured view of a single MCP tool.
type MCPToolEntry struct {
	Name        string            `json:"name"                  yaml:"name"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Params      []ParamDescriptor `json:"params,omitempty"      yaml:"params,omitempty"`
}

// MCPToolsOutput is the top-level result for "describe mcp-tools".
type MCPToolsOutput struct {
	Definition string         `json:"definition" yaml:"definition"`
	Count      int            `json:"count"      yaml:"count"`
	MCPTools   []MCPToolEntry `json:"mcp_tools"  yaml:"mcp_tools"`
}

// DescribeTools returns the catalog of MCP tools from the registry.
func DescribeTools() MCPToolsOutput {
	entries := make([]MCPToolEntry, 0, len(Registry))
	for _, d := range Registry {
		entries = append(entries, MCPToolEntry(d))
	}
	return MCPToolsOutput{
		Definition: MCPToolDefinition,
		Count:      len(entries),
		MCPTools:   entries,
	}
}
