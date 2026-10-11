package main

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/rogpeppe/go-internal/testscript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHarnessInstallReportsWhatItWroteAndPruned pins the report. This install
// force-writes and prunes without a flag, and it did both with no output at all:
// 30 directories rewritten and 11 removed, and nothing said so. A delete a person
// cannot see is how they lose a skill they thought they had.
func TestHarnessInstallReportsWhatItWroteAndPruned(t *testing.T) {
	root := t.TempDir()
	const dest = ".agents/skills"

	var log bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&log, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	require.NoError(t, installHarnessSkillPath(context.Background(), root, dest, agent.FormFull, false))
	assert.Contains(t, log.String(), `msg=wrote component="agent harness install"`)

	// An orphan from an earlier release: magus stamped it, so this install prunes it.
	orphan := filepath.Join(root, dest, "magus-retired")
	require.NoError(t, os.MkdirAll(orphan, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(orphan, "SKILL.md"),
		agentSkills.StampSkill("magus-retired", []byte("---\nname: magus-retired\n---\n\n# gone\n"), agent.VariantShort), 0o644))

	log.Reset()
	out := captureStdout(t, func() {
		require.NoError(t, installHarnessSkillPath(context.Background(), root, dest, agent.FormFull, false))
	})
	assert.NoDirExists(t, orphan)
	assert.Equal(t, "removed skill this binary no longer ships: "+filepath.Join(dest, "magus-retired")+"\n", out)

	// A dry run names the same deletion and performs none of it.
	require.NoError(t, os.MkdirAll(orphan, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(orphan, "SKILL.md"),
		agentSkills.StampSkill("magus-retired", []byte("---\nname: magus-retired\n---\n\n# gone\n"), agent.VariantShort), 0o644))
	out = captureStdout(t, func() {
		require.NoError(t, installHarnessSkillPath(context.Background(), root, dest, agent.FormFull, true))
	})
	assert.DirExists(t, orphan)
	assert.Contains(t, out, "would remove skill this binary no longer ships: "+filepath.Join(dest, "magus-retired")+"\n")
}

// TestHarnessInstallRefusesABoundJobFromEitherSource pins that a harness skill install is
// refused under the checkout's binding as well as under the claim the process was launched
// with. Only the claim used to count, so a worker `magus job exec` bound, with no BAGGAGE,
// could rewrite the skills that steer it.
func TestHarnessInstallRefusesABoundJobFromEitherSource(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(trail.EnvBaggage, "")
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })
	globalCfg = config.Config{}
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("version: 1\n"), 0o644))
	cacheDir, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
	require.NoError(t, err)

	acting := func(ctx context.Context) string {
		lease, err := harnessActingLease(ctx, root)
		require.NoError(t, err)
		return lease
	}
	assert.Empty(t, acting(context.Background()), "an unbound caller installs its own skills")

	claimed := proc.WithLease(context.Background(), "fleet/claimed")
	assert.Equal(t, "fleet/claimed", acting(claimed), "the claim")

	bindCheckout(t, cacheDir, "fleet/bound")
	assert.Equal(t, "fleet/bound", acting(context.Background()), "the binding, with no claim")
	assert.Equal(t, "fleet/bound", acting(claimed), "the binding over a different claim")

	err = agentHarnessInstallCmd(context.Background(), root, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `bound job "fleet/bound"`)
}

