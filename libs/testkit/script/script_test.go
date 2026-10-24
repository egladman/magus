package script

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain keeps the variables the helper-process tests instruct their helpers with.
func TestMain(m *testing.M) { testkit.Main(m, "TESTSCRIPT_HELPER_*") }

// fakeClaude records its arguments and prints a canned result; a prompt holding "fail"
// gets an error result and exit status 1, as a failed claude run does.
const fakeClaude = `#!/bin/sh
printf '%s\n' "$@" > claude.args
case "$2" in
*fail*)
	echo '{"type":"result","is_error":true,"result":"it broke","num_turns":1,"total_cost_usd":0.5,"usage":{"input_tokens":1,"output_tokens":2,"cache_read_input_tokens":3,"cache_creation_input_tokens":4}}'
	exit 1
	;;
esac
echo '{"type":"result","is_error":false,"result":"done","num_turns":3,"total_cost_usd":0.25,"usage":{"input_tokens":10,"output_tokens":20,"cache_read_input_tokens":30,"cache_creation_input_tokens":40,"service_tier":"standard"}}'
`

// scripts writes each body as <name>.txtar into a fresh directory and returns it.
func scripts(t *testing.T, bodies map[string]string) string {
	t.Helper()
	w := testkit.NewWorkspace(t)
	for name, body := range bodies {
		w.Write(name+".txtar", body)
	}
	return w.Root()
}

// onPath sets PATH to the given executables and the system directories alone, so a
// real claude or magus can never run in place of a fake.
func onPath(t *testing.T, tools map[string]string) {
	t.Helper()
	w := testkit.NewWorkspace(t)
	for name, body := range tools {
		w.WriteExecutable(name, body)
	}
	t.Setenv("PATH", strings.Join([]string{w.Root(), "/usr/bin", "/bin"}, string(os.PathListSeparator)))
}

func readRecords(t *testing.T, path string) []Record {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var rows []Record
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		var r Record
		require.NoError(t, json.Unmarshal([]byte(line), &r), line)
		rows = append(rows, r)
	}
	slices.SortFunc(rows, func(a, b Record) int { return strings.Compare(a.Task, b.Task) })
	return rows
}

func TestRunIsolatesEachScript(t *testing.T) {
	t.Setenv("TESTSCRIPT_LEAK", "leaked")
	Run(t, scripts(t, map[string]string{"isolated": `
exec sh -c 'echo "$HOME|$MAGUS_BROKER|$TESTSCRIPT_LEAK"'
stdout '^/.+/home\|off\|$'
! stdout ^${WORK@R}
exec ls -A $HOME
! stdout .
`}))
}

func TestWithSetupRunsAfterIsolationInOrder(t *testing.T) {
	Run(t, scripts(t, map[string]string{"ordered": `
exec sh -c 'echo "$SETUP"'
stdout '^first:home,second$'
`}),
		WithSetup(func(e *Env) error {
			e.Setenv("SETUP", "first:"+filepath.Base(e.Getenv("HOME")))
			return nil
		}),
		WithSetup(func(e *Env) error {
			e.Setenv("SETUP", e.Getenv("SETUP")+",second")
			return nil
		}))
}

func TestWithCommandsAddsACommand(t *testing.T) {
	Run(t, scripts(t, map[string]string{"cmd": "hello world\nstdout '^hello world$'\n"}),
		WithCommands(map[string]Command{
			"hello": func(s *State, _ bool, args []string) {
				_, err := s.Stdout().Write([]byte(fmt.Sprintf("hello %s\n", strings.Join(args, " "))))
				s.Check(err)
			},
		}))
}

func TestRecorderRecordsAPassingScript(t *testing.T) {
	onPath(t, map[string]string{"claude": fakeClaude})
	results := filepath.Join(t.TempDir(), "results.jsonl")
	dir := scripts(t, map[string]string{"pass": `
agent prompt.txt --model haiku
cmp claude.args want.args
stdout '"result":"done"'
agent prompt.txt
agent-pass

-- prompt.txt --
do the task
-- want.args --
-p
do the task

--output-format
json
--strict-mcp-config
--setting-sources
project
--model
haiku
`})
	t.Run("scripts", func(t *testing.T) {
		Run(t, dir, WithAgent(Recorder{Variant: "short", Repetition: 2, File: results}))
	})
	assert.Equal(t, []Record{{
		Variant: "short", Task: "pass", Repetition: 2, Pass: true,
		InputTokens: 20, OutputTokens: 40, CacheReadInputTokens: 60, CacheCreationInputTokens: 80,
		CostUSD: 0.5, Turns: 6,
	}}, readRecords(t, results))
}

