package render

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"text/template"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/libs/clikit/output"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

func TestWriteFormattedJSON(t *testing.T) {
	var b bytes.Buffer
	require.NoError(t, WriteFormatted(&b, output.Options{Format: output.JSON}, struct {
		X int `json:"x"`
	}{X: 7}))
	assert.Contains(t, b.String(), `"x": 7`)
}

func TestTemplateFuncsExcludesTheNonHermetic(t *testing.T) {
	funcs := TemplateFuncs()
	for _, name := range excludedSprigFuncs {
		assert.NotContains(t, funcs, name)
	}
	assert.Contains(t, funcs, "join")
}

func TestTemplateJoinTakesAnySlice(t *testing.T) {
	assert.Equal(t, "", templateJoin(nil, ","))
	assert.Equal(t, "a,b", templateJoin([]string{"a", "b"}, ","))
	assert.Equal(t, "1,2", templateJoin([]any{1, 2}, ","))
	assert.Equal(t, "x", templateJoin("x", ","))
}

// The SessionStart entry runs from the root with -C, and that root is what goes on PATH.
func TestSessionGlueAppendsTheWorkDirToTheEnvFile(t *testing.T) {
	repo := gluePath(t)
	envFile := filepath.Join(t.TempDir(), "env")
	require.NoError(t, os.WriteFile(envFile, []byte("export A=1\n"), 0o644))

	// -C repo, as the wiring passes it: the script runs from the root.
	t.Chdir(repo)
	glue := []string{"docs/guides/integrations/agents/magus-session.buzz"}
	require.NoError(t, glueRunner().Run(t.Context(), "", interp.ScriptOptions{}, "", glue, []string{"--env-file", envFile}, true))
	require.NoError(t, glueRunner().Run(t.Context(), "", interp.ScriptOptions{}, "", glue, []string{"--env-file", ""}, true), "no env file is nothing to do")

	body, err := os.ReadFile(envFile)
	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf("export A=1\nexport PATH=\"%s:$PATH\"\n", repo), string(body))
}

// glueRepo is the repository root, resolved on the first call. Every test reaches it
// before runGlue changes directory, and t.Chdir puts the directory back after each.
var glueRepo = sync.OnceValues(func() (string, error) { return filepath.Abs(filepath.Join("..", "..")) })

func gluePath(t testing.TB, rel ...string) string {
	t.Helper()
	root, err := glueRepo()
	require.NoError(t, err)
	return filepath.Join(append([]string{root}, rel...)...)
}

// glueHostSchemaDir holds the host schemas the glue's replies are graded against;
// testdata/hosts/SOURCES.md says which a host published and which magus wrote.
func glueHostSchemaDir(t testing.TB) string {
	return gluePath(t, "internal", "render", "testdata", "hosts")
}

// glueTemplateDir is the shipped glue.
func glueTemplateDir(t testing.TB) string {
	return gluePath(t, "docs", "guides", "integrations", "agents")
}

// glueRunner runs a glue file the way `magus buzz -s` does outside a workspace: the host
// modules installed, no workspace to open, and the warnings dropped.
func glueRunner() *interp.ScriptRunner {
	return &interp.ScriptRunner{
		Open:    func(context.Context, string) (*interp.ScriptWorkspace, error) { return nil, nil },
		Adopted: func(context.Context) bool { return false },
		Quiet:   true,
		Host: interp.ScriptHost{
			RegisterModules: func(ctx context.Context, sess *buzz.Session, out io.Writer) {
				bindings.RegisterModules(ctx, sess, bindings.WithScriptOutput(out))
			},
			RegisterSpellDecls:     bindings.RegisterSpellDecls,
			RegisterMagusNamespace: bindings.RegisterMagusNamespace,
			DeclareMagusTypes:      bindings.DeclareMagusTypes,
		},
	}
}

// withStdin feeds text to the process's standard input for the duration of fn, which
// is what lets a hook script be driven the way its host drives it.
func withStdin(t *testing.T, text string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	go func() {
		_, _ = w.WriteString(text)
		_ = w.Close()
	}()
	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = prev })
	fn()
}

// glueHostOutputSchema names the schema each host's hook stdout is graded against.
var glueHostOutputSchema = map[string]string{
	"claude-code": "claude-code/hook-output.schema.json",
	"codex":       "codex/pre-tool-use.command.output.schema.json",
}

// glueRawObject matches a JSON object a glue file holds as a raw string constant. Those
// with a template action or a placeholder the glue splices a value into are not replies
// yet; the rest are printed as they stand.
var glueRawObject = regexp.MustCompile("= `(\\{\"[^`]*)`;")

// The reply to Codex's approval request answers its own event, with its own schema.
const (
	gluePermissionRequestEvent  = `"hookEventName":"PermissionRequest"`
	gluePermissionRequestSchema = "codex/permission-request.command.output.schema.json"
)

// glueCoverageHost reads the host out of a glue file's `magus-guard-coverage:` marker, so
// a glue that learns a new host demands that host's schema on the next run.
var glueCoverageHost = regexp.MustCompile(`magus-guard-coverage:.*\bhost=(\S+)`)

// glueVerdicts are the decisions `magus shell` renders, plus one it never does. The values
// are fixed here rather than taken from a run so the rendered bytes belong to the test, and
// the reason carries the characters a naive template would break on. The unknown decision
// stands in for a contract that grew after a copy was installed: it must never render as
// an allow.
var glueVerdicts = []map[string]any{
	{"decision": "deny", "reason": `git stash is denied here: "quoted" & <angled>`},
	{"decision": "advise", "context": "magus workspace: `magus refs <sym>` for code"},
	{"decision": "pass"},
	{"decision": "ask", "reason": `pushing abc1234, which no passing gate covers: "quoted" & <angled>`},
	{"decision": "maybe", "reason": "a decision no template knows"},
}

