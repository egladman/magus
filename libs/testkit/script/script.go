package script

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/testkit"
	"github.com/rogpeppe/go-internal/testscript"
)

// Env and State are go-internal's testscript.Env and testscript.TestScript, named here
// so a file that writes setup hooks and commands imports this package alone.
type (
	Env   = testscript.Env
	State = testscript.TestScript
)

// Command is a script command, as testscript.Params.Cmds holds: neg is true when the
// script negates it with !.
type Command func(s *State, neg bool, args []string)

// Option configures Run.
type Option func(*options)

type options struct {
	setups   []func(*Env) error
	cmds     map[string]Command
	recorder *Recorder
}

// WithSetup adds a hook that runs before each script, after Run has isolated the
// script's environment, the way testify's SetupTest runs before each test. Hooks run in
// the order given, and an error fails the script.
func WithSetup(fn func(*Env) error) Option {
	return func(o *options) { o.setups = append(o.setups, fn) }
}

// WithCommands adds script commands. A later WithCommands replaces an earlier one's
// command of the same name.
func WithCommands(cmds map[string]Command) Option {
	return func(o *options) {
		for name, cmd := range cmds {
			o.cmds[name] = cmd
		}
	}
}

// Run runs every dir/*.txtar as a parallel subtest of t, as testscript.Run does: the
// scripts run after the calling test function returns, so a caller that reads what they
// wrote wraps Run in t.Run.
//
// Each script starts in testkit.Environ over a fresh root: HOME and the XDG directories
// are empty, so no user configuration (an agent's user instructions, skills or hooks, a
// magus config or token) reaches a command, and PATH is the test process's, so a command
// TestMain registered with testscript.Main, such as cmd/magus's in-tree magus, comes
// first. The root is a short temporary directory beside $WORK, not under it: a socket
// under XDG_RUNTIME_DIR has to fit macOS's path cap, and what a command writes under
// HOME must never show up as a change in the workspace a script checks.
//
// Run fails t when WithAgent and WithCommands both define a command. It takes a
// *testing.T, not the testing.TB the rest of testkit takes, because go-internal's
// testscript.Run does.
func Run(t *testing.T, dir string, opts ...Option) {
	t.Helper()
	o := options{cmds: map[string]Command{}}
	for _, opt := range opts {
		opt(&o)
	}
	cmds := make(map[string]func(*State, bool, []string), len(o.cmds)+2)
	for name, cmd := range o.cmds {
		cmds[name] = cmd
	}
	if o.recorder != nil {
		for name, cmd := range map[string]Command{"agent": runAgent, "agent-pass": agentPass} {
			if _, ok := cmds[name]; ok {
				t.Fatalf("script: WithAgent and WithCommands both define the %q command", name)
			}
			cmds[name] = cmd
		}
	}
	testscript.Run(t, testscript.Params{
		Dir: dir,
		Setup: func(e *Env) error {
			if err := isolate(e); err != nil {
				return err
			}
			// Before the hooks, so a script whose setup fails still records its row.
			if o.recorder != nil {
				o.recorder.record(e)
			}
			for _, fn := range o.setups {
				if err := fn(e); err != nil {
					return err
				}
			}
			return nil
		},
		Cmds: cmds,
	})
}

func isolate(e *Env) error {
	root, err := os.MkdirTemp("", "tk")
	if err != nil {
		return fmt.Errorf("script: %w", err)
	}
	e.Defer(func() { _ = os.RemoveAll(root) })
	environ, err := testkit.Environ(root)
	if err != nil {
		return err
	}
	// Later entries win, so these replace testscript's HOME=/no-home and TMPDIR.
	e.Vars = append(e.Vars, environ...)
	return nil
}

// Skills returns a setup hook that installs one variant's skills into the script's
// .claude/skills. A variant that is an absolute path or starts with ./ or ../ names a
// skills directory, copied there; a relative one resolves against the test's working
// directory. Any other variant is a skill form (full, short, both) for `magus agent
// install --skill-form`, run with the magus on the script's PATH and stopped when the
// script's test ends.
func Skills(variant string) func(*Env) error {
	return func(e *Env) error {
		dest := filepath.Join(".claude", "skills")
		if isPath(variant) {
			if err := os.CopyFS(filepath.Join(e.WorkDir, dest), os.DirFS(variant)); err != nil {
				return fmt.Errorf("script: copy skills: %w", err)
			}
			return nil
		}
		cmd := exec.CommandContext(scriptContext(e), "magus", "agent", "install", "--skill-form", variant, dest)
		cmd.Dir = e.WorkDir
		cmd.Env = e.Vars
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("script: magus agent install: %s: %w", bytes.TrimSpace(out), err)
		}
		return nil
	}
}

func isPath(variant string) bool {
	if filepath.IsAbs(variant) {
		return true
	}
	for _, prefix := range []string{"./", "../", "." + string(filepath.Separator), ".." + string(filepath.Separator)} {
		if strings.HasPrefix(variant, prefix) {
			return true
		}
	}
	return false
}

// scriptContext is canceled when the script's test ends: Run's T embeds the script's
// *testing.T.
func scriptContext(e *Env) context.Context {
	if t, ok := e.T().(interface{ Context() context.Context }); ok {
		return t.Context()
	}
	return context.Background()
}

