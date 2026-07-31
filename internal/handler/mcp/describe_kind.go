package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

type describeKindTool struct {
	ws  types.Describer
	cfg types.WorkspaceConfig
}

func (t *describeKindTool) Name() string { return "magus_describe" }

func (t *describeKindTool) Invoke(_ context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
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
		return spells.InvokeResponse{Data: spellReport(out)}, nil
	case "targets":
		if name != "" {
			return describeTargetByName(t.ws, name)
		}
		return spells.InvokeResponse{Data: t.ws.DescribeTargets()}, nil
	case "projects":
		out := t.ws.DescribeProjects()
		if name != "" {
			return describeProjectByPath(out, name)
		}
		return spells.InvokeResponse{Data: out}, nil
	case "workspaces":
		return spells.InvokeResponse{Data: t.ws.DescribeWorkspaces(t.cfg)}, nil
	case "mcp_tools":
		return spells.InvokeResponse{Data: DescribeTools()}, nil
	case "":
		return spells.InvokeResponse{}, errors.New("mcp: kind is required (one of: spells, targets, projects, workspaces, mcp_tools)")
	default:
		return spells.InvokeResponse{}, fmt.Errorf("mcp: unknown kind %q (one of: spells, targets, projects, workspaces, mcp_tools)", kind)
	}
}

// spellReport wraps the inventory in the wire envelope. Count is derived here, at
// the one place that serializes, rather than carried on the inventory itself - the
// narrowing below used to have to remember to set it.
func spellReport(entries []types.SpellEntry) types.SpellReport {
	return types.SpellReport{Definition: types.SpellDefinition, Count: len(entries), Spells: entries}
}

// describeSpellByName narrows the spell inventory to the single spell named name,
// returning a report of one so the wire shape matches the unfiltered list. An
// unknown name is a clear error naming every valid spell, so the agent can correct
// without a second list call.
func describeSpellByName(entries []types.SpellEntry, name string) (spells.InvokeResponse, error) {
	for _, s := range entries {
		if s.Name == name {
			return spells.InvokeResponse{Data: spellReport([]types.SpellEntry{s})}, nil
		}
	}
	names := make([]string, len(entries))
	for i, s := range entries {
		names[i] = s.Name
	}
	return spells.InvokeResponse{}, fmt.Errorf("mcp: no spell named %q (valid: %s)", name, strings.Join(names, ", "))
}

// describeProjectByPath narrows the project inventory to the single project at
// path, returning a ProjectsOutput of one. An unknown path is a clear error
// naming every valid project path.
func describeProjectByPath(out types.ProjectsOutput, path string) (spells.InvokeResponse, error) {
	for _, p := range out.Projects {
		if p.Path == path {
			out.Projects = []types.ProjectEntry{p}
			out.Count = 1
			return spells.InvokeResponse{Data: out}, nil
		}
	}
	paths := make([]string, len(out.Projects))
	for i, p := range out.Projects {
		paths[i] = p.Path
	}
	return spells.InvokeResponse{}, fmt.Errorf("mcp: no project at path %q (valid: %s)", path, strings.Join(paths, ", "))
}

// describeTargetByName evaluates one target into its full dispatch plan, mirroring
// the CLI `magus describe target <name> [project]`: name is the target (optionally
// with charms, e.g. "lint:rw") and an optional whitespace-separated second token
// scopes it to one project; without it every project is evaluated. An unknown
// project surfaces as DescribeTarget's own error.
func describeTargetByName(ws types.Describer, name string) (spells.InvokeResponse, error) {
	fields := strings.Fields(name)
	target, err := types.ParseTarget(fields[0])
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	if len(fields) > 1 {
		target.Path = fields[1]
	}
	out, err := ws.DescribeTarget(target)
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	return spells.InvokeResponse{Data: out}, nil
}

var _ spells.Driver = (*describeKindTool)(nil)

// describeFileTool classifies paths against the workspace's declared source and
// output globs - the read half of generated-file hygiene. Lives here with the
// other describe tool: one file per feature, and this is describe's file noun.
type describeFileTool struct {
	ws types.Describer
}

func (t *describeFileTool) Name() string { return ToolDescribeFile.String() }

func (t *describeFileTool) Invoke(_ context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	raw := paramString(req.Params, "paths", "")
	paths := strings.Fields(raw)
	if len(paths) == 0 {
		return spells.InvokeResponse{}, errors.New("mcp: paths is required (one or more workspace-relative paths, space-separated)")
	}
	files := t.ws.DescribeFiles(paths)
	return spells.InvokeResponse{Data: types.FileReport{Definition: types.FileDefinition, Count: len(files), Files: files}}, nil
}

var _ spells.Driver = (*describeFileTool)(nil)