// TestDescribeHarnessPrintsEachChangeAndTheMergeCommand pins the text a person reads before
// merging: every file, every entry it gains or loses, and the command, which magus prints
// and never runs.
func TestDescribeHarnessPrintsEachChangeAndTheMergeCommand(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, writeHarnessPlan(&out, types.HarnessPlan{
		ID: "claude-code",
		Files: map[string]types.HarnessFile{
			".claude/settings.json": {
				Exists:   true,
				Fragment: map[string]any{"hooks": map[string]any{"Stop": []any{}}},
				Changes: []types.HarnessChange{
					{Op: types.HarnessAdd, Key: "hooks.Stop", Value: map[string]any{"command": "magus buzz -s magus-checkpoint.buzz"}},
					{Op: types.HarnessRetire, Key: "hooks.Stop", Value: map[string]any{"command": "magus buzz -s old/magus-checkpoint.buzz"}},
				},
			},
			".codex/rules/magus.rules": {Content: "rule\n", Changes: []types.HarnessChange{{Op: types.HarnessWrite}}},
		},
		Merge:   "merge-command",
		MCPHint: "register it",
	}))
	assert.Equal(t, `claude-code harness: 2 file(s) to merge
  .claude/settings.json (exists)
    add hooks.Stop: {"command":"magus buzz -s magus-checkpoint.buzz"}
    retire hooks.Stop: {"command":"magus buzz -s old/magus-checkpoint.buzz"}
  .codex/rules/magus.rules (missing)
    write the whole file
merge it yourself (magus never writes host config):
  merge-command
mcp claude-code (user-owned; Magus does not write host MCP config):
register it
`, out.String())

	out.Reset()
	require.NoError(t, writeHarnessPlan(&out, types.HarnessPlan{ID: "cursor"}))
	assert.Equal(t, "cursor harness: current\n", out.String())

	out.Reset()
	require.NoError(t, writeHarnessPlan(&out, types.HarnessPlan{
		ID: "claude-code",
		Wired: []types.HarnessWired{{File: ".claude/settings.json", Key: "hooks.PreToolUse", Entries: []map[string]any{
			{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": "./magus buzz -s magus-command.buzz"}}},
			{"hooks": []any{map[string]any{"type": "command", "command": "./magus buzz -s magus-observe.buzz"}}},
		}}},
	}))
	assert.Equal(t, `claude-code harness: current
wired in .claude/settings.json hooks.PreToolUse:
  Bash: ./magus buzz -s magus-command.buzz
  any: ./magus buzz -s magus-observe.buzz
`, out.String(), "a current harness still prints the commands it wires")
}

// TestDescribeHarnessMergeCommandLeavesTheFileCurrent runs the printed merge command with
// this test binary as `magus`, so the `magus buzz` it pipes into is built from this source
// and never a stale binary found on disk. Afterwards nothing is left to merge, the person's
// own entries and an integer past 2^53 survive, and a retired entry is gone.
func TestDescribeHarnessMergeCommandLeavesTheFileCurrent(t *testing.T) {
	script := filepath.Join(t.TempDir(), "describe_harness_merge.txtar")
	require.NoError(t, os.WriteFile(script, []byte(describeHarnessMergeTxtar), 0o644))
	testscript.Run(t, testscript.Params{
		Files: []string{script},
		Setup: func(e *testscript.Env) error {
			e.Setenv("MAGUS_SERVER_ENABLED", "false")
			e.Setenv("MAGUS_BROKER", "off")
			e.Setenv("MAGUS_HINTS_ENABLED", "false")
			return nil
		},
	})
}

const describeHarnessMergeTxtar = `exec magus describe harness script-host -o 'template={{.merge}}'
cp stdout merge.sh
exec sh merge.sh
cmp hooks.json want.json
exec magus describe harness script-host
stdout '^script-host harness: current$'
stdout '^wired in hooks.json hooks.before:$'
stdout '^  run: magus buzz -s magus-command.buzz$'
exec magus describe harness script-host -o 'template={{range .wired}}{{.file}} {{.key}}{{range .entries}} {{.match}}{{end}}{{end}}'
stdout '^hooks.json hooks.before run$'

-- magusfile.buzz --
import "magus";
import "spells/script-host" as scripthost;
magus\harness.provider(scripthost);
-- spells/script-host/spell.buzz --
import "magus/spell";

export fun mgs_getName() > str { return "script-host"; }

export fun harness_config(target: Target, cb: fun(any)) > {str: any} {
    return {"path": "hooks.json"};
}

export fun harness_skills(target: Target, cb: fun(any)) > {str: any} {
    return {"paths": [], "form": "short"};
}

export fun harness_entries(target: Target, cb: fun(any)) > [any] {
    return [{"path": ["hooks", "before"], "entries": [{"match": "run", "commands": [{"type": "command", "command": "magus buzz -s magus-command.buzz"}]}]}];
}
-- hooks.json --
{
  "large": 9007199254740993,
  "hooks": {
    "before": [
      {"match": "run", "commands": [{"type": "command", "command": "magus buzz -s old/magus-command.buzz"}]},
      {"match": "run", "commands": [{"type": "command", "command": "my-own-hook"}]}
    ],
    "after": [{"command": "theirs"}]
  }
}
-- want.json --
{
  "hooks": {
    "after": [
      {
        "command": "theirs"
      }
    ],
    "before": [
      {
        "commands": [
          {
            "command": "my-own-hook",
            "type": "command"
          }
        ],
        "match": "run"
      },
      {
        "commands": [
          {
            "command": "magus buzz -s magus-command.buzz",
            "type": "command"
          }
        ],
        "match": "run"
      }
    ]
  },
  "large": 9007199254740993
}
`

