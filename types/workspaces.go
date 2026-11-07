package types

// WorkspacesDir, under a workspace root, holds one directory symlink per other
// workspace knowledge.workspaces lists, named by its entry. It lives under the
// ignored .magus/ and is never committed.
const WorkspacesDir = ".magus/workspaces"

// ListedWorkspace is one knowledge.workspaces entry as the load resolved it.
type ListedWorkspace struct {
	// Name is the entry's name, the one label every row and path uses.
	Name string `json:"name" yaml:"name"`
	// Root is the entry's absolute root.
	Root string `json:"root" yaml:"root"`
	// Dir is the workspace-relative slash path this workspace reaches Root at,
	// WorkspacesDir/<Name>. It is empty for the workspace's own entry.
	Dir string `json:"dir" yaml:"dir"`
}
