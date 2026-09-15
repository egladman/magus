package agent

import (
	"fmt"
	"strings"
	"unicode"
)

// DefaultHarnessMCPTokenRef is the secret-provider ref shipped spells name in
// setup hints. Under the built-in environment provider it is the env var name hosts
// already document (Codex bearer_token_env_var, Cursor ${env:...}).
const DefaultHarnessMCPTokenRef = "MAGUS_MCP_TOKEN"

// DefaultHarnessMCPURL is the loopback Streamable HTTP endpoint when a spell
// omits url.
const DefaultHarnessMCPURL = "http://127.0.0.1:7391/mcp"

// HarnessMCP is optional MCP setup guidance declared by a harness spell.
// Magus never writes host MCP client config: the user owns registration.
// Apply only records Hint (and optional Register argv sketch / Docs link)
// for the CLI to print.
type HarnessMCP struct {
	Enabled  *bool   `json:"enabled,omitempty"`
	TokenRef string  `json:"token_ref,omitempty"`
	URL      string  `json:"url,omitempty"`
	// Hint is the short instruction recorded for apply (host CLI command, paste
	// fragment, or "see docs"). Must not contain a resolved secret.
	Hint string `json:"hint,omitempty"`
	// Docs is an optional path or URL to host/Magus MCP documentation.
	Docs string `json:"docs,omitempty"`
	// Register is an optional argv sketch printed beside Hint (placeholders
	// __URL__ / __TOKEN_REF__ only; Magus does not execute it).
	Register []string `json:"register,omitempty"`
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
		return fmt.Errorf("mcp: setup guidance must not embed a resolved bearer token; name secret ref %q instead", ref)
	}
	return nil
}

func validateMCPTokenRefName(ref string) error {
	if ref == "" {
		return fmt.Errorf("mcp: token_ref is required")
	}
	if strings.HasPrefix(strings.ToLower(ref), "mgs_") {
		return fmt.Errorf("mcp: token_ref %q looks like a resolved token; use an env/secret-provider name (e.g. %s)", ref, DefaultHarnessMCPTokenRef)
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

// applyHarnessMCP records user-owned MCP setup guidance on update. It never
// writes a host MCP config file and never resolves the secret ref.
func applyHarnessMCP(d HarnessDescriptor, update *HarnessUpdate) error {
	m := d.MCP
	if !m.GuidanceEnabled() {
		return nil
	}
	if err := validateHarnessMCP(m); err != nil {
		return fmt.Errorf("harness %q mcp: %w", d.ID, err)
	}
	update.MCPHint = renderMCPSetupHint(m, m.urlOrDefault(), m.tokenRefOrDefault())
	return nil
}

func renderMCPSetupHint(m *HarnessMCP, url, ref string) string {
	hint := m.Hint
	hint = strings.ReplaceAll(hint, "__URL__", url)
	hint = strings.ReplaceAll(hint, "__TOKEN_REF__", ref)
	hint = strings.ReplaceAll(hint, "{{url}}", url)
	hint = strings.ReplaceAll(hint, "{{token_ref}}", ref)

	var b strings.Builder
	if hint != "" {
		b.WriteString(hint)
	}
	if len(m.Register) > 0 {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		args := make([]string, len(m.Register))
		for i, a := range m.Register {
			a = strings.ReplaceAll(a, "__URL__", url)
			a = strings.ReplaceAll(a, "__TOKEN_REF__", ref)
			a = strings.ReplaceAll(a, "{{url}}", url)
			a = strings.ReplaceAll(a, "{{token_ref}}", ref)
			args[i] = a
		}
		b.WriteString("command sketch (not executed): ")
		b.WriteString(strings.Join(args, " "))
	}
	if m.Docs != "" {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString("docs: ")
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
