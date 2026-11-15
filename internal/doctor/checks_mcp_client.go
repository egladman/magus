package doctor

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types/check"
)

// checkMCPClient reports whether wired harness spells declare user-owned MCP
// setup guidance (hint, docs, and/or register sketch). Magus does not write or
// inspect host MCP client files.
func (r *runner) checkMCPClient(ctx context.Context) check.Check {
	const name = "mcp-client"
	root := r.ws.Root()
	wired, err := workspace.WiredHarnesses(r.ws)
	if err != nil {
		return unknownWiring(name, err)
	}
	ctx = agent.ContextWithWiredHarnesses(ctx, wired)
	ids, err := agent.KnownHarnesses(ctx, wired...)
	if err != nil {
		return check.Check{Name: name, Status: check.Advice, Message: "cannot list harnesses: " + err.Error()}
	}
	if len(ids) == 0 {
		return check.Check{Name: name, Status: check.OK, Message: "no harnesses wired"}
	}

	var details []string
	guided := 0
	problems := 0
	for _, id := range ids {
		d, _, err := agent.LoadHarness(ctx, root, id)
		if err != nil {
			problems++
			details = append(details, fmt.Sprintf("%s: load failed: %s", id, err.Error()))
			continue
		}
		if d.MCP == nil {
			details = append(details, fmt.Sprintf("%s: no MCP setup guidance (wire MCP yourself via host docs)", id))
			continue
		}
		ref := d.MCP.TokenRef
		if ref == "" {
			ref = agent.DefaultHarnessMCPTokenRef
		}
		guided++
		line := fmt.Sprintf("%s: MCP setup guidance declared (secret ref %s)", id, ref)
		if d.MCP.Docs != "" {
			line += "; docs " + d.MCP.Docs
		}
		details = append(details, line)
	}

	msg := fmt.Sprintf("%d wired harness(es); %d declare MCP setup hints (Magus does not write host MCP config)",
		len(ids), guided)
	status := check.OK
	if problems > 0 {
		status = check.Advice
		msg = fmt.Sprintf("%s; %d load problem(s)", msg, problems)
	}
	return check.Check{Name: name, Status: status, Message: msg, Details: details, Evidence: check.EvidenceDeclared}
}