// glueArrangement is one way a host reaches the shared glue: the host its wiring names,
// the environment it sets, the event it sends, and whether a Codex prompt rule sits in
// the project.
type glueArrangement struct {
	label string
	// host is the name the wiring passes as --agent-name, the only place the glue reads
	// one. Every arrangement names one: glue given none never assembles a reply for magus
	// to render, because magus refuses it (MGS3024), which the transport cases pin.
	host  string
	env   map[string]string
	event string
	rules bool
	// codex marks an arrangement whose PreToolUse reply must never carry permissionDecision
	// "ask": Codex parses it, reports the hook failed, and runs the call anyway.
	codex bool
	// ownResponse is a reader-written HOST_RESPONSE, which cannot claim --renders-ask.
	ownResponse bool
	// wantsAsk marks an arrangement whose ask must render as the host's own prompt.
	wantsAsk bool
	// stub is the script standing in for magus; empty means glueTemplateEcho.
	stub string
}

var glueArrangements = []glueArrangement{
	{label: "claude-code", host: "claude-code", event: `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "claude-code without the advise arm", host: "claude-code", env: map[string]string{"__MAGUS_NO_ADVISE": "1"}, event: `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "codex with no prompt rule", host: "codex", codex: true, event: `{"hook_event_name":"PreToolUse","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "codex with its prompt rule", host: "codex", codex: true, rules: true, event: `{"hook_event_name":"PreToolUse","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "codex in a mode that never prompts", host: "codex", codex: true, rules: true, event: `{"hook_event_name":"PreToolUse","session_id":"s","permission_mode":"bypassPermissions","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "codex approval request for a push", host: "codex", codex: true, rules: true, event: `{"hook_event_name":"PermissionRequest","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git push origin HEAD"}}`},
	{label: "codex approval request for anything else", host: "codex", codex: true, rules: true, event: `{"hook_event_name":"PermissionRequest","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"rm -rf build"}}`},
	{label: "codex with a human-only run's prompt rule", host: "codex", codex: true, rules: true, event: `{"hook_event_name":"PreToolUse","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"magus run agent-bench-run .","file_path":"README.md"}}`},
	{label: "codex approval request for a human-only run", host: "codex", codex: true, rules: true, event: `{"hook_event_name":"PermissionRequest","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"magus run agent-bench-run ."}}`},
	// The payload never overrides the name: an event carrying Codex's turn_id, under a
	// wiring that names claude-code, gets Claude Code's ask.
	{label: "claude-code named, Codex-shaped event", host: "claude-code", wantsAsk: true, event: `{"hook_event_name":"PreToolUse","session_id":"s","turn_id":"t","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "a reader's own HOST_RESPONSE", host: "claude-code", ownResponse: true, env: map[string]string{"HOST_RESPONSE": `{{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}}{{end}}`}, event: `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
}

// glueTemplateEcho stands in for magus: it prints the template it was handed and nothing
// else, so what the glue ASSEMBLES for a host is read from the glue running. It records
// whether the call claimed --renders-ask in the file $RENDERS_ASK_RECORD names.
const glueTemplateEcho = "#!/bin/sh\nclaim=no\nfor a in \"$@\"; do [ \"$a\" = --renders-ask ] && claim=yes; done\nprintf '%s' \"$claim\" > \"$RENDERS_ASK_RECORD\"\n" +
	"while [ $# -gt 0 ]; do\n  if [ \"$1\" = -o ]; then printf '%s' \"${2#template=}\"; exit 0; fi\n  shift\ndone\nexit 1\n"

// glueVerdictEcho stands in for magus on a glue that asks for `-o json` and renders the
// reply itself: it prints the verdict named by $GLUE_VERDICT and nothing else.
const glueVerdictEcho = "#!/bin/sh\nclaim=no\nfor a in \"$@\"; do [ \"$a\" = --renders-ask ] && claim=yes; done\nprintf '%s' \"$claim\" > \"$RENDERS_ASK_RECORD\"\nprintf '%s' \"$GLUE_VERDICT\"\n"

func glueLoadSchema(t *testing.T, rel string) *jsonschema.Resolved {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(glueHostSchemaDir(t), rel))
	require.NoError(t, err, "read the vendored schema %s", rel)
	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(raw, &schema), "%s must parse as a JSON Schema", rel)
	// nil loader on purpose: a schema that grew a remote $ref would fail here rather
	// than turn every run of this test into an HTTP request.
	resolved, err := schema.Resolve(nil)
	require.NoError(t, err, "%s must resolve with no loader", rel)
	return resolved
}

func glueDecode(t *testing.T, label, body string) any {
	t.Helper()
	var doc any
	require.NoError(t, json.Unmarshal([]byte(body), &doc), "%s must be valid JSON: %s", label, body)
	return doc
}

