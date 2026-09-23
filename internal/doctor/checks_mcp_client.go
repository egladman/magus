package doctor

import (
	"fmt"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/types"
)

// checkMCPClient reports whether wired harness spells declare user-owned MCP
// setup guidance (hint, docs, and/or register sketch). Magus does not write or
// inspect host MCP client files.
func (r *runner) checkMCPClient() types.DoctorCheck {
	const name = "mcp-client"
	root := r.ws.Root()
	wired := workspaceHarnesses(r.ws)
	ctx := agent.ContextWithWiredHarnesses(r.runCtx(), wired)
	ids, err := agent.KnownHarnesses(ctx, wired...)
	if err != nil {
		return types.DoctorCheck{Name: name, Status: types.DoctorAdvice, Message: "cannot list harnesses: " + err.Error()}
	}
	if len(ids) == 0 {
		return types.DoctorCheck{Name: name, Status: types.DoctorOK, Message: "no harnesses wired"}
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
		if !d.MCP.GuidanceEnabled() {
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
	status := types.DoctorOK
	if problems > 0 {
		status = types.DoctorAdvice
		msg = fmt.Sprintf("%s; %d load problem(s)", msg, problems)
	}
	return types.DoctorCheck{Name: name, Status: status, Message: msg, Details: details, Evidence: types.EvidenceDeclared}
}
