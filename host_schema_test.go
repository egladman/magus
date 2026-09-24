// cross-cutting: validates every shipped hook config and guard verdict against its host's schema

package magus

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"text/template"

	sprig "github.com/Masterminds/sprig/v3"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
)

// Conformance gates for the agent-host integration files, against the hosts' OWN
// schemas where a host publishes one.
//
// The sibling gates in conventions_test.go assert that these files exist, are
// embedded in their page, and declare a stance for every decision. None of them
// asks the question a user cares about first: would the host actually LOAD this?
// The agents magusfile named that gap in its own words: "the event SHAPES in the
// fixtures are recorded from each host's documentation, so a host silently renaming
// a field is still invisible here". This is what closes it. A schema is the one
// artifact that catches a rename, because it was written by the host.
//
// Two directions are graded. The CONFIG direction takes every hooks config magus
// ships or embeds and validates it against the host's config schema. The OUTPUT
// direction takes the JSON the shipped sh templates print, rendered here from the
// template bodies in those files so it is the real bytes and not a copy, and
// validates it against the host's hook-stdout schema.
//
// Nothing here reaches the network. testdata/hosts holds vendored copies and
// records the provenance of each; tools/host-schemas.buzz is what refreshes them.

const hostSchemaDir = "testdata/hosts"

// hostConfigSchema names the schema every hooks config a host reads is graded against.
// A host absent from this map is a host whose config nothing checks, which is the
// state this file exists to end.
var hostConfigSchema = map[string]string{
	"claude-code": "claude-code/settings.schema.json",
	"codex":       "codex/hooks.schema.json",
	"cursor":      "cursor/hooks.schema.json",
}

// hostOutputSchema names the schema a rendered verdict is graded against. Codex's is
// OpenAI's own generated one; Claude Code's and Cursor's are ours, because neither
// publishes a schema for hook stdout. testdata/hosts/SOURCES.md says which is
// which and refuses to let that distinction blur.
var hostOutputSchema = map[string]string{
	"claude-code": "claude-code/hook-output.schema.json",
	"codex":       "codex/pre-tool-use.command.output.schema.json",
	"cursor":      "cursor/hook-output.schema.json",
}

// cursorReplySchema maps a shell template variable in cursor-hook.sh to the schema for
// the event that reads what it renders.
//
// One entry per host is enough everywhere else. Cursor validates each event's stdout
// with a different function, and its gating events carry no advisory channel, so the
// guard answers two events with two shapes; a single schema for the host would have to
// be the union of both, which validates each reply against fields the event reading it
// ignores.
var cursorReplySchema = map[string]string{
	"gate":   "cursor/hook-output.schema.json",
	"advise": "cursor/post-tool-use.output.schema.json",
}

// hostConfigFile names the config files magus SHIPS for a host, as opposed to the
// blocks its pages embed. Cursor has none: its page tells a reader to write the file.
var hostConfigFile = map[string][]string{
	"claude-code": {".claude/settings.json"},
	"codex":       {filepath.Join(hookTemplateDir, "codex-hooks.json")},
	"cursor":      {".cursor/hooks.json"},
}

// upstreamConfigDir holds configs the HOST's own people wrote, vendored from
// github.com/cursor/plugins (the advisor, ralph-loop and continual-learning plugins).
//
// Every other input to these schemas is something magus produced, and a schema graded
// only against its author's own output cannot fail: wrong in the same direction as the
// thing it grades, it passes forever. These are the independent half. They exercise
// fields magus never writes -- `loop_limit`, including its null form -- and they are the
// only evidence here that the schema matches what Cursor actually loads rather than what
// magus happens to emit.
//
// Refresh them when Cursor's plugin repository moves; a rejection here is a finding about
// OUR schema, never about their config.
const upstreamConfigDir = "testdata/hosts/cursor/upstream-configs"

// hostGuidePage names the page whose embedded JSON configures each host.
var hostGuidePage = map[string]string{
	"claude-code": "claude-code.md",
	"codex":       "codex.md",
	"cursor":      "cursor.md",
}

// jsonCodeBlock matches a fenced json block on a guide page.
var jsonCodeBlock = regexp.MustCompile("(?ms)^```json\r?\n(.*?)^```")

