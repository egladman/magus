package describe

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
)

// RenderGraph writes the text form of the target graph: each project's targets and their dependencies.
func RenderGraph(w io.Writer, out types.TargetGraphOutput) {
	fmt.Fprintf(w, "definition: %s\n\n", out.Definition)
	for _, p := range out.Projects {
		if p.Engine != "buzz" {
			fmt.Fprintf(w, "project: %s  (engine %s: graph extraction not yet supported)\n\n", p.Label(), p.Engine)
			continue
		}
		fmt.Fprintf(w, "project: %s  (%d targets)\n", p.Label(), len(p.Nodes))
		if len(p.Cycle) > 0 {
			fmt.Fprintf(w, "  CYCLE: %s\n", strings.Join(p.Cycle, " -> "))
		}
		for _, n := range p.Nodes {
			if len(n.Dependencies) > 0 {
				fmt.Fprintf(w, "  %s -> %s\n", n.Name, strings.Join(n.Dependencies, ", "))
			} else {
				fmt.Fprintf(w, "  %s\n", n.Name)
			}
		}
		fmt.Fprintln(w)
	}
}

// RenderSpells writes the text form of the spell inventory. printVersions writes a spell's probed
// versions; the caller supplies it because internal/spell imports this package.
func RenderSpells(w io.Writer, inventory []types.Spell, printVersions func(io.Writer, []types.SpellVersion)) {
	fmt.Fprintf(w, "definition: %s\n\n", types.SpellDefinition)
	fmt.Fprintf(w, "spells (%d):\n", len(inventory))
	for _, t := range inventory {
		// A workspace-local spell is marked, because the two are not
		// interchangeable and the listing used to imply they were: only a built-in
		// is reachable as `import "magus/spell/<name>"` from anywhere and appears in
		// the spell reference. A local one came from a path import in THIS
		// workspace's magusfile.
		if t.BuiltIn {
			fmt.Fprintf(w, "  %s\n", t.Name)
		} else {
			fmt.Fprintf(w, "  %s  [workspace-local, not a built-in]\n", t.Name)
		}
		// "adapts", not "language": the record carries two different languages, and
		// naming them both language is what made this ambiguous. This is the source
		// language the spell teaches magus to build; the import below is written in
		// Buzz, whatever that language happens to be.
		if t.Language != "" {
			fmt.Fprintf(w, "    adapts:  %s\n", t.Language)
		}
		if s := SpellSyntaxSummary(t); s != "" {
			fmt.Fprintf(w, "    syntax:  %s\n", s)
		}
		// Rendered as the whole statement rather than the bare path, so it is
		// copy-pasteable into a magusfile. It is the ONLY way to reach the spell's
		// ops: written literally, so the target graph can see the edge.
		//
		// Built-ins only. `magus/spell/<name>` resolves nothing for a workspace-local
		// spell, so printing it handed the reader a line that cannot work; that
		// spell is reached by the path import its own magusfile already writes.
		if t.BuzzImport != "" && t.BuiltIn {
			fmt.Fprintf(w, "    import:  import %q;  (buzz)\n", t.BuzzImport)
		}
		if len(t.Targets) > 0 {
			fmt.Fprintf(w, "    targets: %s\n", strings.Join(t.Targets, ", "))
		}
		for _, toolchain := range t.Toolchains {
			fmt.Fprintf(w, "    toolchain: %s (%s)\n", toolchain.Command, strings.Join(toolchain.Operations, ", "))
		}
		// Print each documented target's comment below the summary line; absent for
		// undocumented targets and for spells whose docs aren't carried (built-ins).
		for _, tgt := range t.Targets {
			if doc := t.TargetDocs[tgt]; doc != "" {
				fmt.Fprintf(w, "      %s: %s\n", tgt, FirstLine(doc))
			}
		}
		if t.Opaque {
			fmt.Fprintf(w, "    opaque: true\n")
		}
		printVersions(w, t.Versions)
	}
}