// runGlue runs one shipped glue file under `magus buzz`, as a host config wires it,
// against event, with magus replaced by the arrangement's stub. It returns what the glue
// printed and whether its call to magus claimed --renders-ask. Call it from a subtest:
// it sets the environment and working directory for the rest of the test it runs in.
func runGlue(t *testing.T, name string, a glueArrangement) (string, bool) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("the stand-in for magus is a POSIX sh script, and sh is not installed")
	}
	dir := t.TempDir()
	if a.rules {
		rules := filepath.Join(dir, ".codex", "rules", "magus.rules")
		require.NoError(t, os.MkdirAll(filepath.Dir(rules), 0o755))
		require.NoError(t, os.WriteFile(rules, []byte("prefix_rule(pattern = [\"git\", \"push\"], decision = \"prompt\")\n"+
			"prefix_rule(pattern = [\"magus\", \"run\", \"agent-bench-run\"], decision = \"prompt\")\n"), 0o644))
	}
	stub := a.stub
	if stub == "" {
		stub = glueTemplateEcho
	}
	bin := testkit.NewWorkspace(t).WriteExecutable("magus", stub)
	record := filepath.Join(dir, "renders-ask")
	t.Setenv("__MAGUS_BIN", bin)
	t.Setenv("RENDERS_ASK_RECORD", record)
	t.Setenv("TMPDIR", t.TempDir())
	for k, v := range a.env {
		t.Setenv(k, v)
	}
	script := filepath.Join(glueTemplateDir(t), name)
	t.Chdir(dir)

	var runErr error
	var reply string
	withStdin(t, a.event, func() {
		reply = testkit.CaptureStdout(t, func() {
			runErr = glueRunner().Run(t.Context(), dir, interp.ScriptOptions{}, "", []string{script}, []string{"--agent-name", a.host}, true)
		})
	})
	require.NoError(t, runErr, "%s (%s) must never fail: its host reads a non-zero exit as an error", name, a.label)
	claim, err := os.ReadFile(record)
	require.NoError(t, err, "%s (%s) never called magus", name, a.label)
	return reply, string(claim) == "yes"
}

// TestGlueRepliesValidateAgainstTheirHostSchema grades the bytes a host actually receives
// from the shared glue: the template each glue file assembles for an arrangement, rendered
// on every verdict, validated against the schema the host's stdout answers to.
//
// Rendered from the glue running rather than from a copy pasted here, so an edit to a
// glue file is graded on the next run instead of drifting away from a fixture nobody
// updates.
func TestGlueRepliesValidateAgainstTheirHostSchema(t *testing.T) {
	funcs := TemplateFuncs()
	for _, name := range []string{"magus-command.buzz", "magus-path.buzz"} {
		raw, err := os.ReadFile(filepath.Join(glueTemplateDir(t), name))
		require.NoError(t, err)
		hosts := map[string]bool{}
		for _, match := range glueCoverageHost.FindAllStringSubmatch(string(raw), -1) {
			hosts[match[1]] = true
		}
		require.NotEmpty(t, hosts, "%s declares no magus-guard-coverage host, so nothing says whose schema grades it", name)

		// The replies the glue prints without asking magus at all: the unreadable-event
		// deny and the approval reply that leaves the prompt to the person.
		for _, match := range glueRawObject.FindAllStringSubmatch(string(raw), -1) {
			literal := match[1]
			if strings.Contains(literal, "{{") || strings.Contains(literal, "__MAGUS_") {
				continue
			}
			for host := range hosts {
				schemaFile := glueHostOutputSchema[host]
				if strings.Contains(literal, gluePermissionRequestEvent) {
					if host != "codex" {
						continue
					}
					schemaFile = gluePermissionRequestSchema
				}
				require.NoError(t, glueLoadSchema(t, schemaFile).Validate(glueDecode(t, name, literal)),
					"%s prints %s, which %s would not accept, per %s", name, literal, host, schemaFile)
			}
		}

		for _, a := range glueArrangements {
			// A glue answers only the hosts it declares; which hosts wire it is the harness
			// spells' to say, and their own tests hold it.
			if !hosts[a.host] {
				continue
			}
			t.Run(fmt.Sprintf("%s/%s", name, a.label), func(t *testing.T) {
				body, rendersAsk := runGlue(t, name, a)
				require.NotEmpty(t, body, "%s (%s) handed magus no template", name, a.label)
				// The claim is what lets magus return an ask at all, so it must track exactly
				// the replies that render one: every reply the glue assembles, and none a
				// reader wrote.
				assert.Equal(t, !a.ownResponse, rendersAsk,
					"%s (%s): --renders-ask claimed=%v; the glue's own reply must claim it and a reader's must not", name, a.label, rendersAsk)
				tmpl, err := template.New(name).Funcs(funcs).Parse(body)
				require.NoError(t, err, "%s (%s) renders its verdict through this template, so it must parse", name, a.label)

				for _, verdict := range glueVerdicts {
					var rendered strings.Builder
					require.NoError(t, tmpl.Execute(&rendered, verdict), "%s (%s) on a %s", name, a.label, verdict["decision"])
					text := rendered.String()
					// A reader's own reply is theirs to get right; magus sends it no ask.
					decision := verdict["decision"]
					if a.ownResponse {
						decision = ""
					}
					switch decision {
					case "maybe":
						assert.Contains(t, text, "deny", "%s (%s) must refuse a decision it does not know, never allow it", name, a.label)
					case "ask":
						assert.NotEmpty(t, text, "%s (%s) renders an ask as nothing, which the host takes as allow", name, a.label)
						if a.codex {
							assert.NotContains(t, text, `"permissionDecision":"ask"`,
								"%s (%s): Codex parses a hook ask, marks the hook failed, and runs the call", name, a.label)
						}
						if a.wantsAsk {
							assert.Contains(t, text, `"permissionDecision":"ask"`,
								"%s (%s): the wiring's host decides the arm, never the event's shape", name, a.label)
						}
					}
					if !strings.HasPrefix(text, "{") {
						continue
					}
					if strings.Contains(text, gluePermissionRequestEvent) {
						require.True(t, hosts["codex"], "%s answers a PermissionRequest but declares no codex coverage", name)
						require.NoError(t, glueLoadSchema(t, gluePermissionRequestSchema).Validate(glueDecode(t, name, text)),
							"%s prints %s, which codex would not accept, per %s", name, text, gluePermissionRequestSchema)
						continue
					}
					schemaFile, ok := glueHostOutputSchema[a.host]
					require.True(t, ok, "%s answers %s and no schema grades that host's stdout", name, a.host)
					require.True(t, hosts[a.host], "%s is wired for %s but declares no coverage for it", name, a.host)
					require.NoError(t, glueLoadSchema(t, schemaFile).Validate(glueDecode(t, name, text)),
						"%s prints %s, which %s would not accept, per %s", name, text, a.host, schemaFile)
				}
			})
		}
	}
}

