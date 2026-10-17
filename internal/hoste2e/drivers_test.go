//go:build e2e

package hoste2e

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/json"
)

// Isolation, per host, from the user's own config:
//
//   - claude: --setting-sources project keeps ~/.claude/settings.json (and its hooks)
//     out, --strict-mcp-config keeps user MCP servers out. Auth is the logged-in
//     keychain entry or CLAUDE_CODE_OAUTH_TOKEN, so CLAUDE_CONFIG_DIR stays unset, and
//     Claude Code may record the temp project in its own ~/.claude.json state.
//   - codex: CODEX_HOME is a temp dir holding a symlink to ~/.codex/auth.json and a
//     config.toml written here, so the trust entry and the MCP table land there.
//   - cursor: cursor-agent documents no config-dir override; the workspace's
//     .cursor/hooks.json and .cursor/mcp.json carry everything, and --trust records the
//     temp workspace in cursor-agent's own state under ~/.cursor.
//   - opencode: XDG_CONFIG_HOME is a temp dir, so ~/.config/opencode/plugins stays
//     out; auth stays in XDG_DATA_HOME, which is left alone.
//
// Every host gets XDG_STATE_HOME at a temp dir, which holds magus's session store.

func claudeDriver() *hostDriver {
	h := &hostDriver{hostSpec: hostSpec{id: "claude-code", binary: "claude", model: envModel("CLAUDE", "haiku"),
		login: "a keychain login from `claude /login`, or CLAUDE_CODE_OAUTH_TOKEN from `claude setup-token`"}}
	// The desktop engine is newer than the CLI cask; it is preferred when the suite runs
	// inside a desktop session that exports it.
	h.locate("CLAUDE", candidate{path: os.Getenv("CLAUDE_CODE_EXECPATH"), source: "CLAUDE_CODE_EXECPATH"})
	h.env = func(*testing.T, *workspace) []string { return nil }
	h.wire = func(t *testing.T, w *workspace) {
		w.wireReplyCheck(t, ".claude/settings.json", "Stop", map[string]any{"hooks": []any{map[string]any{
			"type": "command", "timeout": 10,
			"command": `magus buzz -C "$CLAUDE_PROJECT_DIR" -s ` + replyCheckScript + " -- --agent-name claude-code",
		}}})
		// The command `magus describe harness claude-code` prints for MCP.
		w.run(t, h.path, "mcp", "add", "--scope", "project", "magus", "--", "./magus", "mcp")
	}
	h.turn = func(t *testing.T, w *workspace, conv *conversation, prompt string) (string, hostRun) {
		args := []string{"-p", prompt, "--output-format", "json", "--model", h.model,
			"--setting-sources", "project", "--mcp-config", filepath.Join(w.dir, ".mcp.json"), "--strict-mcp-config",
			"--dangerously-skip-permissions"}
		if conv.turns == 0 {
			args = append(args, "--session-id", conv.id)
		} else {
			args = append(args, "--resume", conv.id)
		}
		run := w.runHost(t, turnLabel(conv), h.path, args)
		var out struct {
			Result string `json:"result"`
		}
		if json.Unmarshal([]byte(run.stdout), &out) == nil {
			return out.Result, run
		}
		return run.stdout, run
	}
	h.skips = map[string]string{}
	return h
}

func codexDriver() *hostDriver {
	h := &hostDriver{hostSpec: hostSpec{id: "codex", binary: "codex", model: envModel("CODEX", "gpt-6-luna")}}
	h.locate("CODEX")
	home := func(w *workspace) string { return filepath.Join(w.root, "codex-home") }
	h.env = func(_ *testing.T, w *workspace) []string { return []string{"CODEX_HOME=" + home(w)} }
	h.wire = func(t *testing.T, w *workspace) {
		w.wireReplyCheck(t, ".codex/hooks.json", "Stop", map[string]any{"hooks": []any{map[string]any{
			"type": "command", "command": "magus buzz -s " + replyCheckScript + " -- --agent-name codex",
		}}})
		dir := home(w)
		mustMkdir(t, dir)
		auth := filepath.Join(os.Getenv("HOME"), ".codex", "auth.json")
		if !fileExists(auth) {
			t.Skipf("codex is not logged in: no %s", auth)
		}
		mustSymlink(t, auth, filepath.Join(dir, "auth.json"))
		config := "model = " + tomlString(h.model) + "\nmodel_reasoning_effort = \"low\"\n\n" +
			"[projects." + tomlString(w.dir) + "]\ntrust_level = \"trusted\"\n\n" +
			codexMCPTables(t, w.harnessPlan(t, h.id).MCPHint)
		writeFile(t, filepath.Join(dir, "config.toml"), config)
	}
	h.turn = func(t *testing.T, w *workspace, conv *conversation, prompt string) (string, hostRun) {
		last := filepath.Join(w.logs, turnLabel(conv)+".last-message")
		run := w.runHost(t, turnLabel(conv), h.path, []string{"exec", "--json",
			"--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust",
			"--cd", w.dir, "-m", h.model, "--output-last-message", last, prompt})
		body, _ := os.ReadFile(last)
		return string(body), run
	}
	h.skips = map[string]string{
		"brief-delivered": "Codex wires the brief to SessionStart(compact) alone, and codex exec has no way to ask for a compaction",
	}
	return h
}