// RenderCharms writes the text form of the charm list. detail adds each charm's declaring targets and the argv edit each makes.
func RenderCharms(w io.Writer, charms []types.CharmEntry, detail bool) {
	fmt.Fprintf(w, "definition: %s\n\n", types.CharmDefinition)
	fmt.Fprintf(w, "charms (%d):\n", len(charms))
	for _, c := range charms {
		tags := make([]string, 0, 2)
		if c.Builtin {
			tags = append(tags, "built-in")
		}
		if c.Default {
			tags = append(tags, "workspace default")
		}
		line := "  " + c.Name
		if len(tags) > 0 {
			line += fmt.Sprintf("  [%s]", strings.Join(tags, ", "))
		}
		fmt.Fprintln(w, line)
		if c.Doc != "" {
			fmt.Fprintf(w, "    %s\n", c.Doc)
		}
		// List view stays a summary; the per-target argv edits print only in the
		// single-charm detail view, where the extra lines are the point.
		if !detail {
			if n := len(c.Declarations); n > 0 {
				fmt.Fprintf(w, "    declared by %d target(s)\n", n)
			}
			continue
		}
		if len(c.Declarations) == 0 {
			fmt.Fprintf(w, "    no target in this workspace declares it\n")
			continue
		}
		fmt.Fprintf(w, "    declared by:\n")
		for _, d := range c.Declarations {
			fmt.Fprintf(w, "      %s:%s  (spell %s)\n", d.Project, d.Target, d.Spell)
			if len(d.After) > 0 && !slices.Equal(d.Before, d.After) {
				fmt.Fprintf(w, "        base     %s\n", strings.Join(d.Before, " "))
				fmt.Fprintf(w, "        + %-6s %s\n", c.Name, strings.Join(d.After, " "))
			}
		}
	}
}

// RenderTargets writes the text form of the target list.
func RenderTargets(w io.Writer, targets []types.TargetEntry) {
	fmt.Fprintf(w, "definition: %s\n\n", types.TargetDefinition)
	fmt.Fprintf(w, "targets (%d):\n", len(targets))
	for _, t := range targets {
		switch t.Kind {
		case "canonical":
			fmt.Fprintf(w, "  %s  [canonical - affected/pipeline anchor; composed in the magusfile]\n", t.Name)
		case "spell":
			fmt.Fprintf(w, "  %s  [spell: %s]\n", t.Name, strings.Join(t.Spells, ", "))
		case "custom":
			fmt.Fprintf(w, "  %s  [custom - projects: %s]\n", t.Name, strings.Join(t.Projects, ", "))
		default:
			fmt.Fprintf(w, "  %s\n", t.Name)
		}
	}
}

// RenderEvaluatedProjects writes the text form of the evaluated project list.
func RenderEvaluatedProjects(w io.Writer, out types.EvaluatedProjectsOutput) {
		fmt.Fprintf(w, "definition: %s\n\n", out.Definition)
		fmt.Fprintf(w, "workspace: %s (%d projects)\n\n", out.Workspace, out.Count)
		for _, p := range out.Projects {
			fmt.Fprintf(w, "project: %s\n", types.ProjectLabel(p.Path, p.Dir))
			fmt.Fprintf(w, "  dir:     %s\n", p.Dir)
			if len(p.Sources) > 0 {
				fmt.Fprintf(w, "  sources: %v\n", p.Sources)
			}
			if len(p.Outputs) > 0 {
				fmt.Fprintf(w, "  outputs: %v\n", p.Outputs)
			}
			if len(p.DependsOn) > 0 {
				fmt.Fprintf(w, "  depends_on: %v\n", p.DependsOn)
			}
			for _, s := range p.ResolvedSpells {
				fmt.Fprintf(w, "  spell: %s\n", s.Name)
			}
			for targetName, pol := range p.TargetPolicies {
				fmt.Fprintf(w, "  policy: %s", targetName)
				if pol.IncludeOS != nil {
					fmt.Fprintf(w, "  include_os=%t", *pol.IncludeOS)
				}
				if pol.IncludeArch != nil {
					fmt.Fprintf(w, "  include_arch=%t", *pol.IncludeArch)
				}
				if pol.Drift != types.DriftDefault {
					fmt.Fprintf(w, "  drift=%s", pol.Drift)
				}
				if pol.RetryOnVolatile {
					fmt.Fprintf(w, "  retry_on_volatile")
				}
				if pol.Advisory {
					fmt.Fprintf(w, "  advisory=%q", pol.AdvisoryReason)
				}
				if pol.SkipCache {
					fmt.Fprintf(w, "  skip_cache")
				}
				if pol.Slots > 0 {
					fmt.Fprintf(w, "  slots=%d", pol.Slots)
				}
				if pol.MemoryMB > 0 {
					fmt.Fprintf(w, "  memory_mb=%d", pol.MemoryMB)
				}
				if pol.Timeout != "" {
					fmt.Fprintf(w, "  timeout=%s", pol.Timeout)
				}
				fmt.Fprintln(w)
			}
			fmt.Fprintln(w)
		}
}

