package describe

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/types"
)

// LsProject is the structured view of a project emitted under -o
// json|yaml. Empty slices/strings are omitted to keep the payload tight.
type LsProject struct {
	Path string `json:"path"                yaml:"path"`
	// Name is the DECLARED project name (magus.project's "name"), empty when the
	// project declares none. Without it the human label fell back to the checkout
	// directory, so `magus ls` printed a worktree's directory name where MAGUS.md,
	// generated from the same workspace, printed the declared one.
	Name  string `json:"name,omitempty"      yaml:"name,omitempty"`
	Dir   string `json:"dir"             yaml:"dir"`
	Spell string `json:"spell,omitempty" yaml:"spell,omitempty"`
	// Targets are the project's runnable target names, from the same graph
	// `magus ls targets` reads. Carried here so the default listing can answer
	// "what can I run" without a second command.
	Targets   []string `json:"targets,omitempty"    yaml:"targets,omitempty"`
	Sources   []string `json:"sources,omitempty"    yaml:"sources,omitempty"`
	Outputs   []string `json:"outputs,omitempty"    yaml:"outputs,omitempty"`
	DependsOn []string `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
}

// LsOutput is the `magus ls` record.
type LsOutput struct {
	Workspace string      `json:"workspace" yaml:"workspace"`
	Count     int         `json:"count"     yaml:"count"`
	Projects  []LsProject `json:"projects"  yaml:"projects"`
}

// LsTargetsProject is one project's runnable targets. Nodes are reused verbatim
// from the target graph rather than remapped into a parallel struct, so `magus ls
// targets -o json` and `magus describe graph -o json` describe a target the same
// way instead of drifting into two shapes for one thing.
type LsTargetsProject struct {
	Path    string                  `json:"path"            yaml:"path"`
	Name    string                  `json:"name,omitempty"  yaml:"name,omitempty"`
	Count   int                     `json:"count"           yaml:"count"`
	Targets []types.TargetGraphNode `json:"targets"         yaml:"targets"`
}

// LsTargetsOutput is the `magus ls targets` record.
type LsTargetsOutput struct {
	Workspace string             `json:"workspace" yaml:"workspace"`
	Count     int                `json:"count"     yaml:"count"`
	Projects  []LsTargetsProject `json:"projects"  yaml:"projects"`
}

// TargetSummary is the one-line right-hand column: the target's doc when it has
// one, else the spell ops its body drives. Most targets in practice carry no doc
// comment, and for those "which toolchain does this actually invoke" is the next
// most useful thing a reader can be told; falling back to it beats a blank column.
func TargetSummary(t types.TargetGraphNode) string {
	if t.Doc != "" {
		return t.Doc
	}
	var parts []string
	for _, s := range t.Spells {
		parts = append(parts, fmt.Sprintf("%s: %s", s.Spell, strings.Join(s.Ops, ", ")))
	}
	if len(parts) > 0 {
		return fmt.Sprintf("[%s]", strings.Join(parts, "; "))
	}
	if len(t.Dependencies) > 0 {
		return fmt.Sprintf("[needs: %s]", strings.Join(t.Dependencies, ", "))
	}
	return ""
}