// TestGlueLeavesAHumanOnlyRunToCodexsPrompt pins that a plain run of a target the rules file
// prompts for reaches the person through Codex's own prompt, as a push does: PreToolUse
// carries the ask as context rather than refusing it, and the approval request leaves the
// prompt standing. A run no rule matches is refused, naming the person's terminal.
func TestGlueLeavesAHumanOnlyRunToCodexsPrompt(t *testing.T) {
	render := func(t *testing.T, command, event, decision string) string {
		t.Helper()
		body, _ := runGlue(t, "magus-command.buzz", glueArrangement{host: "codex", codex: true, rules: true,
			event: fmt.Sprintf(`{"hook_event_name":"%s","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"%s"}}`, event, command)})
		tmpl, err := template.New("glue").Funcs(TemplateFuncs()).Parse(body)
		require.NoError(t, err)
		var out strings.Builder
		require.NoError(t, tmpl.Execute(&out, map[string]string{"decision": decision, "reason": "a person runs this"}))
		return out.String()
	}
	t.Run("prompted run", func(t *testing.T) {
		got := render(t, "magus run agent-bench-run .", "PreToolUse", "ask")
		assert.NotContains(t, got, `"permissionDecision":"deny"`)
		assert.Contains(t, got, "a person runs this")
	})
	t.Run("approval request for a prompted run", func(t *testing.T) {
		assert.Equal(t, `{"hookSpecificOutput":{"hookEventName":"PermissionRequest"}}`, render(t, "magus run agent-bench-run .", "PermissionRequest", "ask"))
		assert.Contains(t, render(t, "magus run agent-bench-run .", "PermissionRequest", "pass"), `"behavior":"allow"`)
	})
	t.Run("run no rule prompts for", func(t *testing.T) {
		got := render(t, "magus run lint .", "PreToolUse", "ask")
		assert.Contains(t, got, `"permissionDecision":"deny"`)
		assert.Contains(t, got, "own terminal")
		assert.NotContains(t, render(t, "magus run lint .", "PermissionRequest", "pass"), "allow",
			"a pass is not consent to a prompt magus did not configure")
	})
}