// coverageHost reads the host out of a template's `magus-guard-coverage:` marker. The
// marker already carries the host-parity contract, so deriving from it rather than from
// a second list here means a template that learns a new host demands that host's schema
// on the next run instead of quietly going ungraded.
var coverageHost = regexp.MustCompile(`magus-guard-coverage:.*\bhost=(\S+)`)

// The literal JSON a template prints without asking magus at all, and the shell variables
// cursor-hook.sh keeps its reply templates in.
var (
	literalJSONObject = regexp.MustCompile(`'(\{"[^'\n]*\})'`)
	shellTemplateVar  = regexp.MustCompile(`(?m)^(\w+)_template='(.*)'$`)
)

// permissionRequestEvent is the hookEventName of Codex's approval-request reply, which is
// graded against its own schema rather than PreToolUse's.
const (
	permissionRequestEvent  = `"hookEventName":"PermissionRequest"`
	permissionRequestSchema = "codex/permission-request.command.output.schema.json"
)

// guardVerdicts are the decisions `magus shell` renders, plus one it never does. The values
// are fixed here rather than taken from a run so the rendered bytes belong to the test, and
// the reason carries the characters a naive template would break on. The unknown decision
// stands in for a contract that grew after a copy was installed: it must never render as
// an allow.
var guardVerdicts = []map[string]any{
	{"decision": "deny", "reason": `git stash is denied here: "quoted" & <angled>`},
	{"decision": "advise", "context": "magus workspace: `magus refs <sym>` for code"},
	{"decision": "pass"},
	{"decision": "ask", "reason": `pushing abc1234, which no passing gate covers: "quoted" & <angled>`},
	{"decision": "maybe", "reason": "a decision no template knows"},
}

// loadHostSchema reads a vendored schema and prepares it for validation.
func loadHostSchema(t *testing.T, rel string) *jsonschema.Resolved {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(hostSchemaDir, rel))
	require.NoError(t, err, "read the vendored schema %s", rel)
	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(raw, &schema), "%s must parse as a JSON Schema", rel)
	// nil loader on purpose: a schema that grew a remote $ref would fail here rather
	// than turn every run of this test into an HTTP request.
	resolved, err := schema.Resolve(nil)
	require.NoError(t, err, "%s must resolve with no loader; a remote $ref would make this test fetch", rel)
	return resolved
}

// decodeJSON unmarshals into the shape jsonschema-go validates.
func decodeJSON(t *testing.T, label, body string) any {
	t.Helper()
	var doc any
	require.NoError(t, json.Unmarshal([]byte(body), &doc), "%s must be valid JSON", label)
	return doc
}

// hooksConfigBlocks returns the fenced json blocks on a page that configure hooks. A
// page may carry JSON for something else (an MCP registration, say), so the selector is
// the presence of a top-level "hooks" key rather than the fence.
func hooksConfigBlocks(t *testing.T, page string) []string {
	t.Helper()
	body, err := os.ReadFile(page)
	require.NoError(t, err, "read %s", page)
	var configs []string
	for _, match := range jsonCodeBlock.FindAllStringSubmatch(string(body), -1) {
		doc, ok := decodeJSON(t, page, match[1]).(map[string]any)
		if !ok {
			continue
		}
		if _, isConfig := doc["hooks"]; isConfig {
			configs = append(configs, match[1])
		}
	}
	return configs
}

// TestShippedHookConfigsValidateAgainstTheirHostSchema grades every hooks config magus
// ships or publishes against the schema its host reads.
//
// The page blocks are covered alongside the files because they are what most readers
// install: a reader copies the block, not the repository's own settings.
func TestShippedHookConfigsValidateAgainstTheirHostSchema(t *testing.T) {
	for host, schemaFile := range hostConfigSchema {
		t.Run(host, func(t *testing.T) {
			schema := loadHostSchema(t, schemaFile)

			var graded int
			for _, file := range hostConfigFile[host] {
				body, err := os.ReadFile(file)
				require.NoError(t, err, "read %s", file)
				assert.NoError(t, schema.Validate(decodeJSON(t, file, string(body))),
					"%s is not a config %s would load, per %s", file, host, schemaFile)
				graded++
			}

			page := filepath.Join(hookTemplateDir, hostGuidePage[host])
			for i, block := range hooksConfigBlocks(t, page) {
				assert.NoError(t, schema.Validate(decodeJSON(t, page, block)),
					"%s json block %d is not a config %s would load, per %s", page, i, host, schemaFile)
				graded++
			}

			assert.Positive(t, graded,
				"nothing was graded for %s: either its page stopped embedding a hooks config or the\n"+
					"fence stopped saying json, and either way this gate went quiet rather than red", host)
		})
	}
}