// TestShippedHarnessEntriesRetireWhenRewritten holds every entry a shipped harness spell
// renders to being one a later merge recognizes as magus's. It merges each host's config
// from nothing, rewrites every command the way a new spell version would, and merges again:
// an entry the planner did not recognize stays beside its replacement, which is how Claude
// Code's session PATH entry came to be wired twice while describe called the file current.
func TestShippedHarnessEntriesRetireWhenRewritten(t *testing.T) {
	script := filepath.Join(t.TempDir(), "shipped_harness_retire.txtar")
	require.NoError(t, os.WriteFile(script, []byte(shippedHarnessRetireTxtar), 0o644))
	testscript.Run(t, testscript.Params{
		Files: []string{script},
		Setup: func(e *testscript.Env) error {
			e.Setenv("MAGUS_SERVER_ENABLED", "false")
			e.Setenv("MAGUS_BROKER", "off")
			e.Setenv("MAGUS_HINTS_ENABLED", "false")
			for _, id := range []string{"claude-code", "codex", "cursor"} {
				spell := filepath.Join("spells", "harness", id, "spell.buzz")
				body, err := os.ReadFile(filepath.Join("..", "..", spell))
				if err != nil {
					return err
				}
				if err := os.MkdirAll(filepath.Join(e.WorkDir, filepath.Dir(spell)), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(e.WorkDir, spell), body, 0o644); err != nil {
					return err
				}
			}
			return nil
		},
	})
}

const shippedHarnessRetireTxtar = `exec sh merge.sh
grep 'magus-command.buzz' .claude/settings.json
grep 'CLAUDE_ENV_FILE' .claude/settings.json
grep 'magus-command.buzz' .codex/hooks.json
grep 'cursor-hook.buzz' .cursor/hooks.json
exec sh rewrite.sh
grep 'stale-rendering' .claude/settings.json
exec sh merge.sh
! grep 'stale-rendering' .claude/settings.json
! grep 'stale-rendering' .codex/hooks.json
! grep 'stale-rendering' .cursor/hooks.json
exec magus describe harness claude-code
stdout '^claude-code harness: current$'
exec magus describe harness codex
stdout '^codex harness: current$'
exec magus describe harness cursor
stdout '^cursor harness: current$'

-- magusfile.buzz --
import "magus";
import "spells/harness/claude-code" as claude;
import "spells/harness/codex";
import "spells/harness/cursor";
magus\harness.provider(claude);
magus\harness.provider(codex);
magus\harness.provider(cursor);
-- merge.sh --
set -e
for id in claude-code codex cursor; do
  magus describe harness "$id" -o 'template={{.merge}}' > "merge-$id.sh"
  sh "merge-$id.sh"
done
-- rewrite.sh --
set -e
for f in .claude/settings.json .codex/hooks.json .cursor/hooks.json; do
  sed 's/"command": "/"command": ": stale-rendering; /g' "$f" > rewritten
  mv rewritten "$f"
done
`

// TestAgentHarnessHasNoWritingVerb pins that no verb writes host config: magus prints it
// through `describe harness` and the person merges it.
func TestAgentHarnessHasNoWritingVerb(t *testing.T) {
	for _, verb := range []string{"apply", "remove"} {
		err := agentHarnessCmd(context.Background(), t.TempDir(), []string{verb, "--id", "x"})
		require.Error(t, err, verb)
		assert.Contains(t, err.Error(), "unknown subcommand", verb)
		assert.Contains(t, err.Error(), "magus describe harness", verb)
	}
}
