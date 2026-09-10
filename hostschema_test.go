package magus

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
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
// Nothing here reaches the network. testdata/hostschemas holds vendored copies and
// records the provenance of each; tools/host-schemas.buzz is what refreshes them.

const hostSchemaDir = "testdata/hostschemas"

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
// publishes a schema for hook stdout. testdata/hostschemas/SOURCES.md says which is
// which and refuses to let that distinction blur.
var hostOutputSchema = map[string]string{
	"claude-code": "claude-code/hook-output.schema.json",
	"codex":       "codex/pre-tool-use.output.schema.json",
	"cursor":      "cursor/hook-output.schema.json",
}

// hostConfigFile names the config files magus SHIPS for a host, as opposed to the
// blocks its pages embed. Cursor has none: its page tells a reader to write the file.
var hostConfigFile = map[string][]string{
	"claude-code": {".claude/settings.json"},
	"codex":       {filepath.Join(hookTemplateDir, "codex-hooks.json")},
}

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

// The shell assignments that carry a Go template body, and the literal JSON a template
// prints without asking magus at all.
//
// HOST_RESPONSE is matched as two segments because the shell splices
// "$HOST_ADVISE_BRANCH" between them. Composing it here rather than reading one string
// is what lets the test render BOTH arrangements: with the advise arm, which is what
// Claude Code gets, and without it, which is what codex-hooks.json asks for by setting
// GUARD_NO_ADVISE.
var (
	adviseBranchAssign = regexp.MustCompile(`(?m)^\s*\[ -n "\$HOST_ADVISE_BRANCH" \] \|\| HOST_ADVISE_BRANCH='(.*)'$`)
	hostResponseAssign = regexp.MustCompile(`(?m)^\[ -n "\$HOST_RESPONSE" \] \|\| HOST_RESPONSE='(.*)'"\$HOST_ADVISE_BRANCH"'(.*)'$`)
	inlineTemplateArg  = regexp.MustCompile(`-o 'template=(.*)'`)
	literalJSONObject  = regexp.MustCompile(`'(\{"[^'\n]*\})'`)
)

// guardVerdicts are the three decisions `magus session hook` renders. The values are
// fixed here rather than taken from a run so the rendered bytes belong to the test, and
// the reason carries the characters a naive template would break on.
var guardVerdicts = []map[string]any{
	{"decision": "deny", "reason": `git stash is denied here: "quoted" & <angled>`},
	{"decision": "advise", "context": "magus workspace: `magus refs <sym>` for code"},
	{"decision": "pass"},
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

// guardResponses returns every JSON document a shipped sh template can print: the
// literal ones, and the renderings of the Go templates it hands magus.
func guardResponses(t *testing.T, name, body string) []string {
	t.Helper()

	var bodies []string
	if segments := hostResponseAssign.FindStringSubmatch(body); segments != nil {
		advise := adviseBranchAssign.FindStringSubmatch(body)
		require.NotNil(t, advise,
			"%s splices $HOST_ADVISE_BRANCH into HOST_RESPONSE but never assigns it a default", name)
		// Both arrangements, because both ship: the advise arm is what Claude Code
		// renders, and GUARD_NO_ADVISE empties it for Codex.
		bodies = append(bodies, segments[1]+advise[1]+segments[2], segments[1]+segments[2])
	}
	for _, match := range inlineTemplateArg.FindAllStringSubmatch(body, -1) {
		bodies = append(bodies, match[1])
	}

	funcs := template.FuncMap(sprig.HermeticTxtFuncMap())
	var out []string
	for _, match := range literalJSONObject.FindAllStringSubmatch(body, -1) {
		out = append(out, match[1])
	}
	for _, arrangement := range bodies {
		tmpl, err := template.New(name).Funcs(funcs).Parse(arrangement)
		require.NoError(t, err, "%s renders its verdict through this template, so it must parse", name)
		for _, verdict := range guardVerdicts {
			var rendered strings.Builder
			require.NoError(t, tmpl.Execute(&rendered, verdict), "%s on a %s", name, verdict["decision"])
			// A pass renders nothing, and one surface answers in prose rather than JSON
			// because Cursor's post-write event has no verdict channel to answer on.
			if text := rendered.String(); strings.HasPrefix(text, "{") {
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
			responses := guardResponses(t, filepath.Base(path), body)
			require.NotEmpty(t, responses,
				"%s prints no JSON at all, which means the extraction above stopped matching it\n"+
					"rather than that the template stopped answering", path)

			for host := range hosts {
				schemaFile, ok := hostOutputSchema[host]
				require.True(t, ok,
					"%s answers %s and no schema grades that host's stdout; vendor one under %s\n"+
						"and record it in SOURCES.md", path, host, hostSchemaDir)
				schema := loadHostSchema(t, schemaFile)
				for _, response := range responses {
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
	})
}

// sourcesRow matches one row of the provenance table, which is the only place a
// vendored schema's origin and digest are written down.
var sourcesRow = regexp.MustCompile("(?m)^\\| `([^`]+)` \\| (published|derived) \\|.*\\| `([0-9a-f]{64})` \\|")

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