// TestCursorSchemaAcceptsCursorsOwnConfigs grades our Cursor schema against configs
// Cursor's own people wrote, which is the only input here magus did not produce.
//
// The sibling test above proves the schema accepts what magus writes. That is compatible
// with the schema being wrong, because magus writes a narrow subset: a rejection of a real
// config is invisible to it. This is the half that catches a schema too strict to load
// what the host actually loads -- the direction a hand transcription fails in, since a
// reader transcribing a validator records the branches they happened to read.
func TestCursorSchemaAcceptsCursorsOwnConfigs(t *testing.T) {
	schema := loadHostSchema(t, hostConfigSchema["cursor"])

	entries, err := os.ReadDir(upstreamConfigDir)
	require.NoError(t, err, "read %s", upstreamConfigDir)
	require.NotEmpty(t, entries,
		"no upstream configs vendored: this gate went quiet rather than red, which is the\n"+
			"failure it exists to prevent")

	for _, entry := range entries {
		file := filepath.Join(upstreamConfigDir, entry.Name())
		body, err := os.ReadFile(file)
		require.NoError(t, err, "read %s", file)
		assert.NoError(t, schema.Validate(decodeJSON(t, file, string(body))),
			"%s is a config Cursor ships and our schema rejects it, so the schema is wrong", file)
	}
}

// TestClaudeCodeAndCursorSchemasRejectAnUnknownHookEvent proves the config schemas bite.
//
// A gate that only ever sees valid input cannot tell a strict schema from an empty one,
// and an empty one is exactly what a mis-parsed schema degrades into.
func TestClaudeCodeAndCursorSchemasRejectAnUnknownHookEvent(t *testing.T) {
	cases := map[string]string{
		"claude-code": `{"hooks":{"PreToolUseTypo":[{"hooks":[{"type":"command","command":"true"}]}]}}`,
		"cursor":      `{"version":1,"hooks":{"beforeShellExecutionTypo":[{"command":"true"}]}}`,
	}
	for host, body := range cases {
		t.Run(host, func(t *testing.T) {
			schema := loadHostSchema(t, hostConfigSchema[host])
			assert.Error(t, schema.Validate(decodeJSON(t, host, body)),
				"%s's schema accepted an event name that host has never heard of, so it would\n"+
					"accept a typo in a shipped config too", host)
		})
	}
}

