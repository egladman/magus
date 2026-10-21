package agent

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/egladman/magus/types"
)

// DefaultHarnessMCPTokenRef is the secret-provider ref shipped spells name in
// setup hints. Under the built-in environment provider it resolves to an env var,
// which is the form a host's own MCP client configuration already expects.
//
//nolint:gosec // G101: an env var NAME, not a credential; the value it refers to never appears here
const DefaultHarnessMCPTokenRef = "MAGUS_MCP_TOKEN"

func tokenRefOrDefault(m *types.McpServer) string {
	if m.TokenRef == "" {
		return DefaultHarnessMCPTokenRef
	}
	return m.TokenRef
}

// validateHarnessMCP refuses guidance that names nothing to do, a transport it cannot
// reach, or a resolved secret. A nil m is a harness that does not export mgs_getMcp.
func validateHarnessMCP(m *types.McpServer) error {
	if m == nil {
		return nil
	}
	if m.Hint == "" && m.Docs == "" && len(m.Register) == 0 {
		return fmt.Errorf("harness mcp: need hint, docs, and/or register argv (Magus does not write host MCP config)")
	}
	switch m.Transport {
	case types.McpStdio:
		if len(m.Command) == 0 {
			return fmt.Errorf("harness mcp: a stdio server names the command the host starts, such as [\"./magus\", \"mcp\"]")
		}
	case types.McpHTTP:
		if m.URL == "" {
			return fmt.Errorf("harness mcp: an http server names the url the host reaches")
		}
	default:
		return fmt.Errorf("harness mcp: transport %s is none of %s", m.Transport, strings.Join(m.Transport.Values(), ", "))
	}
	ref := tokenRefOrDefault(m)
	if err := validateMCPTokenRefName(ref); err != nil {
		return err
	}
	// Validate the text the user will see after placeholder substitution so a
	// TokenRef or URL cannot smuggle a resolved secret past the scanner.
	if looksLikeEmbeddedMCPSecret(renderMCPSetupHint(m)) || looksLikeEmbeddedMCPSecret(m.URL) {
		return fmt.Errorf("harness mcp: setup guidance must not embed a resolved bearer token, name secret ref %q instead", ref)
	}
	return nil
}

func validateMCPTokenRefName(ref string) error {
	if ref == "" {
		return fmt.Errorf("harness mcp: token_ref is required")
	}
	if strings.HasPrefix(strings.ToLower(ref), "mgs_") {
		return fmt.Errorf("harness mcp: token_ref %q looks like a resolved token, use an env/secret-provider name such as %s", ref, DefaultHarnessMCPTokenRef)
	}
	for _, r := range ref {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		return fmt.Errorf("harness mcp: token_ref %q must be an identifier (letters, digits, underscore)", ref)
	}
	return nil
}

func looksLikeEmbeddedMCPSecret(blob string) bool {
	lower := strings.ToLower(blob)
	if strings.Contains(lower, "bearer mgs_") {
		return true
	}
	if strings.Contains(lower, "mgs_") {
		// Any mgs_ in guidance is treated as a minted Magus token, not a ref name.
		return true
	}
	return false
}

// harnessMCPHint renders d's user-owned MCP setup guidance, empty when it declares none. It
// never reads a host MCP config file and never resolves the secret ref.
func harnessMCPHint(d HarnessDescriptor) (string, error) {
	if d.MCP == nil {
		return "", nil
	}
	if err := validateHarnessMCP(d.MCP); err != nil {
		return "", fmt.Errorf("harness %q mcp: %w", d.ID, err)
	}
	return renderMCPSetupHint(d.MCP), nil
}

// renderMCPSetupHint leads with the transport the guidance registers, so the choice reads
// the same for every host whatever its spell's hint says.
func renderMCPSetupHint(m *types.McpServer) string {
	command, ref := strings.Join(m.Command, " "), tokenRefOrDefault(m)
	placeholders := strings.NewReplacer(
		"__URL__", m.URL, "__TOKEN_REF__", ref, "__COMMAND__", command,
		"{{url}}", m.URL, "{{token_ref}}", ref, "{{command}}", command,
	)
	var b strings.Builder
	if m.Transport == types.McpHTTP {
		fmt.Fprintf(&b, "transport: http, %s; one shared server answers from the checkout and build that started it", m.URL)
	} else {
		fmt.Fprintf(&b, "transport: stdio, %s started per session in this checkout, so it serves this tree at this build", command)
	}
	if m.Hint != "" {
		b.WriteByte('\n')
		b.WriteString(placeholders.Replace(m.Hint))
	}
	if len(m.Register) > 0 {
		args := make([]string, len(m.Register))
		for i, a := range m.Register {
			args[i] = placeholders.Replace(a)
		}
		b.WriteString("\ncommand sketch (not executed): ")
		b.WriteString(strings.Join(args, " "))
	}
	if m.Docs != "" {
		b.WriteString("\ndocs: ")
		b.WriteString(m.Docs)
	}
	return b.String()
}

func verifyHarnessMCP(d HarnessDescriptor, result *HarnessVerification) {
	if d.MCP == nil {
		return
	}
	if err := validateHarnessMCP(d.MCP); err != nil {
		result.MCP = HarnessCheck{Status: HarnessInvalid, Reason: err.Error()}
		return
	}
	// User-owned: Magus does not inspect host MCP client files. Status is
	// "verified" only for the spell's guidance declaration, not host wiring.
	result.MCP = HarnessCheck{Status: HarnessVerified, Reason: "setup guidance declared; user owns host MCP client config (Magus does not write or inspect it)"}
}