// codexMCPTables lifts the TOML tables out of the hint `magus describe harness codex`
// prints for the user to paste into config.toml.
func codexMCPTables(t *testing.T, hint string) string {
	t.Helper()
	var tables []string
	for _, block := range strings.Split(hint, "\n\n") {
		if strings.HasPrefix(block, "[") {
			tables = append(tables, block)
		}
	}
	if len(tables) == 0 {
		t.Fatalf("the codex MCP hint carries no TOML table to paste:\n%s", hint)
	}
	return strings.Join(tables, "\n\n") + "\n"
}

func tomlString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func cursorDriver() *hostDriver {
	h := &hostDriver{hostSpec: hostSpec{id: "cursor", binary: "cursor-agent", model: envModel("CURSOR", "auto")}}
	h.locate("CURSOR")
	h.env = func(*testing.T, *workspace) []string { return nil }
	h.wire = func(t *testing.T, w *workspace) {
		w.wireReplyCheck(t, ".cursor/hooks.json", "stop", map[string]any{
			"command": "magus buzz -s " + replyCheckScript + " -- --agent-name cursor", "timeout": 10, "loop_limit": 1,
		})
		// The stdio entry `magus describe harness cursor` describes in prose.
		writeJSON(t, filepath.Join(w.dir, ".cursor", "mcp.json"), map[string]any{
			"mcpServers": map[string]any{"magus": map[string]any{"command": "./magus", "args": []string{"mcp"}}},
		})
	}
	h.turn = func(t *testing.T, w *workspace, conv *conversation, prompt string) (string, hostRun) {
		args := []string{"-p", "--output-format", "json", "--force", "--trust", "--approve-mcps",
			"--workspace", w.dir, "--model", h.model, prompt}
		run := w.runHost(t, turnLabel(conv), h.path, args)
		// cursor-agent 2026.10.01 sometimes drops its connection with every event hooked;
		// one retry, reported, rather than a flaky row.
		if strings.Contains(run.stdout+run.stderr, "WritableIterable is closed") {
			t.Logf("cursor-agent lost its connection (WritableIterable is closed); retrying once")
			run = w.runHost(t, turnLabel(conv)+"-retry", h.path, args)
		}
		var out struct {
			Result string `json:"result"`
		}
		if json.Unmarshal([]byte(run.stdout), &out) == nil {
			return out.Result, run
		}
		return run.stdout, run
	}
	const noPTY = "cursor-agent -p fires neither beforeSubmitPrompt nor stop (probed on 2026.10.01); proving it needs an interactive pty driver this suite does not have"
	h.skips = map[string]string{"prompt-recorded": noPTY, "reply-check": noPTY}
	return h
}

func opencodeDriver() *hostDriver {
	h := &hostDriver{hostSpec: hostSpec{id: "opencode", binary: "opencode", model: envModel("OPENCODE", "opencode/big-pickle")}}
	h.locate("OPENCODE")
	h.env = func(_ *testing.T, w *workspace) []string {
		return []string{
			"XDG_CONFIG_HOME=" + filepath.Join(w.root, "xdg-config"),
			// The plugin runs the reply check only when this names the script.
			"__PROOFREAD_REPLY_BUZZ=" + filepath.Join(w.dir, replyCheckScript),
		}
	}
	h.wire = func(t *testing.T, w *workspace) {
		// The plugin the OpenCode guide says to save under .opencode/plugins/.
		copyFile(t, repoPath(t, "docs/guides/integrations/agents/opencode-plugin.ts"),
			filepath.Join(w.dir, ".opencode", "plugins", "magus.ts"), 0o644)
		// The local server `magus describe harness opencode` describes in prose, merged
		// into whatever opencode.json the harness merge wrote.
		path := filepath.Join(w.dir, "opencode.json")
		config := map[string]any{}
		if body, err := os.ReadFile(path); err == nil {
			if err := json.Unmarshal(body, &config); err != nil {
				t.Fatalf("%s: %v", path, err)
			}
		}
		config["mcp"] = map[string]any{"magus": map[string]any{"type": "local", "command": []string{"./magus", "mcp"}}}
		writeJSON(t, path, config)
	}
	h.turn = func(t *testing.T, w *workspace, conv *conversation, prompt string) (string, hostRun) {
		run := w.runHost(t, turnLabel(conv), h.path, []string{"run", "--format", "json", "--dir", w.dir, "--auto", "-m", h.model, prompt})
		return opencodeText(run.stdout), run
	}
	h.skips = map[string]string{
		"brief-delivered": "OpenCode offers no context channel after a compaction; its guide documents that the plugin hands a compacted session nothing",
	}
	return h
}

// opencodeText joins the assistant text parts of `opencode run --format json` events.
func opencodeText(stdout string) string {
	var parts []string
	for _, line := range strings.Split(stdout, "\n") {
		var ev struct {
			Type string `json:"type"`
			Part struct {
				Text string `json:"text"`
			} `json:"part"`
		}
		if json.Unmarshal([]byte(line), &ev) == nil && ev.Type == "text" {
			parts = append(parts, ev.Part.Text)
		}
	}
	if len(parts) == 0 {
		return stdout
	}
	return strings.Join(parts, "\n")
}

func turnLabel(conv *conversation) string { return "turn" + strconv.Itoa(conv.turns+1) }

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(body)+"\n")
}