// TestCodexHookEventsAreNamedByItsSchema closes the one hole in Codex's published schema.
//
// Its `hooks` object takes additionalProperties, so `PreToolUseTypo` validates and the
// hook simply never fires, which is the silent failure this whole file exists to
// catch. The schema still NAMES every event Codex supports, so the check magus makes is
// every event it ships is one of those.
func TestCodexHookEventsAreNamedByItsSchema(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(hostSchemaDir, hostConfigSchema["codex"]))
	require.NoError(t, err)

	var schema struct {
		Properties struct {
			Hooks struct {
				Properties map[string]any `json:"properties"`
			} `json:"hooks"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema))
	known := schema.Properties.Hooks.Properties
	require.NotEmpty(t, known, "the codex schema must name the events it supports")
	require.NotContains(t, known, "PreToolUseTypo",
		"the event list read out of the schema is the wrong one if a made-up name is in it")

	for _, file := range hostConfigFile["codex"] {
		body, err := os.ReadFile(file)
		require.NoError(t, err, "read %s", file)
		var config struct {
			Hooks map[string]any `json:"hooks"`
		}
		require.NoError(t, json.Unmarshal(body, &config))
		require.NotEmpty(t, config.Hooks, "%s registers no hooks", file)
		for event := range config.Hooks {
			assert.Contains(t, known, event,
				"%s registers %q, which the Codex hooks schema does not name. Codex accepts an\n"+
					"unknown event without complaint and then never fires it, so this is the only\n"+
					"place a rename or a typo can surface.", file, event)
		}
	}
}

// templateArrangement is one way a host reaches a shared sh template: the environment its
// wiring sets, the event it sends, and whether a Codex prompt rule sits in the project.
type templateArrangement struct {
	label string
	// host is the name the wiring passes as --agent-name, the only place a template reads
	// one. Every arrangement names one: a template given none never assembles a reply for
	// magus to render, because magus refuses it (MGS3022), which the transport cases pin.
	host  string
	env   []string
	event string
	rules bool
	// codex marks an arrangement whose PreToolUse reply must never carry permissionDecision
	// "ask": Codex parses it, reports the hook failed, and runs the call anyway.
	codex bool
	// ownResponse is a reader-written HOST_RESPONSE, which cannot claim --renders-ask.
	ownResponse bool
	// wantsAsk marks an arrangement whose ask must render as the host's own prompt.
	wantsAsk bool
}

var templateArrangements = []templateArrangement{
	{label: "claude-code", host: "claude-code", event: `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "claude-code without the advise arm", host: "claude-code", env: []string{"__MAGUS_NO_ADVISE=1"}, event: `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "codex with no prompt rule", host: "codex", codex: true, event: `{"hook_event_name":"PreToolUse","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "codex with its prompt rule", host: "codex", codex: true, rules: true, event: `{"hook_event_name":"PreToolUse","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "codex in a mode that never prompts", host: "codex", codex: true, rules: true, event: `{"hook_event_name":"PreToolUse","session_id":"s","permission_mode":"bypassPermissions","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "codex approval request for a push", host: "codex", codex: true, rules: true, event: `{"hook_event_name":"PermissionRequest","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git push origin HEAD"}}`},
	{label: "codex approval request for anything else", host: "codex", codex: true, rules: true, event: `{"hook_event_name":"PermissionRequest","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"rm -rf build"}}`},
	// The payload never overrides the name: an event carrying Codex's turn_id, under a
	// wiring that names claude-code, gets Claude Code's ask. Checked by wantsAsk below.
	{label: "claude-code named, Codex-shaped event", host: "claude-code", wantsAsk: true, event: `{"hook_event_name":"PreToolUse","session_id":"s","turn_id":"t","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "a reader's own HOST_RESPONSE", host: "claude-code", ownResponse: true, env: []string{`HOST_RESPONSE={{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}}{{end}}`}, event: `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
}

// templateEcho stands in for magus: it prints the template it was handed and nothing else,
// so what a script ASSEMBLES for a host is read from the script running rather than from
// a regex over its source. It records whether the call claimed --renders-ask in the file
// $RENDERS_ASK_RECORD names.
const templateEcho = "#!/bin/sh\nclaim=no\nfor a in \"$@\"; do [ \"$a\" = --renders-ask ] && claim=yes; done\nprintf '%s' \"$claim\" > \"$RENDERS_ASK_RECORD\"\n" +
	"while [ $# -gt 0 ]; do\n  if [ \"$1\" = -o ]; then printf '%s' \"${2#template=}\"; exit 0; fi\n  shift\ndone\nexit 1\n"

// renderedTemplate is one template body a script assembled, the arrangement it came from,
// and whether the call claimed --renders-ask.
type renderedTemplate struct {
	arrangement templateArrangement
	body        string
	rendersAsk  bool
}

// assembledTemplates runs a shipped sh template once per arrangement against templateEcho
// and returns each template body it would hand magus.
func assembledTemplates(t *testing.T, path string) []renderedTemplate {
	t.Helper()
	for _, tool := range []string{"sh", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("the shared templates run under sh and read the event with jq; %s is not installed", tool)
		}
	}
	script, err := filepath.Abs(path)
	require.NoError(t, err)
	bin := filepath.Join(t.TempDir(), "magus")
	require.NoError(t, os.WriteFile(bin, []byte(templateEcho), 0o755))

	var out []renderedTemplate
	for _, a := range templateArrangements {
		dir := t.TempDir()
		if a.rules {
			rules := filepath.Join(dir, ".codex", "rules", "magus.rules")
			require.NoError(t, os.MkdirAll(filepath.Dir(rules), 0o755))
			require.NoError(t, os.WriteFile(rules, []byte("prefix_rule(pattern = [\"git\", \"push\"], decision = \"prompt\")\n"), 0o644))
		}
		record := filepath.Join(dir, "renders-ask")
		cmd := exec.Command("sh", script, "--agent-name", a.host)
		cmd.Dir = dir
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "TMPDIR=" + t.TempDir(), "__MAGUS_BIN=" + bin, "RENDERS_ASK_RECORD=" + record}, a.env...)
		cmd.Stdin = strings.NewReader(a.event)
		body, err := cmd.Output()
		require.NoError(t, err, "%s (%s)", path, a.label)
		require.NotEmpty(t, body, "%s (%s) handed magus no template", path, a.label)
		claim, err := os.ReadFile(record)
		require.NoError(t, err, "%s (%s) never called magus", path, a.label)
		out = append(out, renderedTemplate{arrangement: a, body: string(body), rendersAsk: string(claim) == "yes"})
	}
	return out
}

