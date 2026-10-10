package agent

import (
	"fmt"
	"strings"
	"unicode"
)

// DefaultHarnessMCPTokenRef is the secret-provider ref shipped spells name in
// setup hints. Under the built-in environment provider it resolves to an env var,
// which is the form a host's own MCP client configuration already expects.
//
//nolint:gosec // G101: an env var NAME, not a credential; the value it refers to never appears here
const DefaultHarnessMCPTokenRef = "MAGUS_MCP_TOKEN"

// DefaultHarnessMCPURL is the loopback Streamable HTTP endpoint a hint's __URL__
// names when a spell omits url.
const DefaultHarnessMCPURL = "http://127.0.0.1:7391/mcp"

// DefaultHarnessMCPCommand is the stdio server an agent host launches per session, from
// the checkout it works in: that checkout's own binary, so the session reads its own
// tree at its own build. A hint's __COMMAND__ names it.
const DefaultHarnessMCPCommand = "./magus mcp"

// HarnessMCPTransport is how a host reaches magus's MCP server.
type HarnessMCPTransport string

const (
	// HarnessMCPStdio is a `./magus mcp` process the host starts per session. It is the
	// default for an agent host.
	HarnessMCPStdio HarnessMCPTransport = "stdio"
	// HarnessMCPHTTP is the long-lived `magus server` endpoint, shared by every client and
	// answering from the checkout and build that started it. It is for the console and
	// for clients with no shell.
	HarnessMCPHTTP HarnessMCPTransport = "http"
)

// HarnessMCP is optional MCP setup guidance declared by a harness spell.
// Magus never writes host MCP client config: the user owns registration.
// A plan only carries Hint (and optional Register argv sketch / Docs link)
// for the CLI to print.
type HarnessMCP struct {
	Enabled  *bool  `json:"enabled,omitempty"`
	TokenRef string `json:"token_ref,omitempty"`
	// URL names the HTTP endpoint, and a spell that sets it is giving HTTP guidance.
	// Empty means stdio.
	URL string `json:"url,omitempty"`
	// Hint is the short instruction describe prints (host CLI command, paste
	// fragment, or "see docs"). Must not contain a resolved secret.
	Hint string `json:"hint,omitempty"`
	// Docs is an optional path or URL to host/Magus MCP documentation.
	Docs string `json:"docs,omitempty"`
	// Register is an optional argv sketch printed beside Hint (placeholders
	// __URL__ / __TOKEN_REF__ only; Magus does not execute it).
	Register []string `json:"register,omitempty"`
}

// Transport is the transport m's guidance registers: HTTP when the spell names a URL,
// stdio otherwise.
func (m *HarnessMCP) Transport() HarnessMCPTransport {
	if m != nil && m.URL != "" {
		return HarnessMCPHTTP
	}
	return HarnessMCPStdio
}

// GuidanceEnabled reports whether this block should contribute setup guidance.
// A nil Enabled pointer means enabled (spells omit the field when they want a hint).
func (m *HarnessMCP) GuidanceEnabled() bool {
	if m == nil {
		return false
	}
	if m.Enabled == nil {
		return true
	}
	return *m.Enabled
}

func (m *HarnessMCP) urlOrDefault() string {
	if m == nil || m.URL == "" {
		return DefaultHarnessMCPURL
	}
	return m.URL
}

func (m *HarnessMCP) tokenRefOrDefault() string {
	if m == nil || m.TokenRef == "" {
		return DefaultHarnessMCPTokenRef
	}
	return m.TokenRef
}

func validateHarnessMCP(m *HarnessMCP) error {
	if m == nil || !m.GuidanceEnabled() {
		return nil
	}
	if m.Hint == "" && m.Docs == "" && len(m.Register) == 0 {
		return fmt.Errorf("mcp: need hint, docs, and/or register argv (Magus does not write host MCP config)")
	}
	ref := m.tokenRefOrDefault()
	if err := validateMCPTokenRefName(ref); err != nil {
		return err
	}
	// Validate the text the user will see after placeholder substitution so a
	// TokenRef or URL cannot smuggle a resolved secret past the scanner.
	rendered := renderMCPSetupHint(m, m.urlOrDefault(), ref)
	if looksLikeEmbeddedMCPSecret(rendered) || looksLikeEmbeddedMCPSecret(m.urlOrDefault()) {
		return fmt.Errorf("mcp: setup guidance must not embed a resolved bearer token, name secret ref %q instead", ref)
	}
	return nil
}

func validateMCPTokenRefName(ref string) error {
	if ref == "" {
		return fmt.Errorf("mcp: token_ref is required")
	}
	if strings.HasPrefix(strings.ToLower(ref), "mgs_") {
		return fmt.Errorf("mcp: token_ref %q looks like a resolved token, use an env/secret-provider name such as %s", ref, DefaultHarnessMCPTokenRef)
	}
	for _, r := range ref {
		if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		return fmt.Errorf("mcp: token_ref %q must be an identifier (letters, digits, underscore)", ref)
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
	m := d.MCP
	if !m.GuidanceEnabled() {
		return "", nil
	}
	if err := validateHarnessMCP(m); err != nil {
		return "", fmt.Errorf("harness %q mcp: %w", d.ID, err)
	}
	return renderMCPSetupHint(m, m.urlOrDefault(), m.tokenRefOrDefault()), nil
}

// renderMCPSetupHint leads with the transport the guidance registers, so the choice reads
// the same for every host whatever its spell's hint says.
func renderMCPSetupHint(m *HarnessMCP, url, ref string) string {
	placeholders := strings.NewReplacer(
		"__URL__", url, "__TOKEN_REF__", ref, "__COMMAND__", DefaultHarnessMCPCommand,
		"{{url}}", url, "{{token_ref}}", ref, "{{command}}", DefaultHarnessMCPCommand,
	)
	var b strings.Builder
	if m.Transport() == HarnessMCPHTTP {
		fmt.Fprintf(&b, "transport: http, %s; one shared server answers from the checkout and build that started it", url)
	} else {
		fmt.Fprintf(&b, "transport: stdio, %s started per session in this checkout, so it serves this tree at this build", DefaultHarnessMCPCommand)
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
	m := d.MCP
	if !m.GuidanceEnabled() {
		return
	}
	if err := validateHarnessMCP(m); err != nil {
		result.MCPStatus = HarnessInvalid
		result.MCPReason = err.Error()
		return
	}
	// User-owned: Magus does not inspect host MCP client files. Status is
	// "verified" only for the spell's guidance declaration, not host wiring.
	result.MCPStatus = HarnessVerified
	result.MCPReason = "setup guidance declared; user owns host MCP client config (Magus does not write or inspect it)"
}
