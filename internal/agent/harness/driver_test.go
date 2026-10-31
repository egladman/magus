//go:build e2e

package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/testkit/e2e"
)

// claudeDriver keeps ~/.claude/settings.json out with --setting-sources
// and user MCP servers out with --strict-mcp-config. Auth is the keychain
// or CLAUDE_CODE_OAUTH_TOKEN, so CLAUDE_CONFIG_DIR stays unset, and the
// host may record the temp project in its own ~/.claude.json.
func claudeDriver() *hostDriver {
	h := &hostDriver{id: "claude-code", binary: "claude", model: envModel("CLAUDE", "haiku"),
		login:      "a keychain login from `claude /login`, or CLAUDE_CODE_OAUTH_TOKEN from `claude setup-token`",
		briefTurns: []string{"Reply with the single word ready.", "/compact", briefQuestion}}
	// The desktop engine is newer than the CLI cask, so it is preferred
	// when the suite runs inside a desktop session that exports it.
	h.locate("CLAUDE", e2e.Candidate{Path: os.Getenv("CLAUDE_CODE_EXECPATH"), Source: "CLAUDE_CODE_EXECPATH"})
	h.env = func(*testing.T, *workspace) []string { return nil }
	h.wire = func(t *testing.T, w *workspace) {
		w.wireReplyCheck(t, ".claude/settings.json", "Stop", map[string]any{"hooks": []any{map[string]any{
			"type": "command", "timeout": 10,
			"command": fmt.Sprintf(`magus buzz -C "$CLAUDE_PROJECT_DIR" -s %s -- --agent-name claude-code`, replyCheckScript),
		}}})
		// The command `magus describe harness claude-code` prints for MCP.
		w.run(t, h.install.Path, "mcp", "add", "--scope", "project", "magus", "--", "./magus", "mcp")
	}
	h.turn = func(t *testing.T, w *workspace, conv *conversation, prompt string) (string, e2e.Result) {
		args := []string{"-p", prompt, "--output-format", "json", "--model", h.model,
			"--setting-sources", "project", "--mcp-config", filepath.Join(w.dir, ".mcp.json"), "--strict-mcp-config",
			"--dangerously-skip-permissions"}
		if conv.turns == 0 {
			args = append(args, "--session-id", conv.id)
		} else {
			args = append(args, "--resume", conv.id)
		}
		run := w.runHost(t, turnLabel(conv), h.install.Path, args)
		var out struct {
			Result string `json:"result"`
		}
		if json.Unmarshal([]byte(run.Stdout), &out) == nil {
			return out.Result, run
		}
		return run.Stdout, run
	}
	h.askTurn = func(t *testing.T, w *workspace, c askCase) askRun {
		args := append([]string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose",
			"--include-hook-events", "--model", h.model, "--setting-sources", "project",
			"--mcp-config", filepath.Join(w.dir, ".mcp.json"), "--strict-mcp-config",
			"--permission-prompt-tool", "stdio"}, c.args...)
		return w.answerPrompts(t, c.name, h.install.Path, args, c.env, askPrompt)
	}
	return h
}

var bypassArgs = []string{"--permission-mode", "bypassPermissions", "--allow-dangerously-skip-permissions"}

// bareHookAsk is magus-command.buzz's Claude Code ask arm, with no mode check.
const bareHookAsk = `{{else if eq .decision "ask"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":{{toJson .reason}}}}`

type claudeCodeSuite struct{ hostSuite }

func (s *claudeCodeSuite) TestAskDefault() {
	s.ask(askCase{name: "default", want: askPrompts, args: []string{"--permission-mode", "default"}})
}

func (s *claudeCodeSuite) TestAskAcceptEdits() {
	s.ask(askCase{name: "accept-edits", want: askPrompts, args: []string{"--permission-mode", "acceptEdits"}})
}

func (s *claudeCodeSuite) TestAskAuto() {
	s.ask(askCase{name: "auto", want: askPrompts, args: []string{"--permission-mode", "auto"}})
}

func (s *claudeCodeSuite) TestAskPlan() {
	s.ask(askCase{name: "plan", want: askRefuses, args: []string{"--permission-mode", "plan"}})
}

func (s *claudeCodeSuite) TestAskAllowRule() {
	s.ask(askCase{name: "allow-rule", want: askPrompts, args: []string{"--permission-mode", "default"},
		setup: func(t *testing.T, w *workspace) { w.allowInSettings(t, "Bash(./magus run:*)") }})
}

func (s *claudeCodeSuite) TestAskDontAsk() {
	s.ask(askCase{name: "dont-ask", want: askRefuses, args: []string{"--permission-mode", "dontAsk"}})
}