// RenderProjects writes the text form of the project list.
func RenderProjects(w io.Writer, out types.ProjectsOutput) {
	fmt.Fprintf(w, "definition: %s\n\n", out.Definition)
	fmt.Fprintf(w, "workspace: %s (%d projects)\n\n", out.Workspace, out.Count)
	for _, p := range out.Projects {
		fmt.Fprintf(w, "project: %s\n", types.ProjectLabel(p.Path, p.Dir))
		fmt.Fprintf(w, "  dir:  %s\n", p.Dir)
		if p.Spell != "" {
			fmt.Fprintf(w, "  spell: %s\n", p.Spell)
		}
		if len(p.Sources) > 0 {
			fmt.Fprintf(w, "  sources: %v\n", p.Sources)
		}
		if len(p.Outputs) > 0 {
			fmt.Fprintf(w, "  outputs: %v\n", p.Outputs)
		}
		if len(p.DependsOn) > 0 {
			fmt.Fprintf(w, "  depends_on: %v\n", p.DependsOn)
		}
		fmt.Fprintln(w)
	}
}

// RenderEvaluatedTargets writes the text form of the evaluated targets. explain adds each charm's step-by-step effect on the command.
func RenderEvaluatedTargets(w io.Writer, out []types.EvaluatedTarget, explain bool) {
	fmt.Fprintf(w, "definition: %s\n\n", types.EvaluatedTargetDefinition)
	fmt.Fprintf(w, "targets (%d):\n\n", len(out))
	for _, e := range out {
		fmt.Fprintf(w, "project: %s  target: %s\n", e.Project, e.Target)
		fmt.Fprintf(w, "  dir:     %s\n", e.Dir)
		if len(e.Sources) > 0 {
			fmt.Fprintf(w, "  sources: %v\n", e.Sources)
		}
		if len(e.Outputs) > 0 {
			fmt.Fprintf(w, "  outputs: %v\n", e.Outputs)
		}
		// Always on, no flag: it is one line, it is absent for a target that composes
		// nothing, and "what does this run, in what order" is the question the command
		// already exists to answer: a flag would only hide the answer behind knowing
		// to ask for it (docs/recommendations.md, fold don't add).
		if len(e.Chain) > 0 {
			fmt.Fprintf(w, "  chain:   %s\n", types.Chain(e.Chain))
		}
		if len(e.BeforeKey) > 0 {
			refs := make([]string, len(e.BeforeKey))
			for i, r := range e.BeforeKey {
				refs[i] = r.Ref()
			}
			fmt.Fprintf(w, "  before key: %s\n", strings.Join(refs, ", "))
		}
		if len(e.DependsOn) > 0 {
			fmt.Fprintf(w, "  depends_on: %v\n", e.DependsOn)
		}
		if len(e.Charms) > 0 {
			fmt.Fprintf(w, "  charms:  %v\n", e.Charms)
		}
		for _, s := range e.Spells {
			fmt.Fprintf(w, "  spell: %s", s.Name)
			if len(s.TargetSources) > 0 {
				fmt.Fprintf(w, "  target_sources=%v", s.TargetSources)
			}
			fmt.Fprintln(w)
			if len(s.Command) > 0 {
				fmt.Fprintf(w, "    command: %s\n", strings.Join(s.Command, " "))
			}
			if s.Service != nil {
				fmt.Fprintf(w, "    service:\n")
				if len(s.Service.Readiness) > 0 {
					fmt.Fprintf(w, "      readiness: %s\n", strings.Join(s.Service.Readiness, " "))
				}
				if len(s.Service.Stop) > 0 {
					fmt.Fprintf(w, "      stop:      %s\n", strings.Join(s.Service.Stop, " "))
				}
				if s.Service.Idle != "" {
					fmt.Fprintf(w, "      idle:      %s\n", s.Service.Idle)
				}
				if s.Service.Distinct != "" {
					fmt.Fprintf(w, "      distinct:  %s\n", s.Service.Distinct)
				} else {
					fmt.Fprintf(w, "      shared:    yes (dedups by fingerprint)\n")
				}
				if s.Service.Fingerprint != "" {
					fmt.Fprintf(w, "      fingerprint: %s\n", s.Service.Fingerprint)
				}
			}
			if explain && len(s.CharmTrace) > 0 {
				fmt.Fprintf(w, "    charm trace:\n")
				for _, step := range s.CharmTrace {
					label := "base"
					if step.Charm != "" {
						label = "+ " + step.Charm
					}
					fmt.Fprintf(w, "      %-10s %s\n", label, strings.Join(step.Command, " "))
				}
			}
			// Shown without --explain: an overridden charm is a mistake, not a detail.
			if len(s.Conflicts) > 0 {
				fmt.Fprintf(w, "    charm conflicts:\n")
				for _, c := range s.Conflicts {
					by := c.OverriddenBy
					if by == "" {
						by = "another active charm"
					}
					fmt.Fprintf(w, "      %s overridden by %s (no effect here)\n", c.Name, by)
				}
			}
		}
		if e.Policy != nil {
			fmt.Fprintf(w, "  policy:")
			if e.Policy.IncludeOS != nil {
				fmt.Fprintf(w, "  include_os=%t", *e.Policy.IncludeOS)
			}
			if e.Policy.IncludeArch != nil {
				fmt.Fprintf(w, "  include_arch=%t", *e.Policy.IncludeArch)
			}
			if e.Policy.Drift != types.DriftDefault {
				fmt.Fprintf(w, "  drift=%s", e.Policy.Drift)
			}
			if e.Policy.RetryOnVolatile {
				fmt.Fprintf(w, "  retry_on_volatile")
			}
			if e.Policy.Advisory {
				fmt.Fprintf(w, "  advisory=%q", e.Policy.AdvisoryReason)
			}
			if e.Policy.SkipCache {
				fmt.Fprintf(w, "  skip_cache")
			}
			if e.Policy.Slots > 0 {
				fmt.Fprintf(w, "  slots=%d", e.Policy.Slots)
			}
			if e.Policy.MemoryMB > 0 {
				fmt.Fprintf(w, "  memory_mb=%d", e.Policy.MemoryMB)
			}
			if e.Policy.Timeout != "" {
				fmt.Fprintf(w, "  timeout=%s", e.Policy.Timeout)
			}
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w)
	}
}