// Recorder turns on the agent commands; see WithAgent.
type Recorder struct {
	Variant    string // recorded on every row: the skill form or skills directory under test
	Repetition int    // recorded on every row
	File       string // the JSONL file every script appends its row to
}

// Record is one script's row in Recorder.File. The usage, cost and turns sum every
// agent command the script ran.
type Record struct {
	Variant                  string  `json:"variant"`
	Task                     string  `json:"task"`
	Repetition               int     `json:"rep"`
	Pass                     bool    `json:"pass"`
	InputTokens              int     `json:"input_tokens"`
	OutputTokens             int     `json:"output_tokens"`
	CacheReadInputTokens     int     `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int     `json:"cache_creation_input_tokens"`
	CostUSD                  float64 `json:"cost_usd"`
	Turns                    int     `json:"turns"`
}

// WithAgent adds two script commands and appends one Record per script to r.File when
// the script ends, whether it passed or failed.
//
//	agent <prompt-file> [arg...]
//
// runs the agent CLI in print mode, `-p <prompt> --output-format json
// --strict-mcp-config --setting-sources project [arg...]`, in the script's current
// directory with the script's environment, and adds the run's usage to the row. The CLI
// is the executable the script's environment names in execPathEnv when a setup hook
// sets it, else agentCLI on the script's PATH. Its stdout is the agent's JSON result.
// It fails the script when the agent exits non-zero or reports an error, after
// counting what the run spent.
//
//	agent-pass
//
// marks the row passed. A script ends with its check and then agent-pass, so one that
// fails before it records pass=false.
//
// Run refuses a WithCommands that defines agent or agent-pass alongside WithAgent.
func WithAgent(r Recorder) Option {
	return func(o *options) { o.recorder = &r }
}

type recordKey struct{}

func (r *Recorder) record(e *Env) {
	rec := &Record{
		Variant: r.Variant,
		// testscript names a script's work directory "script-" plus the script's name.
		Task:       strings.TrimPrefix(filepath.Base(e.WorkDir), "script-"),
		Repetition: r.Repetition,
	}
	e.Values[recordKey{}] = rec
	e.Defer(func() {
		err := appendRecord(r.File, rec)
		if err == nil {
			return
		}
		// Errorf rather than Fatal: this runs as the script unwinds, perhaps from a
		// failure already, and a second Goexit there would hide the first.
		if t, ok := e.T().(interface{ Errorf(string, ...any) }); ok {
			t.Errorf("script: %v", err)
			return
		}
		e.T().Fatal("script: ", err)
	})
}

// recordsMu keeps two parallel scripts' rows from interleaving in one file.
var recordsMu sync.Mutex

func appendRecord(path string, rec *Record) (err error) {
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	recordsMu.Lock()
	defer recordsMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	_, err = f.Write(append(line, '\n'))
	return err
}

func scriptRecord(s *State) *Record {
	rec, ok := s.Value(recordKey{}).(*Record)
	if !ok {
		s.Fatalf("agent: the script has no row; Run with WithAgent sets one up")
	}
	return rec
}

// agentResult is the part of the agent's JSON result a row records. Its fields, from
// usage to num_turns, are the agent CLI's.
type agentResult struct {
	IsError      bool    `json:"is_error"`
	Result       string  `json:"result"`
	NumTurns     int     `json:"num_turns"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	Usage        struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

const (
	agentCLI    = "claude"
	execPathEnv = "CLAUDE_CODE_EXECPATH"
)

func runAgent(s *State, neg bool, args []string) {
	if neg || len(args) == 0 {
		s.Fatalf("usage: agent <prompt-file> [arg...]")
	}
	rec := scriptRecord(s)
	agentArgs := append([]string{
		// The prompt directly after -p: a variadic flag such as --allowedTools would
		// swallow a positional that followed it.
		"-p", s.ReadFile(args[0]),
		"--output-format", "json",
		"--strict-mcp-config",
		"--setting-sources", "project",
	}, args[1:]...)
	runErr := s.Exec(cmp.Or(s.Getenv(execPathEnv), agentCLI), agentArgs...)
	var out agentResult
	if err := json.Unmarshal([]byte(s.ReadFile("stdout")), &out); err != nil {
		s.Fatalf("agent: no JSON result: %v", errors.Join(runErr, err))
	}
	rec.InputTokens += out.Usage.InputTokens
	rec.OutputTokens += out.Usage.OutputTokens
	rec.CacheReadInputTokens += out.Usage.CacheReadInputTokens
	rec.CacheCreationInputTokens += out.Usage.CacheCreationInputTokens
	rec.CostUSD += out.TotalCostUSD
	rec.Turns += out.NumTurns
	switch {
	case runErr != nil:
		s.Fatalf("agent: %v", runErr)
	case out.IsError:
		s.Fatalf("agent: reported an error: %s", out.Result)
	}
}

func agentPass(s *State, neg bool, args []string) {
	if neg || len(args) != 0 {
		s.Fatalf("usage: agent-pass")
	}
	scriptRecord(s).Pass = true
}