// TestGlueNeverRewritesTheCommand pins that no host is handed updatedInput: a pass renders
// nothing and an advise renders its context alone, whatever the host or the tool. Claude
// Code and Cursor already give a shell command a closed stdin, Codex never did, and a
// rewrite would take a command out of reach of the person's own permission rules.
func TestGlueNeverRewritesTheCommand(t *testing.T) {
	funcs := TemplateFuncs()
	const input = `{"command":"git push","description":"push it","timeout":60000,"run_in_background":true}`
	for _, tc := range []struct{ label, host, event string }{
		{"claude-code", "claude-code", fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":%s}`, input)},
		{"codex", "codex", fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":%s}`, input)},
		{"a restated read", "claude-code", `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Read","tool_input":{"file_path":"README.md"}}`},
	} {
		t.Run(tc.label, func(t *testing.T) {
			body, _ := runGlue(t, "magus-command.buzz", glueArrangement{label: tc.label, host: tc.host, event: tc.event, codex: tc.host == "codex"})
			tmpl, err := template.New(tc.label).Funcs(funcs).Option("missingkey=error").Parse(body)
			require.NoError(t, err)

			var pass strings.Builder
			require.NoError(t, tmpl.Execute(&pass, map[string]any{"decision": "pass"}))
			assert.Empty(t, pass.String(), "a pass renders nothing")

			var advise strings.Builder
			require.NoError(t, tmpl.Execute(&advise, map[string]any{"decision": "advise", "context": "said once"}))
			assert.JSONEq(t, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"said once"}}`, advise.String())
			assert.NotContains(t, advise.String(), "updatedInput")
			require.NoError(t, glueLoadSchema(t, glueHostOutputSchema[tc.host]).Validate(glueDecode(t, tc.label, advise.String())))
		})
	}
}

// glueCursorEvents are the two Cursor events that read a reply the guard renders, and
// the schema each one's stdout answers to. Cursor validates each event's stdout with a
// different function, and its gating events carry no advisory channel, so the guard
// answers two events with two shapes.
var glueCursorEvents = map[string]struct {
	event, schema string
}{
	"gate":   {`{"hook_event_name":"beforeShellExecution","conversation_id":"c","command":"git push","cwd":"."}`, "cursor/hook-output.schema.json"},
	"advise": {`{"hook_event_name":"postToolUse","conversation_id":"c","tool_name":"Shell","tool_input":{"command":"git push"}}`, "cursor/post-tool-use.output.schema.json"},
}

// glueSchemaPropertyNames reads the field names a vendored schema declares, out of the
// raw JSON: the question is what the host NAMES, and jsonschema-go validates rather than
// introspects.
func glueSchemaPropertyNames(t *testing.T, rel string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(glueHostSchemaDir(t), rel))
	require.NoError(t, err, "read the vendored schema %s", rel)
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema))
	require.NotEmpty(t, schema.Properties, "%s must name the fields it grades", rel)
	return schema.Properties
}

func TestCursorGrepReachesGuardWithItsOptions(t *testing.T) {
	for _, event := range []string{"preToolUse", "postToolUse"} {
		t.Run(event, func(t *testing.T) {
			input := map[string]any{
				"pattern": "func HandleRequest", "path": "internal/api/handler.go",
				"glob": "*.go", "type": "go", "output_mode": "content",
				"-A": 20, "-B": 2, "-C": 3, "-i": true, "multiline": true,
			}
			raw, err := json.Marshal(map[string]any{
				"hook_event_name": event, "conversation_id": "grep-session",
				"tool_name": "Grep", "tool_input": input, "cwd": ".",
			})
			require.NoError(t, err)
			record := filepath.Join(t.TempDir(), "guard-input")
			out, _ := runGlue(t, "cursor-hook.buzz", glueArrangement{
				label: event, host: "cursor", event: string(raw),
				stub: "#!/bin/sh\ncat > \"$GREP_INPUT_RECORD\"\nprintf no > \"$RENDERS_ASK_RECORD\"\nprintf '%s' \"$GLUE_VERDICT\"\n",
				env: map[string]string{
					"GREP_INPUT_RECORD":     record,
					"GLUE_VERDICT":          `{"decision":"advise","context":"use magus refs HandleRequest --source"}`,
					"CURSOR_HOOK_STATE_DIR": t.TempDir(),
				},
			})
			judged, err := os.ReadFile(record)
			require.NoError(t, err)
			var payload struct {
				ToolName  string          `json:"tool_name"`
				Input     json.RawMessage `json:"tool_input"`
				SessionID string          `json:"session_id"`
				Cwd       string          `json:"cwd"`
			}
			require.NoError(t, json.Unmarshal(judged, &payload), "the native search must reach the shared guard as a tool call")
			want, err := json.Marshal(input)
			require.NoError(t, err)
			gotInput := payload.Input
			payload.Input = nil
			assert.Equal(t, struct {
				ToolName  string          `json:"tool_name"`
				Input     json.RawMessage `json:"tool_input"`
				SessionID string          `json:"session_id"`
				Cwd       string          `json:"cwd"`
			}{ToolName: "Grep", SessionID: "grep-session", Cwd: "."}, payload)
			assert.JSONEq(t, string(want), string(gotInput), "context and scope must survive the Cursor adapter")
			if event == "preToolUse" {
				assert.Equal(t, "allow", glueDecode(t, event, out).(map[string]any)["permission"])
			} else {
				assert.Contains(t, out, "use magus refs HandleRequest --source")
			}
		})
	}
}

// cursorReadingListSuite runs cursor-hook.buzz on a WebSearch with a stand-in
// magus that answers config view with a reading list and the link query with
// citations.
type cursorReadingListSuite struct {
	suite.Suite
}

func TestCursorReadingListSuite(t *testing.T) {
	suite.Run(t, new(cursorReadingListSuite))
}

const cursorReadingListStub = "#!/bin/sh\nprintf no > \"$RENDERS_ASK_RECORD\"\n" +
	"case \"$1\" in\n  config) [ -n \"$CONFIG_FAILS\" ] && exit 2; printf '%s' \"$SOURCES_JSON\" ;;\n" +
	"  query) printf '%s' \"$LINKS\" ;;\nesac\n"

// hint is the additional_context the hook returns for a search of terms.
func (s *cursorReadingListSuite) hint(terms string, env map[string]string) string {
	event, err := json.Marshal(map[string]any{
		"hook_event_name": "postToolUse", "conversation_id": "c",
		"tool_name": "WebSearch", "tool_input": map[string]any{"search_term": terms},
	})
	s.Require().NoError(err)
	env["CURSOR_HOOK_STATE_DIR"] = s.T().TempDir()
	out, _ := runGlue(s.T(), "cursor-hook.buzz", glueArrangement{
		label: terms, host: "cursor", event: string(event), stub: cursorReadingListStub, env: env,
	})
	var reply struct {
		Context string `json:"additional_context"`
	}
	s.Require().NoError(json.Unmarshal([]byte(out), &reply), out)
	return reply.Context
}

const cursorReadingList = `{"sources":[
{"url":"https://blog.example/go","topic":"go modules","verdict":"caveat","why":"predates workspaces"},
{"url":"https://spam.example/","topic":"go modules","verdict":"skip","why":"SEO"},
{"url":"https://go.dev/ref/mod","topic":"go modules","verdict":"follow","why":"the reference"},
{"url":"https://react.dev/","topic":"react hooks","verdict":"follow","why":""}]}`

func (s *cursorReadingListSuite) TestRanksFollowBeforeCaveatAndDropsSkip() {
	got := s.hint("go modules vendoring", map[string]string{"SOURCES_JSON": cursorReadingList})
	s.Equal("Your reading list (sources in the user magus.yaml) covers this search. "+
		"Prefer follow; read a caveat with its why in mind:\n"+
		"  - follow https://go.dev/ref/mod\n"+
		"  - caveat https://blog.example/go: predates workspaces", got)
}

func (s *cursorReadingListSuite) TestDropsACitationUnderASkipEntry() {
	got := s.hint("go modules", map[string]string{
		"SOURCES_JSON": cursorReadingList,
		"LINKS":        "link:https://spam.example/go-modules\nlink:https://pkg.go.dev/cmd/go\n",
	})
	s.Contains(got, "  - https://pkg.go.dev/cmd/go")
	s.NotContains(got, "spam.example")
}

func (s *cursorReadingListSuite) TestNamesNothingForAnUnrelatedSearch() {
	s.Empty(s.hint("rust lifetimes", map[string]string{"SOURCES_JSON": cursorReadingList}))
}

func (s *cursorReadingListSuite) TestAWordInsideALongerWordNamesNothing() {
	s.Empty(s.hint("mongo modules", map[string]string{"SOURCES_JSON": cursorReadingList}))
}

func (s *cursorReadingListSuite) TestSaysSoWhenConfigViewFails() {
	got := s.hint("go modules", map[string]string{"CONFIG_FAILS": "1"})
	s.Equal("Your reading list (sources in the user magus.yaml) was not applied: magus config view exited 2.", got)
}

// TestCursorGlueRepliesValidateAgainstTheEventThatReadsThem grades what cursor-hook.buzz
// answers, per event.
//
// Both halves of the check matter and only one of them is a schema. Validating catches a
// field whose TYPE moved. The subset assertion catches a field Cursor RENAMED, which
// validating cannot: Cursor's stdout validators read the fields they know and ignore the
// rest, so a reply naming additional_context_v2 is accepted, ignored, and carries no
// advisory at all.
func TestCursorGlueRepliesValidateAgainstTheEventThatReadsThem(t *testing.T) {
	for name, ev := range glueCursorEvents {
		schema := glueLoadSchema(t, ev.schema)
		named := glueSchemaPropertyNames(t, ev.schema)
		for i, verdict := range glueVerdicts {
			t.Run(fmt.Sprintf("%s/%d-%s", name, i, verdict["decision"]), func(t *testing.T) {
				verdictJSON, err := json.Marshal(verdict)
				require.NoError(t, err)
				out, _ := runGlue(t, "cursor-hook.buzz", glueArrangement{
					label: name, host: "cursor", event: ev.event, stub: glueVerdictEcho,
					env: map[string]string{
						"GLUE_VERDICT":          string(verdictJSON),
						"CURSOR_HOOK_STATE_DIR": t.TempDir(),
					},
				})
				reply, isObject := glueDecode(t, name, out).(map[string]any)
				require.True(t, isObject, "the %s reply must be a JSON object: %s", name, out)
				switch name {
				case "gate":
					switch verdict["decision"] {
					case "ask":
						assert.Equal(t, "ask", reply["permission"], "an ask is Cursor's own approval prompt")
					case "pass", "advise":
						assert.Equal(t, "allow", reply["permission"])
					default:
						assert.Equal(t, "deny", reply["permission"], "only pass and advise may allow; %s must not", verdict["decision"])
					}
				case "advise":
					if verdict["decision"] == "advise" {
						assert.Equal(t, verdict["context"], reply["additional_context"], "the advice must reach the model")
					} else {
						assert.NotContains(t, reply, "additional_context", "only an advise carries context")
					}
				}
				require.NoError(t, schema.Validate(reply),
					"the %s reply is %s, which Cursor would not accept, per %s", name, out, ev.schema)
				for field := range reply {
					assert.Contains(t, named, field,
						"the %s reply names %q, which %s does not. Cursor ignores a field it has never\n"+
							"heard of rather than rejecting it, so a rename costs the verdict in silence.",
						name, field, ev.schema)
				}
			})
		}
	}
}

// hookAudienceSuite pins decision 110 per host: a deny shows the person its first line, and
// the agent gets the rest through the host's agent-only channel.
type hookAudienceSuite struct {
	suite.Suite
}

func TestHookAudienceSuite(t *testing.T) {
	suite.Run(t, new(hookAudienceSuite))
}

const audienceReason = "a text search the graph answers\nnothing ran (2 commands)\ndetails: ./magus query output grd1"

// render runs the Claude Code / Codex glue on event and renders its template on verdict.
func (s *hookAudienceSuite) render(host, event string, verdict map[string]any) map[string]any {
	body, _ := runGlue(s.T(), "magus-command.buzz", glueArrangement{label: host, host: host, event: event, codex: host == "codex"})
	tmpl, err := template.New(host).Funcs(TemplateFuncs()).Option("missingkey=error").Parse(body)
	s.Require().NoError(err)
	var out strings.Builder
	s.Require().NoError(tmpl.Execute(&out, verdict))
	reply, ok := glueDecode(s.T(), host, out.String()).(map[string]any)
	s.Require().True(ok, out.String())
	return reply
}

func (s *hookAudienceSuite) TestClaudeCodeDenyShowsOneLineAndHandsTheAgentTheRest() {
	reply := s.render("claude-code", `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{"command":"git stash"}}`,
		map[string]any{"decision": "deny", "reason": audienceReason})
	out := reply["hookSpecificOutput"].(map[string]any)
	s.Equal("a text search the graph answers", out["permissionDecisionReason"])
	s.Equal("nothing ran (2 commands)\ndetails: ./magus query output grd1", out["additionalContext"])
}

func (s *hookAudienceSuite) TestCodexDenyShowsOneLineAndHandsTheAgentTheRest() {
	reply := s.render("codex", `{"hook_event_name":"PreToolUse","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git stash"}}`,
		map[string]any{"decision": "deny", "reason": audienceReason})
	out := reply["hookSpecificOutput"].(map[string]any)
	s.Equal("a text search the graph answers", out["permissionDecisionReason"])
	s.Equal("nothing ran (2 commands)\ndetails: ./magus query output grd1", out["additionalContext"])
	s.Require().NoError(glueLoadSchema(s.T(), glueHostOutputSchema["codex"]).Validate(reply))
}

func (s *hookAudienceSuite) TestAOneLineDenyCarriesNoAgentContext() {
	reply := s.render("claude-code", `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{"command":"git stash"}}`,
		map[string]any{"decision": "deny", "reason": "no"})
	out := reply["hookSpecificOutput"].(map[string]any)
	s.Equal("no", out["permissionDecisionReason"])
	s.NotContains(out, "additionalContext")
}

func (s *hookAudienceSuite) TestAStopBlockShowsOneLineAndHandsTheAgentTheRest() {
	reply := s.render("claude-code", `{"hook_event_name":"Stop","session_id":"s","stop_hook_active":false}`,
		map[string]any{"decision": "deny", "reason": audienceReason})
	s.Equal("block", reply["decision"])
	s.Equal("a text search the graph answers", reply["reason"])
	out := reply["hookSpecificOutput"].(map[string]any)
	s.Equal("Stop", out["hookEventName"])
	s.Equal("nothing ran (2 commands)\ndetails: ./magus query output grd1", out["additionalContext"])
}

func (s *hookAudienceSuite) TestASubagentStopBlockNamesItsOwnEvent() {
	reply := s.render("claude-code", `{"hook_event_name":"SubagentStop","session_id":"s","agent_id":"a","stop_hook_active":false}`,
		map[string]any{"decision": "deny", "reason": audienceReason})
	s.Equal("SubagentStop", reply["hookSpecificOutput"].(map[string]any)["hookEventName"])
}

func (s *hookAudienceSuite) TestACodexStopBlockKeepsTheWholeReason() {
	reply := s.render("codex", `{"hook_event_name":"SubagentStop","session_id":"s","agent_id":"a","stop_hook_active":false}`,
		map[string]any{"decision": "deny", "reason": audienceReason})
	s.Equal(audienceReason, reply["reason"])
	s.NotContains(reply, "hookSpecificOutput")
	s.Require().NoError(glueLoadSchema(s.T(), "codex/stop.command.output.schema.json").Validate(reply))
}

func (s *hookAudienceSuite) TestCursorDenyShowsThePersonOneLine() {
	verdict, err := json.Marshal(types.GuardJudgment{APIVersion: agent.GuardAPIVersion, Decision: "deny", Reason: audienceReason})
	s.Require().NoError(err)
	s.T().Setenv("GLUE_VERDICT", string(verdict))
	body, _ := runGlue(s.T(), "cursor-hook.buzz", glueArrangement{label: "cursor", host: "cursor", stub: glueVerdictEcho,
		event: `{"hook_event_name":"beforeShellExecution","conversation_id":"c","generation_id":"g","command":"git stash","cwd":"/tmp"}`})
	reply, ok := glueDecode(s.T(), "cursor", body).(map[string]any)
	s.Require().True(ok, body)
	s.Equal("a text search the graph answers", reply["user_message"])
	s.Equal(audienceReason, reply["agent_message"])
}

// unjudgedHookSuite pins decision 114a: a call no magus judged fails closed, and the
// person is told how to get a magus on every such call.
type unjudgedHookSuite struct {
	suite.Suite
}

func TestUnjudgedHookSuite(t *testing.T) {
	suite.Run(t, new(unjudgedHookSuite))
}

// glueBrokenMagus stands in for a magus that runs but cannot judge.
const glueBrokenMagus = "#!/bin/sh\nprintf no > \"$RENDERS_ASK_RECORD\"\necho 'cannot load this tree' >&2\nexit 3\n"

func (s *unjudgedHookSuite) reply(name, host, event string) map[string]any {
	body, _ := runGlue(s.T(), name, glueArrangement{label: host, host: host, event: event, stub: glueBrokenMagus, codex: host == "codex"})
	reply, ok := glueDecode(s.T(), host, body).(map[string]any)
	s.Require().True(ok, body)
	return reply
}

func (s *unjudgedHookSuite) TestClaudeCodeRefusesACallThatMayChangeState() {
	reply := s.reply("magus-command.buzz", "claude-code", `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{"command":"git stash"}}`)
	out := reply["hookSpecificOutput"].(map[string]any)
	s.Equal("deny", out["permissionDecision"])
	s.Contains(out["permissionDecisionReason"], "cannot load this tree")
	s.Contains(reply["systemMessage"], "Rebuild or update", "the person reads the fix")
	s.Require().NoError(glueLoadSchema(s.T(), glueHostOutputSchema["claude-code"]).Validate(reply))
}

func (s *unjudgedHookSuite) TestClaudeCodeRefusesAReadAndTellsThePerson() {
	reply := s.reply("magus-command.buzz", "claude-code", `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Read","tool_input":{"file_path":"README.md"}}`)
	s.Equal("deny", reply["hookSpecificOutput"].(map[string]any)["permissionDecision"])
	s.Contains(reply["systemMessage"], "could not judge this call")
}