// TestRecorderRecordsAFailingScript runs its failing scripts in a helper process, which
// re-runs this binary, so their failure is the helper's and not this test's.
func TestRecorderRecordsAFailingScript(t *testing.T) {
	if results := os.Getenv("TESTSCRIPT_HELPER_RESULTS"); results != "" {
		Run(t, os.Getenv("TESTSCRIPT_HELPER_DIR"), WithAgent(Recorder{Variant: "full", Repetition: 1, File: results}))
		return
	}
	onPath(t, map[string]string{"claude": fakeClaude})
	results := filepath.Join(t.TempDir(), "results.jsonl")
	dir := scripts(t, map[string]string{
		"checkfails":  "agent prompt.txt\nexists answer.txt\nagent-pass\n-- prompt.txt --\ndo the task\n",
		"claudefails": "agent prompt.txt\nagent-pass\n-- prompt.txt --\nfail the task\n",
		"neverran":    "exists answer.txt\nagent prompt.txt\nagent-pass\n-- prompt.txt --\ndo the task\n",
	})
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecorderRecordsAFailingScript$")
	cmd.Env = append(os.Environ(), "TESTSCRIPT_HELPER_RESULTS="+results, "TESTSCRIPT_HELPER_DIR="+dir)
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "%s", out)
	assert.Contains(t, string(out), "agent: exit status 1")
	assert.Equal(t, []Record{
		{
			Variant: "full", Task: "checkfails", Repetition: 1,
			InputTokens: 10, OutputTokens: 20, CacheReadInputTokens: 30, CacheCreationInputTokens: 40,
			CostUSD: 0.25, Turns: 3,
		},
		{
			Variant: "full", Task: "claudefails", Repetition: 1,
			InputTokens: 1, OutputTokens: 2, CacheReadInputTokens: 3, CacheCreationInputTokens: 4,
			CostUSD: 0.5, Turns: 1,
		},
		{Variant: "full", Task: "neverran", Repetition: 1},
	}, readRecords(t, results))
}

func TestSkillsCopiesADirectory(t *testing.T) {
	skills := testkit.NewWorkspace(t)
	skills.Write("magus-run/SKILL.md", "name: magus-run\n")
	Run(t, scripts(t, map[string]string{"copied": `
cmp .claude/skills/magus-run/SKILL.md want.md

-- want.md --
name: magus-run
`}), WithSetup(Skills(skills.Root())))
}

func TestSkillsRunsMagusAgentInstallInWork(t *testing.T) {
	onPath(t, map[string]string{"magus": "#!/bin/sh\nprintf '%s\\n' \"$@\" > magus.args\n"})
	Run(t, scripts(t, map[string]string{"installed": `
cmp magus.args want.args

-- want.args --
agent
install
--skill-form
short
.claude/skills
`}), WithSetup(Skills("short")))
}

func TestSkillsCopiesAnExplicitlyRelativeDirectory(t *testing.T) {
	cwd := testkit.NewWorkspace(t)
	cwd.Write("skills/magus-run/SKILL.md", "name: magus-run\n")
	t.Chdir(cwd.Root())
	Run(t, scripts(t, map[string]string{"copied": `
cmp .claude/skills/magus-run/SKILL.md want.md

-- want.md --
name: magus-run
`}), WithSetup(Skills("./skills")))
}

// TestSkillsTreatsABareWordAsASkillForm keeps a directory beside the test, named like
// a skill form, from being copied in its place.
func TestSkillsTreatsABareWordAsASkillForm(t *testing.T) {
	cwd := testkit.NewWorkspace(t)
	cwd.Write("short/magus-run/SKILL.md", "name: magus-run\n")
	t.Chdir(cwd.Root())
	onPath(t, map[string]string{"magus": "#!/bin/sh\nprintf '%s\\n' \"$@\" > magus.args\n"})
	Run(t, scripts(t, map[string]string{"installed": `
exists magus.args
! exists .claude/skills/magus-run
`}), WithSetup(Skills("short")))
}

func TestRecorderRunsCLAUDE_CODE_EXECPATH(t *testing.T) {
	onPath(t, nil)
	cli := testkit.NewWorkspace(t)
	cli.WriteExecutable("claude-desktop", fakeClaude)
	results := filepath.Join(t.TempDir(), "results.jsonl")
	dir := scripts(t, map[string]string{"execpath": "agent prompt.txt\nagent-pass\n-- prompt.txt --\ndo the task\n"})
	t.Run("scripts", func(t *testing.T) {
		Run(t, dir,
			WithSetup(func(e *Env) error {
				e.Setenv("CLAUDE_CODE_EXECPATH", filepath.Join(cli.Root(), "claude-desktop"))
				return nil
			}),
			WithAgent(Recorder{Variant: "short", Repetition: 1, File: results}))
	})
	assert.Equal(t, []Record{{
		Variant: "short", Task: "execpath", Repetition: 1, Pass: true,
		InputTokens: 10, OutputTokens: 20, CacheReadInputTokens: 30, CacheCreationInputTokens: 40,
		CostUSD: 0.25, Turns: 3,
	}}, readRecords(t, results))
}

// TestWithAgentRefusesACommandNamedAgent runs Run in a helper process, which re-runs
// this binary, so the refusal fails the helper and not this test.
func TestWithAgentRefusesACommandNamedAgent(t *testing.T) {
	if os.Getenv("TESTSCRIPT_HELPER_COLLIDE") != "" {
		Run(t, t.TempDir(),
			WithCommands(map[string]Command{"agent": func(*State, bool, []string) {}}),
			WithAgent(Recorder{File: filepath.Join(t.TempDir(), "results.jsonl")}))
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestWithAgentRefusesACommandNamedAgent$")
	cmd.Env = append(os.Environ(), "TESTSCRIPT_HELPER_COLLIDE=1")
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "%s", out)
	assert.Contains(t, string(out), `script: WithAgent and WithCommands both define the "agent" command`)
}