// guardResponses returns every JSON document a shipped sh template can print: the literal
// ones, and the renderings of the Go template it assembles for each arrangement.
func guardResponses(t *testing.T, path, body string) []string {
	t.Helper()
	name := filepath.Base(path)
	var out []string
	for _, match := range literalJSONObject.FindAllStringSubmatch(body, -1) {
		out = append(out, match[1])
	}
	// Only the shared templates assemble HOST_RESPONSE; cursor-hook.sh keeps its replies in
	// variables TestCursorGuardRepliesValidateAgainstTheEventThatReadsThem grades.
	if !strings.Contains(body, "HOST_RESPONSE") {
		return out
	}
	funcs := sprig.HermeticTxtFuncMap()
	for _, assembled := range assembledTemplates(t, path) {
		a := assembled.arrangement
		// The claim is what lets magus return an ask at all, so it must track exactly the
		// replies that render one: every reply the template assembles, and none a reader wrote.
		assert.Equal(t, !a.ownResponse, assembled.rendersAsk,
			"%s (%s): --renders-ask claimed=%v; a template's own reply must claim it and a reader's must not", name, a.label, assembled.rendersAsk)
		tmpl, err := template.New(name).Funcs(funcs).Parse(assembled.body)
		require.NoError(t, err, "%s (%s) renders its verdict through this template, so it must parse", name, a.label)
		for _, verdict := range guardVerdicts {
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
			if strings.HasPrefix(text, "{") {
				out = append(out, text)
			}
		}
	}
	return out
}

// TestRenderedGuardVerdictsValidateAgainstTheirHostSchema grades the bytes a host
// actually receives.
//
// Rendered from the template bodies in the shipped files rather than from a copy pasted
// here, so an edit to a template is graded on the next run instead of drifting away from
// a fixture nobody updates.
func TestRenderedGuardVerdictsValidateAgainstTheirHostSchema(t *testing.T) {
	templates, err := filepath.Glob(filepath.Join(hookTemplateDir, "*.sh"))
	require.NoError(t, err)
	require.NotEmpty(t, templates, "the shipped templates must be discoverable")

	var graded int
	for _, path := range templates {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		body := string(raw)

		hosts := map[string]bool{}
		for _, match := range coverageHost.FindAllStringSubmatch(body, -1) {
			hosts[match[1]] = true
		}
		// No coverage marker means the template carries no verdict at all. It observes,
		// or checkpoints, or rehydrates, and has no host reply to grade. That absence is
		// deliberate and conventions_test.go already holds it to it.
		if len(hosts) == 0 {
			continue
		}
		graded++

		t.Run(filepath.Base(path), func(t *testing.T) {
			responses := guardResponses(t, path, body)
			require.NotEmpty(t, responses,
				"%s prints no JSON at all, which means the extraction above stopped matching it\n"+
					"rather than that the template stopped answering", path)

			// A reply to Codex's approval request answers a different event, with its own
			// published schema, and no other host is wired to raise it.
			approvals := loadHostSchema(t, permissionRequestSchema)
			for _, response := range responses {
				if !strings.Contains(response, permissionRequestEvent) {
					continue
				}
				require.True(t, hosts["codex"], "%s answers a PermissionRequest but declares no codex coverage", path)
				assert.NoError(t, approvals.Validate(decodeJSON(t, path, response)),
					"%s prints %s, which codex would not accept, per %s", path, response, permissionRequestSchema)
			}
			for host := range hosts {
				schemaFile, ok := hostOutputSchema[host]
				require.True(t, ok,
					"%s answers %s and no schema grades that host's stdout; vendor one under %s\n"+
						"and record it in SOURCES.md", path, host, hostSchemaDir)
				schema := loadHostSchema(t, schemaFile)
				for _, response := range responses {
					if strings.Contains(response, permissionRequestEvent) {
						continue
					}
					assert.NoError(t, schema.Validate(decodeJSON(t, path, response)),
						"%s prints %s, which %s would not accept, per %s", path, response, host, schemaFile)
				}
			}
		})
	}
	assert.Positive(t, graded,
		"no template declared a magus-guard-coverage host, so this gate graded nothing and said so\n"+
			"by passing")
}