func (s *unjudgedHookSuite) TestTheSecondCallInASessionIsToldToo() {
	event := `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{"command":"ls"}}`
	s.reply("magus-command.buzz", "claude-code", event)
	reply := s.reply("magus-command.buzz", "claude-code", event)
	s.Contains(reply["systemMessage"], "could not judge this call")
}

func (s *unjudgedHookSuite) TestASilentFailureNamesItsExitCode() {
	body, _ := runGlue(s.T(), "magus-command.buzz", glueArrangement{label: "claude-code", host: "claude-code", stub: "#!/bin/sh\nprintf no > \"$RENDERS_ASK_RECORD\"\nexit 3\n",
		event: `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{"command":"git stash"}}`})
	reply, ok := glueDecode(s.T(), "claude-code", body).(map[string]any)
	s.Require().True(ok, body)
	s.Contains(reply["systemMessage"], "it printed no error (exit code 3)")
}

func (s *unjudgedHookSuite) TestCodexRefusalFitsItsSchema() {
	reply := s.reply("magus-command.buzz", "codex", `{"hook_event_name":"PreToolUse","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git stash"}}`)
	s.Equal("deny", reply["hookSpecificOutput"].(map[string]any)["permissionDecision"])
	s.Require().NoError(glueLoadSchema(s.T(), glueHostOutputSchema["codex"]).Validate(reply))
}

