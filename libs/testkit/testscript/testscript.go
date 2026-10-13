package testscript

import (
	"bytes"
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

// Env and TestScript are go-internal's, named here so a file that writes setup hooks
// and commands imports this package alone.
type (
	Env        = testscript.Env
	TestScript = testscript.TestScript
)

// Option configures Run.
type Option func(*options)

type options struct {
	setups []func(*Env) error
	cmds   map[string]func(ts *TestScript, neg bool, args []string)
	agent  *Agent
}

// WithSetup adds a hook that runs before each script, after Run has isolated the
// script's environment, the way testify's SetupTest runs before each test. Hooks run in
// the order given, and an error fails the script.
func WithSetup(fn func(*Env) error) Option {
	return func(o *options) { o.setups = append(o.setups, fn) }
}

// WithCmds adds script commands, as testscript.Params.Cmds does.
func WithCmds(cmds map[string]func(ts *TestScript, neg bool, args []string)) Option {
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
func Run(t *testing.T, dir string, opts ...Option) {
	t.Helper()
	o := options{cmds: map[string]func(*TestScript, bool, []string){}}
	for _, opt := range opts {
		opt(&o)
	}
	if o.agent != nil {
		o.cmds["agent"] = runAgent
		o.cmds["agent-pass"] = agentPass
	}
	testscript.Run(t, testscript.Params{
		Dir: dir,
		Setup: func(e *Env) error {
			if err := isolate(e); err != nil {
				return err
			}
			// Before the hooks, so a script whose setup fails still records its row.
			if o.agent != nil {
				o.agent.record(e)
			}
			for _, fn := range o.setups {
				if err := fn(e); err != nil {
					return err
				}
			}
			return nil
		},
		Cmds: o.cmds,
	})
}

func isolate(e *Env) error {
	root, err := os.MkdirTemp("", "tk")
	if err != nil {
		return fmt.Errorf("testscript: %w", err)
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

// InstallSkills returns a setup hook that installs one variant's skills into the
// script's .claude/skills. A variant naming a directory is copied there; any other is a
// skill form (full, short, both) for `magus agent install --skill-form`, run with the
// magus on the test process's PATH.
func InstallSkills(variant string) func(*Env) error {
	return func(e *Env) error {
		dest := filepath.Join(".claude", "skills")
		if info, err := os.Stat(variant); err == nil && info.IsDir() {
			if err := os.CopyFS(filepath.Join(e.WorkDir, dest), os.DirFS(variant)); err != nil {
				return fmt.Errorf("testscript: copy skills: %w", err)
			}
			return nil
		}
		cmd := exec.Command("magus", "agent", "install", "--skill-form", variant, dest)
		cmd.Dir = e.WorkDir
		cmd.Env = e.Vars
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("testscript: magus agent install: %s: %w", bytes.TrimSpace(out), err)
		}
		return nil
	}
}

// Agent turns on the agent commands; see WithAgent.
type Agent struct {
	Variant string // recorded on every row: the skill form or skills directory under test
	Rep     int    // recorded on every row
	Results string // the JSONL file every script appends its row to
}

// Result is one script's row in Agent.Results. The usage, cost and turns sum every
// agent command the script ran.
type Result struct {
	Variant                  string  `json:"variant"`
	Task                     string  `json:"task"`
	Rep                      int     `json:"rep"`
	Pass                     bool    `json:"pass"`
	InputTokens              int     `json:"input_tokens"`
	OutputTokens             int     `json:"output_tokens"`
	CacheReadInputTokens     int     `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int     `json:"cache_creation_input_tokens"`
	CostUSD                  float64 `json:"cost_usd"`
	Turns                    int     `json:"turns"`
}

// agentCLI is the agent the agent command runs. Its flags and its JSON result, from
// usage to num_turns, are that CLI's.
const agentCLI = "claude"

// WithAgent adds two script commands and appends one Result per script to a.Results
// when the script ends, whether it passed or failed.
//
//	agent <prompt-file> [arg...]
//
// runs the agent CLI in print mode, `-p <prompt> --output-format json
// --strict-mcp-config --setting-sources project [arg...]`, in the script's current
// directory with the script's environment, and adds the run's usage to the row. Its
// stdout is the agent's JSON result. It fails the script when the agent exits non-zero
// or reports an error, after counting what the run spent.
//
//	agent-pass
//
// marks the row passed. A script ends with its check and then agent-pass, so one that
// fails before it records pass=false.
func WithAgent(a Agent) Option {
	return func(o *options) { o.agent = &a }
}

type resultKey struct{}

func (a *Agent) record(e *Env) {
	r := &Result{
		Variant: a.Variant,
		// testscript names a script's work directory "script-" plus the script's name.
		Task: strings.TrimPrefix(filepath.Base(e.WorkDir), "script-"),
		Rep:  a.Rep,
	}
	e.Values[resultKey{}] = r
	e.Defer(func() {
		err := appendResult(a.Results, r)
		if err == nil {
			return
		}
		// Errorf rather than Fatal: this runs as the script unwinds, perhaps from a
		// failure already, and a second Goexit there would hide the first. Run's T has
		// Errorf; only a RunT caller's might not.
		if t, ok := e.T().(interface{ Errorf(string, ...any) }); ok {
			t.Errorf("testscript: %v", err)
			return
		}
		e.T().Fatal("testscript: ", err)
	})
}

// resultsMu keeps two parallel scripts' rows from interleaving in one file.
var resultsMu sync.Mutex

func appendResult(path string, r *Result) (err error) {
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	resultsMu.Lock()
	defer resultsMu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	_, err = f.Write(append(line, '\n'))
	return err
}

func scriptResult(ts *TestScript) *Result {
	r, ok := ts.Value(resultKey{}).(*Result)
	if !ok {
		ts.Fatalf("agent: the script has no row; Run with WithAgent sets one up")
	}
	return r
}

// agentResult is the part of the agent's JSON result a row records.
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

func runAgent(ts *TestScript, neg bool, args []string) {
	if neg || len(args) == 0 {
		ts.Fatalf("usage: agent <prompt-file> [arg...]")
	}
	r := scriptResult(ts)
	agentArgs := append([]string{
		// The prompt directly after -p: a variadic flag such as --allowedTools would
		// swallow a positional that followed it.
		"-p", ts.ReadFile(args[0]),
		"--output-format", "json",
		"--strict-mcp-config",
		"--setting-sources", "project",
	}, args[1:]...)
	runErr := ts.Exec(agentCLI, agentArgs...)
	var out agentResult
	if err := json.Unmarshal([]byte(ts.ReadFile("stdout")), &out); err != nil {
		ts.Fatalf("agent: no JSON result: %v", errors.Join(runErr, err))
	}
	r.InputTokens += out.Usage.InputTokens
	r.OutputTokens += out.Usage.OutputTokens
	r.CacheReadInputTokens += out.Usage.CacheReadInputTokens
	r.CacheCreationInputTokens += out.Usage.CacheCreationInputTokens
	r.CostUSD += out.TotalCostUSD
	r.Turns += out.NumTurns
	switch {
	case runErr != nil:
		ts.Fatalf("agent: %v", runErr)
	case out.IsError:
		ts.Fatalf("agent: reported an error: %s", out.Result)
	}
}

func agentPass(ts *TestScript, neg bool, args []string) {
	if neg || len(args) != 0 {
		ts.Fatalf("usage: agent-pass")
	}
	scriptResult(ts).Pass = true
}