// RenderWorkspaces writes the text form of the workspace entries.
func RenderWorkspaces(w io.Writer, out []types.WorkspaceEntry) {
	fmt.Fprintf(w, "definition: %s\n\n", types.WorkspaceDefinition)
	for _, e := range out {
		fmt.Fprintf(w, "workspace: %s\n", e.Root)
		fmt.Fprintf(w, "  projects: %d\n", e.ProjectCount)
		if e.VCSBaseRef != "" {
			fmt.Fprintf(w, "  vcs base ref: %s\n", e.VCSBaseRef)
		}
		if e.CacheDir != "" {
			fmt.Fprintf(w, "  cache dir: %s\n", e.CacheDir)
		}
		if e.Concurrency > 0 {
			fmt.Fprintf(w, "  concurrency: %d\n", e.Concurrency)
		}
		for _, l := range e.Workspaces {
			if l.Dir == "" {
				fmt.Fprintf(w, "  workspace %s: %s (this one)\n", l.Name, l.Root)
				continue
			}
			fmt.Fprintf(w, "  workspace %s: %s -> %s\n", l.Name, l.Dir, l.Root)
		}
	}
}

// RenderMCPTools writes the text form of the MCP tool list.
func RenderMCPTools(w io.Writer, out types.MCPToolReport) {
	fmt.Fprintf(w, "definition: %s\n\n", out.Definition)
	fmt.Fprintf(w, "mcp-tools (%d):\n", out.Count)
	for _, t := range out.MCPTools {
		fmt.Fprintf(w, "  %s\n", t.Name)
		if t.Description != "" {
			fmt.Fprintf(w, "    %s\n", t.Description)
		}
		for _, p := range t.Params {
			req := ""
			if p.Required {
				req = " (required)"
			}
			fmt.Fprintf(w, "    param %s <%s>%s: %s\n", p.Name, p.Type, req, p.Description)
		}
	}
}