func (s *unjudgedHookSuite) TestCodexPermissionRequestKeepsThePromptAndTellsThePerson() {
	reply := s.reply("magus-command.buzz", "codex", `{"hook_event_name":"PermissionRequest","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git push"}}`)
	s.NotContains(reply["hookSpecificOutput"], "decision")
	s.Contains(reply["systemMessage"], "could not judge this call")
	s.Require().NoError(glueLoadSchema(s.T(), "codex/permission-request.command.output.schema.json").Validate(reply))
}

func (s *unjudgedHookSuite) TestAStopTellsThePersonRatherThanStderr() {
	reply := s.reply("magus-command.buzz", "claude-code", `{"hook_event_name":"Stop","session_id":"s","stop_hook_active":false}`)
	s.Contains(reply["systemMessage"], "could not judge this call")
	s.NotContains(reply, "decision")
}

func (s *unjudgedHookSuite) TestAPathWriteIsRefused() {
	reply := s.reply("magus-path.buzz", "claude-code", `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Write","tool_input":{"file_path":"README.md"}}`)
	s.Equal("deny", reply["hookSpecificOutput"].(map[string]any)["permissionDecision"])
}

func (s *unjudgedHookSuite) TestCursorRefusesAndShowsThePersonTheFix() {
	reply := s.reply("cursor-hook.buzz", "cursor", `{"hook_event_name":"beforeShellExecution","conversation_id":"c","generation_id":"g","command":"git stash","cwd":"/tmp"}`)
	s.Equal("deny", reply["permission"])
	s.Contains(reply["user_message"], "Rebuild or update")
}