// TestHookOutputSchemasRejectAWrongEventName is the negative for the output direction.
//
// hookEventName is the field a host keys its whole reading of the reply on, so a typo
// there costs the verdict silently. Both output schemas pin it to a constant, and this
// is what proves they still do.
func TestHookOutputSchemasRejectAWrongEventName(t *testing.T) {
	const wrong = `{"hookSpecificOutput":{"hookEventName":"PreToolUseTypo",` +
		`"permissionDecision":"deny","permissionDecisionReason":"nope"}}`

	for _, host := range []string{"claude-code", "codex"} {
		t.Run(host, func(t *testing.T) {
			schema := loadHostSchema(t, hostOutputSchema[host])
			assert.Error(t, schema.Validate(decodeJSON(t, host, wrong)),
				"%s's output schema accepted a misspelled hookEventName", host)
		})
	}

	t.Run("cursor", func(t *testing.T) {
		schema := loadHostSchema(t, hostOutputSchema["cursor"])
		assert.Error(t, schema.Validate(decodeJSON(t, "cursor", `{"permission":"denied"}`)),
			"cursor's output schema accepted a permission value Cursor does not define")

		advise := loadHostSchema(t, cursorReplySchema["advise"])
		assert.Error(t, advise.Validate(decodeJSON(t, "cursor", `{"additional_context":{"text":"nope"}}`)),
			"cursor's postToolUse schema accepted an advisory that is not a string, which Cursor drops")
	})
}

// schemaPropertyNames reads the field names a vendored schema declares.
//
// Read out of the raw JSON rather than off the resolved schema because the question is
// what the host NAMES, and jsonschema-go is built to validate rather than to introspect.
func schemaPropertyNames(t *testing.T, rel string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(hostSchemaDir, rel))
	require.NoError(t, err, "read the vendored schema %s", rel)
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema))
	require.NotEmpty(t, schema.Properties, "%s must name the fields it grades", rel)
	return schema.Properties
}

