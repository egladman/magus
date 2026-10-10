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

// ClassEconomy marks an agent whose work is bounded lookups, so a host that picks a
// model per agent can give it the cheapest tier it offers.
const ClassEconomy = "economy"

// Agent is a named subagent every agent host can route to by its Description. magus
// ships it host-neutral: each harness spell renders it in its host's file format and
// maps Class to whatever model the host offers. Nothing here names a model.
type Agent struct {
	Name        string
	Description string
	// Instructions is the agent's system prompt, the body of its Markdown file.
	Instructions string
	// ReadOnly means the agent must not change the workspace; a host that can restrict
	// tools or mark an agent read-only does.
	ReadOnly bool
	Class    string
}

// Params renders a as the map a harness spell receives for one agent.
func (a Agent) Params() map[string]any {
	return map[string]any{
		"name":         a.Name,
		"description":  a.Description,
		"instructions": a.Instructions,
		"read_only":    a.ReadOnly,
		"class":        a.Class,
	}
}

// agentDefs holds what a Markdown body cannot: everything but the instructions.
var agentDefs = []Agent{
	{
		Name:        "magus-scout",
		Description: "Answers standalone lookups and proves or refutes claims about a magus workspace with read-only magus queries, reporting each command and its output ref. Use for where-is, what-depends-on, is-this-generated and did-this-pass questions; not for edits or design.",
		ReadOnly:    true,
		Class:       ClassEconomy,
	},
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
		a.Instructions = strings.TrimSpace(string(body)) + "\n"
		out = append(out, a)
	}
	return out, nil
}

// ShippedAgentParams renders every shipped agent as the "agents" list a harness_agents
// invocation carries.
func ShippedAgentParams() ([]any, error) {
	agents, err := ShippedAgents()
	if err != nil {
		return nil, err
	}
	list := make([]any, len(agents))
	for i, a := range agents {
		list[i] = a.Params()
	}
	return list, nil
}
