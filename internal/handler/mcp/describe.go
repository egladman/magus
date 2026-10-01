package mcp

import "github.com/egladman/magus/types"

// DescribeTools returns the catalog of MCP tools from the registry.
func DescribeTools() types.MCPToolReport {
	entries := make([]types.MCPTool, 0, len(Registry))
	for _, d := range Registry {
		var params []types.MCPToolParam
		for _, p := range d.Params {
			params = append(params, types.MCPToolParam(p))
		}
		entries = append(entries, types.MCPTool{
			Name:        d.Name,
			Description: d.Description,
			Params:      params,
		})
	}
	return types.MCPToolReport{
		Definition: types.MCPToolDefinition,
		Count:      len(entries),
		MCPTools:   entries,
	}
}
