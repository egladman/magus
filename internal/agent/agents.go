package agent

import (
	"embed"
	"fmt"
	"path"
	"strings"
)

// agentFS holds the host-neutral agent bodies, one Markdown file per agent.
//
//go:embed agents
var agentFS embed.FS

// Agent is a named subagent every agent host can route to by its Description. magus
// ships it host-neutral: each harness spell renders it in its host's file format. Nothing
// here names a model; the render step hands the spell the model an agent runs on, from
// the workspace's model tiers.
type Agent struct {
	Name        string
	Description string
	// Instructions is the agent's system prompt, the body of its Markdown file.
	Instructions string
	// ReadOnly means the agent must not change the workspace; a host that can restrict
	// tools or mark an agent read-only does.
	ReadOnly bool
}

// agentDefs holds what a Markdown body cannot: everything but the instructions.
var agentDefs = []Agent{
	{
		Name:        "magus-scout",
		Description: "Answers standalone lookups and proves or refutes claims about a magus workspace with read-only magus queries, reporting each command and its output ref. Use for where-is, what-depends-on, is-this-generated and did-this-pass questions; not for edits or design.",
		ReadOnly:    true,
	},
}

// scoutCommands is the one allowlist the scout's prose renders from.
var scoutCommands = []struct{ Command, Use string }{
	{"magus query", "for what exists and how entities relate."},
	{"magus refs", "for where a symbol is defined and used."},
	{"magus describe", "for a file, target, project or job."},
	{"magus affected --explain", "for why a project is or is not affected."},
	{"magus query output <ref>", "to reopen the full output of an earlier command."},
	{"magus job exit", "as your last act, to file your row."},
}

func renderScoutCommands() string {
	var b strings.Builder
	for i, c := range scoutCommands {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "- `%s` %s", c.Command, c.Use)
	}
	return b.String()
}

// ShippedAgents returns the agents magus ships, with instructions read from the embedded
// Markdown.
func ShippedAgents() ([]Agent, error) {
	out := make([]Agent, 0, len(agentDefs))
	for _, a := range agentDefs {
		body, err := agentFS.ReadFile(path.Join("agents", a.Name+".md"))
		if err != nil {
			return nil, fmt.Errorf("agent: read %s: %w", a.Name, err)
		}
		text := strings.ReplaceAll(string(body), "{{commands}}", renderScoutCommands())
		a.Instructions = strings.TrimSpace(text) + "\n"
		out = append(out, a)
	}
	return out, nil
}