func (s *claudeCodeSuite) TestAskBypass() {
	s.ask(askCase{name: "bypass", want: askRefuses, args: bypassArgs})
}

// The hook's bare ask under bypass: what the refusal exists to prevent.
func (s *claudeCodeSuite) TestAskBypassBareHookAsk() {
	s.ask(askCase{name: "bypass-bare-hook-ask", want: askObserves, args: bypassArgs, env: []string{"HOST_ASK_BRANCH=" + bareHookAsk}})
}

// codexDriver points CODEX_HOME at a temp dir, so the trust entry and the
// MCP table land there. It holds a config.toml written here and a symlink
// to ~/.codex/auth.json.
func codexDriver() *hostDriver {
	h := &hostDriver{id: "codex", binary: "codex", model: envModel("CODEX", "gpt-6-luna")}
	h.locate("CODEX")
	home := func(w *workspace) string { return filepath.Join(w.root, "codex-home") }
	h.env = func(_ *testing.T, w *workspace) []string { return []string{"CODEX_HOME=" + home(w)} }
	h.wire = func(t *testing.T, w *workspace) {
		w.wireReplyCheck(t, ".codex/hooks.json", "Stop", map[string]any{"hooks": []any{map[string]any{
			"type": "command", "command": fmt.Sprintf("magus buzz -s %s -- --agent-name codex", replyCheckScript),
		}}})
		dir := home(w)
		mustMkdir(t, dir)
		auth := filepath.Join(os.Getenv("HOME"), ".codex", "auth.json")
		if !fileExists(auth) {
			h.unauthenticated = "no " + auth
			t.Skip(h.notLoggedIn())
		}
		mustSymlink(t, auth, filepath.Join(dir, "auth.json"))
		config := fmt.Sprintf("model = %s\nmodel_reasoning_effort = \"low\"\n\n[projects.%s]\ntrust_level = \"trusted\"\n\n%s",
			tomlString(h.model), tomlString(w.dir), codexMCPTables(t, w.harnessPlan(t, h.id).MCPHint))
		writeFile(t, filepath.Join(dir, "config.toml"), config)
	}
	h.turn = func(t *testing.T, w *workspace, conv *conversation, prompt string) (string, e2e.Result) {
		last := filepath.Join(w.logs, turnLabel(conv)+".last-message")
		run := w.runHost(t, turnLabel(conv), h.install.Path, []string{"exec", "--json",
			"--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust",
			"--cd", w.dir, "-m", h.model, "--output-last-message", last, prompt})
		body, _ := os.ReadFile(last)
		return string(body), run
	}
	h.askTurn = func(t *testing.T, w *workspace, c askCase) askRun {
		args := append([]string{"exec", "--json", "--dangerously-bypass-hook-trust", "--cd", w.dir, "-m", h.model}, c.args...)
		return askHeadless(t, w, c, h.install.Path, append(args, askPrompt))
	}
	return h
}

// codexMCPTables lifts the TOML tables out of the hint `magus describe
// harness codex` prints for the user to paste into config.toml.
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

type codexSuite struct{ hostSuite }

func (s *codexSuite) TestBriefDelivered() {
	s.skipScenario("brief-delivered", "Codex wires the brief to SessionStart(compact) alone, and codex exec has no way to ask for a compaction")
}

func (s *codexSuite) TestAskApprovalNever() {
	s.ask(askCase{name: "approval-never", want: askHolds, args: []string{"-c", `approval_policy="never"`}})
}

func (s *codexSuite) TestAskApprovalOnRequest() {
	s.ask(askCase{name: "approval-on-request", want: askHolds, args: []string{"-c", `approval_policy="on-request"`}})
}

func (s *codexSuite) TestAskBypass() {
	s.ask(askCase{name: "bypass", want: askHolds, args: []string{"--dangerously-bypass-approvals-and-sandbox"}})
}