const glueBrokenMultiLineMagus = "#!/bin/sh\nprintf no > \"$RENDERS_ASK_RECORD\"\necho 'magus.yaml has keys this magus does not know:' >&2\necho '  - guard.newest  (line 4)' >&2\nexit 3\n"

func (s *unjudgedHookSuite) bootstrapEvent(command string) string {
	encoded, err := json.Marshal(command)
	s.Require().NoError(err)
	return fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{"command":%s}}`, encoded)
}

// repairRootEnv and repairLinesEnv are the variables guard.ExportRepairLines fills for the
// hook glue, spelled out because the glue reads them by these names.
const (
	repairRootEnv  = "__MAGUS_REPAIR_ROOT"
	repairLinesEnv = "__MAGUS_REPAIR_LINES"
)

// suppliedRepair is a workspace whose repair lines magus handed the glue, as startup does.
func (s *unjudgedHookSuite) suppliedRepair() string {
	root := s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("{}\n"), 0o644))
	// Setenv first so the test restores both after ExportRepairLines overwrites them.
	s.T().Setenv(repairRootEnv, "")
	s.T().Setenv(repairLinesEnv, "")
	s.Require().NoError(guard.ExportRepairLines(root))
	return root
}

// repairEvent is a shell call from root.
func (s *unjudgedHookSuite) repairEvent(root, command string) string {
	encoded, err := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "session_id": "s", "tool_name": "Bash",
		"cwd": root, "tool_input": map[string]any{"command": command}})
	s.Require().NoError(err)
	return string(encoded)
}

func (s *unjudgedHookSuite) TestTheRepairLineAloneIsLetThroughAndTheNoticeStays() {
	reply := s.reply("magus-command.buzz", "claude-code", s.repairEvent(s.suppliedRepair(), "magus self update"))
	s.NotContains(reply["hookSpecificOutput"], "permissionDecision")
	s.Contains(reply["systemMessage"], "could not judge this call")
	s.Require().NoError(glueLoadSchema(s.T(), glueHostOutputSchema["claude-code"]).Validate(reply))
}

func (s *unjudgedHookSuite) TestAnotherWorkspacesRepairIsRefused() {
	s.suppliedRepair()
	reply := s.reply("magus-command.buzz", "claude-code", s.repairEvent(s.T().TempDir(), "magus self update"))
	s.Equal("deny", reply["hookSpecificOutput"].(map[string]any)["permissionDecision"])
	s.Contains(reply["systemMessage"], "magus did not supply the repair lines of")
}

func (s *unjudgedHookSuite) TestTheRepairLineWithAnotherCommandAfterItIsRefused() {
	reply := s.reply("magus-command.buzz", "claude-code", s.repairEvent(s.suppliedRepair(), "magus self update && git stash"))
	s.Equal("deny", reply["hookSpecificOutput"].(map[string]any)["permissionDecision"])
}

func (s *unjudgedHookSuite) TestASourceBuildIsRefusedWhereTheWorkspaceInstallsARelease() {
	reply := s.reply("magus-command.buzz", "claude-code", s.repairEvent(s.suppliedRepair(), ward.BootstrapCommand))
	s.Equal("deny", reply["hookSpecificOutput"].(map[string]any)["permissionDecision"])
}

func (s *unjudgedHookSuite) TestCursorLetsTheRepairLineThrough() {
	root := s.suppliedRepair()
	encoded, err := json.Marshal(map[string]any{"hook_event_name": "beforeShellExecution", "conversation_id": "c", "generation_id": "g",
		"command": "magus self update", "cwd": root})
	s.Require().NoError(err)
	reply := s.reply("cursor-hook.buzz", "cursor", string(encoded))
	s.Equal("allow", reply["permission"])
	s.Contains(reply["user_message"], "could not judge this call")
}

func (s *unjudgedHookSuite) TestEveryLineOfTheLoadErrorReachesThePerson() {
	body, _ := runGlue(s.T(), "magus-command.buzz", glueArrangement{label: "claude-code", host: "claude-code",
		event: s.bootstrapEvent("git stash"), stub: glueBrokenMultiLineMagus})
	reply, ok := glueDecode(s.T(), "claude-code", body).(map[string]any)
	s.Require().True(ok, body)
	s.Contains(reply["systemMessage"], "guard.newest")
}

func (s *unjudgedHookSuite) TestTheSessionStartTextPromisesNoGitStatus() {
	body, err := os.ReadFile(filepath.Join(glueTemplateDir(s.T()), "magus-session.buzz"))
	s.Require().NoError(err)
	s.NotContains(string(body), "`git status` and the fix still run")
}

// The hook reads its repair from the variable magus fills, never from a copy of the rules.
func (s *unjudgedHookSuite) TestTheHookReadsTheRepairMagusSupplies() {
	body, err := os.ReadFile(filepath.Join(glueTemplateDir(s.T()), "lib", "hook.buzz"))
	s.Require().NoError(err)
	s.Contains(string(body), fmt.Sprintf("final REPAIR_ENV = %q;", repairLinesEnv))
	s.NotContains(string(body), ward.BootstrapCommand)
}