// TestCursorGuardRepliesValidateAgainstTheEventThatReadsThem grades what cursor-hook.sh
// answers, per event.
//
// The generic extraction above cannot reach these. The guard holds its two replies in
// shell variables and splices them in as `template=$gate_template`, so the only Cursor
// bytes that gate ever saw were the two literal allow replies, leaving the deny path
// ungraded, which is the one path a guard exists for.
//
// Both halves of the check matter and only one of them is a schema. Validating catches a
// field whose TYPE moved. The subset assertion catches a field Cursor RENAMED, which
// validating cannot: Cursor's stdout validators read the fields they know and ignore the
// rest, so a reply naming additional_context_v2 is accepted, ignored, and carries no
// advisory at all.
func TestCursorGuardRepliesValidateAgainstTheEventThatReadsThem(t *testing.T) {
	path := filepath.Join(hookTemplateDir, "cursor-hook.sh")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "read %s", path)

	templates := shellTemplateVar.FindAllStringSubmatch(string(raw), -1)
	require.Len(t, templates, len(cursorReplySchema),
		"%s must render one reply template per mapped Cursor event; a template that stopped\n"+
			"being assigned on its own line reads here as one that stopped existing", path)

	funcs := sprig.HermeticTxtFuncMap()
	for _, match := range templates {
		name := match[1]
		t.Run(name, func(t *testing.T) {
			schemaFile, ok := cursorReplySchema[name]
			require.True(t, ok,
				"%s renders %s_template and nothing says which Cursor event reads it", path, name)
			schema := loadHostSchema(t, schemaFile)
			named := schemaPropertyNames(t, schemaFile)

			tmpl, err := template.New(name).Funcs(funcs).Parse(match[2])
			require.NoError(t, err, "%s_template carries the reply, so it must parse", name)
			for _, verdict := range guardVerdicts {
				var out strings.Builder
				require.NoError(t, tmpl.Execute(&out, verdict), "%s_template on a %s", name, verdict["decision"])
				reply, isObject := decodeJSON(t, path, out.String()).(map[string]any)
				require.True(t, isObject, "%s_template must render a JSON object", name)
				if name == "gate" {
					switch verdict["decision"] {
					case "ask":
						assert.Equal(t, "ask", reply["permission"], "an ask is Cursor's own approval prompt")
					case "pass", "advise":
						assert.Equal(t, "allow", reply["permission"])
					default:
						assert.Equal(t, "deny", reply["permission"], "only pass and advise may allow; %s must not", verdict["decision"])
					}
				}
				assert.NoError(t, schema.Validate(reply),
					"%s_template renders %s, which Cursor would not accept, per %s", name, out.String(), schemaFile)
				for field := range reply {
					assert.Contains(t, named, field,
						"%s_template names %q, which %s does not. Cursor ignores a field it has never\n"+
							"heard of rather than rejecting it, so a rename costs the verdict in silence.",
						name, field, schemaFile)
				}
			}
		})
	}
}

// sourcesRow matches one row of the provenance table, which is the only place a
// vendored schema's origin and digest are written down.
var sourcesRow = regexp.MustCompile("(?m)^\\| `([^`]+)` \\| (published|derived-from-binary|derived) \\|.*\\| `([0-9a-f]{64})` \\|")

// TestVendoredHostSchemasMatchTheirRecordedDigest keeps the provenance honest.
//
// The whole value of a vendored schema is that it is the host's bytes and not our
// opinion of them. Nothing else stops a failing gate from being "fixed" by editing the
// schema, which would leave the test green and the claim false.
func TestVendoredHostSchemasMatchTheirRecordedDigest(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(hostSchemaDir, "SOURCES.md"))
	require.NoError(t, err, "read the provenance table")

	recorded := map[string]string{}
	for _, row := range sourcesRow.FindAllStringSubmatch(string(body), -1) {
		recorded[row[1]] = row[3]
	}
	require.NotEmpty(t, recorded, "SOURCES.md must carry a row per vendored schema")

	vendored, err := filepath.Glob(filepath.Join(hostSchemaDir, "*", "*.json"))
	require.NoError(t, err)
	require.NotEmpty(t, vendored)

	for _, path := range vendored {
		rel := filepath.ToSlash(strings.TrimPrefix(path, hostSchemaDir+string(filepath.Separator)))
		want, ok := recorded[rel]
		require.True(t, ok, "%s is vendored but SOURCES.md says nothing about where it came from", rel)
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		sum := sha256.Sum256(raw)
		assert.Equal(t, want, hex.EncodeToString(sum[:]),
			"%s no longer matches the digest SOURCES.md records. If you refreshed it, update the\n"+
				"digest and the read date in the same commit. If you edited it to make a gate pass,\n"+
				"that gate was telling you a shipped file drifted from what its host accepts.", rel)
	}

	for rel := range recorded {
		assert.FileExists(t, filepath.Join(hostSchemaDir, filepath.FromSlash(rel)),
			"SOURCES.md records %s, which is not vendored", rel)
	}
}