// cursorDriver has no config-dir override to isolate with. The workspace's
// .cursor/hooks.json and .cursor/mcp.json carry everything, and --trust
// records the temp workspace in cursor-agent's own state under ~/.cursor.
func cursorDriver() *hostDriver {
	h := &hostDriver{id: "cursor", binary: "cursor-agent", model: envModel("CURSOR", "auto")}
	h.locate("CURSOR")
	h.env = func(*testing.T, *workspace) []string { return nil }
	h.wire = func(t *testing.T, w *workspace) {
		w.wireReplyCheck(t, ".cursor/hooks.json", "stop", map[string]any{
			"command": fmt.Sprintf("magus buzz -s %s -- --agent-name cursor", replyCheckScript), "timeout": 10, "loop_limit": 1,
		})
		// The stdio entry `magus describe harness cursor` describes in prose.
		writeJSON(t, filepath.Join(w.dir, ".cursor", "mcp.json"), map[string]any{
			"mcpServers": map[string]any{"magus": map[string]any{"command": "./magus", "args": []string{"mcp"}}},
		})
	}
	h.turn = func(t *testing.T, w *workspace, conv *conversation, prompt string) (string, e2e.Result) {
		args := []string{"-p", "--output-format", "json", "--force", "--trust", "--approve-mcps",
			"--workspace", w.dir, "--model", h.model, prompt}
		run := w.runHost(t, turnLabel(conv), h.install.Path, args)
		// cursor-agent 2026.10.01 sometimes drops its connection with every
		// event hooked; one retry, reported, rather than a flaky row.
		if strings.Contains(run.Stdout+run.Stderr, "WritableIterable is closed") {
			t.Logf("cursor-agent lost its connection (WritableIterable is closed); retrying once")
			run = w.runHost(t, turnLabel(conv)+"-retry", h.install.Path, args)
		}
		var out struct {
			Result string `json:"result"`
		}
		if json.Unmarshal([]byte(run.Stdout), &out) == nil {
			return out.Result, run
		}
		return run.Stdout, run
	}
	h.askTurn = func(t *testing.T, w *workspace, c askCase) askRun {
		args := append([]string{"-p", "--output-format", "json", "--trust", "--approve-mcps",
			"--workspace", w.dir, "--model", h.model}, c.args...)
		return askHeadless(t, w, c, h.install.Path, append(args, askPrompt))
	}
	return h
}

// askReachedShellEvent proves beforeShellExecution consumed what preToolUse
// filed: that event is the gate behind preToolUse's allow.
func askReachedShellEvent(t *testing.T, w *workspace) {
	t.Helper()
	held, err := filepath.Glob(filepath.Join(w.root, "state", "magus", "cursor-hook", "ask-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(held) > 0 {
		t.Errorf("beforeShellExecution never read the filed ask: %v", held)
	}
}

const noPTY = "cursor-agent -p fires neither beforeSubmitPrompt nor stop (probed on 2026.10.01); proving it needs an interactive pty driver this suite does not have"

type cursorSuite struct{ hostSuite }

func (s *cursorSuite) TestPromptRecorded() { s.skipScenario("prompt-recorded", noPTY) }

func (s *cursorSuite) TestReplyCheck() { s.skipScenario("reply-check", noPTY) }

func (s *cursorSuite) TestAskNoForce() {
	s.ask(askCase{name: "no-force", want: askHolds, check: askReachedShellEvent})
}

func (s *cursorSuite) TestAskForce() {
	s.ask(askCase{name: "force", want: askHolds, args: []string{"--force"}, check: askReachedShellEvent})
}

// opencodeDriver points XDG_CONFIG_HOME at a temp dir, so
// ~/.config/opencode/plugins stays out. Auth stays in XDG_DATA_HOME.
func opencodeDriver() *hostDriver {
	h := &hostDriver{id: "opencode", binary: "opencode", model: envModel("OPENCODE", "opencode/big-pickle")}
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
		copyFile(t, checkoutPath(t, "docs/guides/integrations/agents/opencode-plugin.ts"),
			filepath.Join(w.dir, ".opencode", "plugins", "magus.ts"), 0o644)
		// The local server `magus describe harness opencode` describes in
		// prose, merged into whatever opencode.json the harness merge wrote.
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
	h.turn = func(t *testing.T, w *workspace, conv *conversation, prompt string) (string, e2e.Result) {
		run := w.runHost(t, turnLabel(conv), h.install.Path, []string{"run", "--format", "json", "--dir", w.dir, "--auto", "-m", h.model, prompt})
		return opencodeText(run.Stdout), run
	}
	h.askTurn = func(t *testing.T, w *workspace, c askCase) askRun {
		args := append([]string{"run", "--format", "json", "--dir", w.dir, "-m", h.model}, c.args...)
		return askHeadless(t, w, c, h.install.Path, append(args, askPrompt))
	}
	return h
}

// opencodeText joins the text parts of `opencode run --format json` events.
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

type openCodeSuite struct{ hostSuite }

func (s *openCodeSuite) TestBriefDelivered() {
	s.skipScenario("brief-delivered", "OpenCode offers no context channel after a compaction; its guide documents that the plugin hands a compacted session nothing")
}

func (s *openCodeSuite) TestAskDefault() {
	s.ask(askCase{name: "default", want: askHolds})
}

func (s *openCodeSuite) TestAskAuto() {
	s.ask(askCase{name: "auto", want: askHolds, args: []string{"--auto"}})
}
