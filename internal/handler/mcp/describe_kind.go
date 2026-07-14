package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/egladman/magus/types"
)

type describeKindTool struct {
	ws  types.Describer
	cfg types.WorkspaceConfig
}

func (t *describeKindTool) Name() string { return "magus_describe" }

func (t *describeKindTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	kind := paramString(req.Params, "kind", "")
	// name narrows a list into one entity's detail, mirroring the CLI's trailing
	// name positional (`magus describe <noun> <name>`). Only spells, targets, and
	// projects support it; the other kinds ignore it.
	name := strings.TrimSpace(paramString(req.Params, "name", ""))
	switch kind {
	case "spells":
		out := t.ws.DescribeSpells()
		if name != "" {
			return describeSpellByName(out, name)
		}
		return types.InvokeResponse{Data: out}, nil
	case "targets":
		if name != "" {
			return describeTargetByName(t.ws, name)
		}
		return types.InvokeResponse{Data: t.ws.DescribeTargets()}, nil
	case "projects":
		out := t.ws.DescribeProjects()
		if name != "" {
			return describeProjectByPath(out, name)
		}
		return types.InvokeResponse{Data: out}, nil
	case "workspaces":
		return types.InvokeResponse{Data: t.ws.DescribeWorkspaces(t.cfg)}, nil
	case "mcp_tools":
		return types.InvokeResponse{Data: DescribeTools()}, nil
	case "":
		return types.InvokeResponse{}, errors.New("mcp: kind is required (one of: spells, targets, projects, workspaces, mcp_tools)")
	default:
		return types.InvokeResponse{}, fmt.Errorf("mcp: unknown kind %q (one of: spells, targets, projects, workspaces, mcp_tools)", kind)
	}
}

// describeSpellByName narrows the spell inventory to the single spell named name,
// returning a SpellsOutput of one so the wire shape matches the unfiltered list.
// An unknown name is a clear error naming every valid spell, so the agent can
// correct without a second list call.
func describeSpellByName(out types.SpellsOutput, name string) (types.InvokeResponse, error) {
	for _, s := range out.Spells {
		if s.Name == name {
			out.Spells = []types.SpellEntry{s}
			out.Count = 1
			return types.InvokeResponse{Data: out}, nil
		}
	}
	names := make([]string, len(out.Spells))
	for i, s := range out.Spells {
		names[i] = s.Name
	}
	return types.InvokeResponse{}, fmt.Errorf("mcp: no spell named %q (valid: %s)", name, strings.Join(names, ", "))
}

// describeProjectByPath narrows the project inventory to the single project at
// path, returning a ProjectsOutput of one. An unknown path is a clear error
// naming every valid project path.
func describeProjectByPath(out types.ProjectsOutput, path string) (types.InvokeResponse, error) {
	for _, p := range out.Projects {
		if p.Path == path {
			out.Projects = []types.ProjectEntry{p}
			out.Count = 1
			return types.InvokeResponse{Data: out}, nil
		}
	}
	paths := make([]string, len(out.Projects))
	for i, p := range out.Projects {
		paths[i] = p.Path
	}
	return types.InvokeResponse{}, fmt.Errorf("mcp: no project at path %q (valid: %s)", path, strings.Join(paths, ", "))
}

// describeTargetByName evaluates one target into its full dispatch plan, mirroring
// the CLI `magus describe target <name> [project]`: name is the target (optionally
// with charms, e.g. "lint:rw") and an optional whitespace-separated second token
// scopes it to one project; without it every project is evaluated. An unknown
// project surfaces as DescribeTarget's own error.
func describeTargetByName(ws types.Describer, name string) (types.InvokeResponse, error) {
	fields := strings.Fields(name)
	target, err := types.ParseTarget(fields[0])
	if err != nil {
		return types.InvokeResponse{}, err
	}
	if len(fields) > 1 {
		target.Path = fields[1]
	}
	out, err := ws.DescribeTarget(target)
	if err != nil {
		return types.InvokeResponse{}, err
	}
	return types.InvokeResponse{Data: out}, nil
}

var _ types.SpellDriver = (*describeKindTool)(nil)

// describeFileTool classifies paths against the workspace's declared source and
// output globs - the read half of generated-file hygiene. Lives here with the
// other describe tool: one file per feature, and this is describe's file noun.
type describeFileTool struct {
	ws types.Describer
}

func (t *describeFileTool) Name() string { return ToolDescribeFile.String() }

func (t *describeFileTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	raw := paramString(req.Params, "paths", "")
	paths := strings.Fields(raw)
	if len(paths) == 0 {
		return types.InvokeResponse{}, errors.New("mcp: paths is required (one or more workspace-relative paths, space-separated)")
	}
	return types.InvokeResponse{Data: t.ws.DescribeFiles(paths)}, nil
}

var _ types.SpellDriver = (*describeFileTool)(nil)
