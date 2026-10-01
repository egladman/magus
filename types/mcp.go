package types

// MCPToolDefinition is the human-readable description of what an MCP tool is.
const MCPToolDefinition = "An MCP tool is a function the magus server exposes to AI " +
	"agents via the Model Context Protocol. Agents call these tools to discover, " +
	"build, and diagnose the workspace without running shell commands. Start the " +
	"server with `magus server start` to enable MCP."

// MCPTool is one tool the magus MCP server exposes, as `magus describe mcp-tool`
// prints it and magus\describe.mcpTool returns it.
type MCPTool struct {
	Name        string         `json:"name"                  yaml:"name"`
	Description string         `json:"description,omitempty" yaml:"description,omitempty"`
	Params      []MCPToolParam `json:"params,omitempty"      yaml:"params,omitempty"`
}

// MCPToolParam is one parameter an MCP tool takes.
type MCPToolParam struct {
	Name        string `json:"name"                  yaml:"name"`
	Type        string `json:"type"                  yaml:"type"`
	Required    bool   `json:"required,omitempty"    yaml:"required,omitempty"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// MCPToolReport is the top-level result for `magus describe mcp-tools`.
type MCPToolReport struct {
	Definition string    `json:"definition" yaml:"definition"`
	Count      int       `json:"count"      yaml:"count"`
	MCPTools   []MCPTool `json:"mcp_tools"  yaml:"mcp_tools"`
}
