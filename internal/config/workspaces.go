package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/egladman/magus/types"
)

// KnowledgeWorkspace is one knowledge.workspaces entry, {name, path}.
type KnowledgeWorkspace struct {
	// Name is [a-z0-9-]+ and unique in the list. Every label and the
	// .magus/workspaces/<name> path use it, never a directory basename.
	Name string `json:"name" yaml:"name"`
	// Path is the workspace's root, absolute or starting with ~.
	Path string `json:"path" yaml:"path"`
	// File and Line place the entry, so a refusal names the line to change.
	File string `json:"-" yaml:"-"`
	Line int    `json:"-" yaml:"-"`
}

// UnmarshalYAML refuses a bare string, the list's old shape, and any key but
// name and path: a node's own Decode would skip the file's unknown-key check.
func (e *KnowledgeWorkspace) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return types.DiagnosticErrorf(types.WorkspaceEntryShape,
			"line %d: knowledge.workspaces entry %q is a bare value; write it as {name: <name>, path: %s}",
			n.Line, n.Value, n.Value)
	}
	*e = KnowledgeWorkspace{Line: n.Line}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		switch k.Value {
		case "name":
			e.Name = v.Value
		case "path":
			e.Path = v.Value
		default:
			return types.DiagnosticErrorf(types.WorkspaceEntryShape,
				"line %d: knowledge.workspaces entry has the key %q; an entry takes only name and path",
				k.Line, k.Value)
		}
	}
	return nil
}

// Where is the entry's file:line, or the file alone when no line is known.
func (e KnowledgeWorkspace) Where() string {
	if e.Line == 0 {
		return e.File
	}
	return fmt.Sprintf("%s:%d", e.File, e.Line)
}

// Root is Path with a leading ~ expanded and cleaned.
func (e KnowledgeWorkspace) Root() (string, error) {
	p := e.Path
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("knowledge.workspaces entry %s: expand ~: %w", e.Name, err)
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return filepath.Clean(p), nil
}

var workspaceName = regexp.MustCompile(`^[a-z0-9-]+$`)

// workspacesShapeError refuses the first entry with a bad or repeated name or
// a relative path. A relative path would mean something different per cwd.
func workspacesShapeError(entries []KnowledgeWorkspace) error {
	seen := map[string]string{}
	for _, e := range entries {
		switch {
		case !workspaceName.MatchString(e.Name):
			return types.DiagnosticErrorf(types.WorkspaceEntryShape,
				"%s: knowledge.workspaces entry name %q must be [a-z0-9-]+", e.Where(), e.Name)
		case seen[e.Name] != "":
			return types.DiagnosticErrorf(types.WorkspaceEntryShape,
				"%s: knowledge.workspaces names %q twice, first at %s; give each entry its own name",
				e.Where(), e.Name, seen[e.Name])
		case !filepath.IsAbs(e.Path) && e.Path != "~" && !strings.HasPrefix(e.Path, "~/"):
			return types.DiagnosticErrorf(types.WorkspaceEntryShape,
				"%s: knowledge.workspaces entry %s has the path %q; write an absolute path or one starting with ~",
				e.Where(), e.Name, e.Path)
		}
		seen[e.Name] = e.Where()
	}
	return nil
}

// workspacesCommittedError refuses knowledge.workspaces in a repository tier.
func workspacesCommittedError(entries []KnowledgeWorkspace) error {
	if len(entries) == 0 {
		return nil
	}
	return types.DiagnosticErrorf(types.WorkspacesCommitted,
		"knowledge.workspaces is personal: move it from %s to $XDG_CONFIG_HOME/magus/%s",
		entries[0].Where(), Filename)
}