// RenderFiles writes the text form of the classified files: each entry, then the declarations covering more than one of them.
func RenderFiles(w io.Writer, report types.FileReport, files []types.FileEntry, focus project.Focus, inFocus bool) {
	fmt.Fprintf(w, "definition: %s\n", types.FileDefinition)
	// Printed once rather than per entry: it is a fact about where the caller stands,
	// so it is the same for every path in the request, and it is what makes a
	// `focus: out` line below actionable instead of an accusation.
	if inFocus {
		fmt.Fprintf(w, "focus of %s: %s\n", strings.Join(focus.Seeds, ", "), strings.Join(focus.Projects, ", "))
	}
	fmt.Fprintln(w)
	for _, f := range files {
		fmt.Fprintf(w, "%s\n", f.Path)
		if f.Project != "" {
			fmt.Fprintf(w, "  project: %s\n", f.Project)
		}
		fmt.Fprintf(w, "  role: %s\n", f.Role)
		// Only when absent. Present is the overwhelmingly common case, and a line on
		// every entry would bury the one reading that changes what the rest means.
		if !f.Exists {
			fmt.Fprintf(w, "  exists: false\n")
		}
		if len(f.OutputOf) > 0 {
			fmt.Fprintf(w, "  output_of: %v\n", f.OutputOf)
		}
		if len(f.SourceOf) > 0 {
			fmt.Fprintf(w, "  source_of: %v\n", f.SourceOf)
		}
		if len(f.DependsOn) > 0 {
			fmt.Fprintf(w, "  depends_on: %v\n", f.DependsOn)
		}
		// Only when OUT, for the reason `exists: false` prints only when false: in
		// focus is the overwhelmingly common case, and a line on every entry buries
		// the one reading that changes what to do next. `-o json` carries both.
		if f.Focus == types.FocusOut {
			fmt.Fprintf(w, "  focus: out\n")
		}
		for _, c := range f.Claims {
			fmt.Fprintf(w, "  declared: %-6s %s  %s\n", c.Role, ClaimLabel(c), c.Glob)
		}
		if f.Hint != "" {
			fmt.Fprintf(w, "  hint: %s\n", f.Hint)
		}
		fmt.Fprintln(w)
	}
	if len(report.Overlaps) > 0 {
		fmt.Fprintln(w, "declarations covering more than one of these paths:")
		for _, c := range report.Overlaps {
			fmt.Fprintf(w, "  %-6s %-24s %-24s %s\n", c.Role, ClaimLabel(c), c.Glob, strings.Join(c.Paths, ", "))
		}
	}
}

// RenderModules writes the text form of the module list.
func RenderModules(w io.Writer, out []types.ModuleEntry) {
		fmt.Fprintf(w, "definition: %s\n\n", types.ModuleDefinition)
		fmt.Fprintf(w, "modules (%d):\n", len(out))
		for _, m := range out {
			fmt.Fprintf(w, "  %s\n", m.Name)
			if m.Doc != "" {
				fmt.Fprintf(w, "    %s\n", FirstLine(m.Doc))
			}
		}
		fmt.Fprintln(w, "\nRun `magus describe module <name>` for a module's methods and signatures.")
}
