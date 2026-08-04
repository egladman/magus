package magus_test

// Dogfooding checks: assertions that this repository actually uses what it publishes.
//
// This file deliberately has no dogfood.go beside it, and it is the one place in the tree
// where that is the point rather than an oversight. Its subject is not a Go symbol but the
// agreement between three artifacts - the repo's own config, the guide, and the templates a
// reader downloads - so there is nothing for it to pair with, and naming it after any one of
// them (it was hookdocs_test.go) advertised a hookdocs.go that never existed. Anything else
// asserting "we use what we ship" belongs here too.
//
// The guard hook this repository dogfoods, the one its documentation teaches,
// and the one a reader downloads must all be the SAME file. They drifted once
// already: the docs kept advertising `command -v magus || exit 0` after the
// shipped config had moved on, so a reader copying the documented form got a
// guard that fails open in silence - the exact failure the change removed.
//
// The templates under docs/guides/integrations/agents/ are the source of truth. This
// repository's own config invokes them rather than inlining a copy, so
// dogfooding exercises the artifact readers actually get. These tests keep that
// true: a config that stops referencing a template, or a template that stops
// being embedded in the guide, is a test failure rather than something only a
// careful reader would notice.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	json "github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	dogfoodedHookConfig = ".claude/settings.json"
	hookGuideDoc        = "docs/guides/integrations/agents.md"
	hookTemplateDir     = "docs/guides/integrations/agents"
)

// hookTemplates are the artifacts a reader installs. The directory also holds
// the project's own scaffolding (package.json, tsconfig.json, biome.json, the
// lockfile, node_modules) which exists to LINT the templates and is not itself
// something anyone copies into a host - so the list is explicit rather than a
// directory walk that would drag all of it into the guide.
var hookTemplates = []string{
	"magus-guard-command.sh",
	"magus-guard-path.sh",
	"codex-hooks.json",
	"cursor-guard.sh",
	"opencode-plugin.ts",
}

type hookSettings struct {
	Hooks struct {
		PreToolUse []struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"PreToolUse"`
	} `json:"hooks"`
}

// TestDogfoodedHookInvokesTheTemplate keeps this repository honest: its own
// guard must run the same file a reader downloads, not a copy that can drift.
//
// The config it reads is per-developer and deliberately untracked - committing one
// machine's settings.json shipped that machine's guard wiring to every clone - so this
// runs for whoever wired their own hooks and skips in a fresh clone and in CI, where
// there is no dogfooded config to check. The docs-versus-template drift this file exists
// to prevent is still gated unconditionally by TestHookTemplatesAreEmbeddedInTheGuide.
func TestDogfoodedHookInvokesTheTemplate(t *testing.T) {
	raw, err := os.ReadFile(dogfoodedHookConfig)
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("%s is untracked per-developer wiring; no dogfooded config to check", dogfoodedHookConfig)
	}
	require.NoError(t, err, "read %s", dogfoodedHookConfig)

	var cfg hookSettings
	require.NoError(t, json.Unmarshal(raw, &cfg), "parse %s", dogfoodedHookConfig)
	require.NotEmpty(t, cfg.Hooks.PreToolUse, "%s declares no PreToolUse hooks", dogfoodedHookConfig)

	for _, entry := range cfg.Hooks.PreToolUse {
		require.NotEmpty(t, entry.Hooks, "matcher %q has no hooks", entry.Matcher)
		for _, h := range entry.Hooks {
			assert.Contains(t, h.Command, hookTemplateDir,
				"the %q hook must invoke a template under %s rather than inline its own copy, "+
					"so dogfooding exercises the file readers download", entry.Matcher, hookTemplateDir)

			// The referenced file must exist: a hook pointing at a moved or
			// renamed template fails open silently, which is the failure mode
			// this whole arrangement exists to remove.
			for _, field := range strings.Fields(h.Command) {
				if strings.HasPrefix(field, hookTemplateDir) {
					assert.FileExists(t, field, "%q hook references a template that does not exist", entry.Matcher)
				}
			}
		}
	}
}

// TestHookTemplatesUseTheStdinContract pins the invocation shape the binary
// accepts: `magus hook` reads its input from STDIN, takes no positional
// arguments, and is a top-level verb - `magus agent hook` no longer exists.
//
// The OpenCode plugin shipped the dead form once (`["agent", "hook", ..., "--",
// command]`): magus exited non-zero with empty stdout, JSON.parse("") threw,
// and the plugin's fail-open path allowed EVERY call - a guard hole invisible
// unless someone read the console. Text-mirroring alone
// (TestHookTemplatesAreEmbeddedInTheGuide) cannot catch that class: the guide
// faithfully embedded the broken invocation.
func TestHookTemplatesUseTheStdinContract(t *testing.T) {
	for _, name := range hookTemplates {
		body, err := os.ReadFile(filepath.Join(hookTemplateDir, name))
		require.NoError(t, err, "every template in hookTemplates must exist")
		text := string(body)

		assert.NotContains(t, text, "agent hook",
			"%s: `magus agent hook` was replaced by top-level `magus hook`", name)
		assert.NotContains(t, text, `"agent", "hook"`,
			"%s: `magus agent hook` was replaced by top-level `magus hook`", name)
	}

	// The plugin is the one template that spawns magus without a shell, so the
	// stdin wiring is code rather than a pipe: pin that it exists.
	plugin, err := os.ReadFile(filepath.Join(hookTemplateDir, "opencode-plugin.ts"))
	require.NoError(t, err)
	assert.Contains(t, string(plugin), "stdin:",
		"the OpenCode plugin must hand the guard its input on stdin; hookCmd rejects positional arguments")
}

// TestHookTemplatesAreEmbeddedInTheGuide keeps the docs site explorable without
// a transclusion feature: every template is embedded verbatim, and an edit to
// one that is not mirrored into the guide fails here.
func TestHookTemplatesAreEmbeddedInTheGuide(t *testing.T) {
	guide, err := os.ReadFile(hookGuideDoc)
	require.NoError(t, err, "read %s", hookGuideDoc)
	doc := string(guide)

	for _, name := range hookTemplates {
		body, err := os.ReadFile(filepath.Join(hookTemplateDir, name))
		require.NoError(t, err, "every template in hookTemplates must exist")

		// Compare on the executable lines only. Comments carry the reasoning and
		// are worth reading in the file itself; requiring the guide to mirror
		// every one of them would make the page unreadable and the test brittle.
		var missing []string
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
				continue
			}
			if !strings.Contains(doc, line) {
				missing = append(missing, line)
			}
		}
		assert.Empty(t, missing,
			"%s embeds %s incompletely: the lines below are in the template but not in the guide.\n"+
				"Re-copy the template into its code block - a reader exploring the docs site must see\n"+
				"what they would download.", hookGuideDoc, name)
	}
}
