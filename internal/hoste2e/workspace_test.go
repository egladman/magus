//go:build e2e

package hoste2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/txtar"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// jobID is the live job row each workspace declares, so a spawn title and a lease
// marker name a job the guard can bind, and the brief lists it.
const jobID = "e2e-job"

// workspace is one throwaway magus workspace wired for one host, plus the private
// directories that keep the host and magus away from the user's own config and state.
type workspace struct {
	host  string
	root  string
	dir   string
	bin   string
	nonce string
	env   []string
	logs  string
	// ready is when setup finished, in unix milliseconds; trail events before it are
	// setup's own.
	ready int64
}

func (w *workspace) code(kind string) string { return "E2E-" + kind + "-" + w.nonce }

func (w *workspace) cacheDir() string { return filepath.Join(w.dir, ".magus") }

func (w *workspace) proofreadCalls() string { return filepath.Join(w.root, "proofread-calls") }

// repoPath resolves a path in this checkout; the suite runs from internal/hoste2e.
func repoPath(t *testing.T, rel string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func magusBinary(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("MAGUS_E2E_MAGUS")
	if bin == "" {
		bin = repoPath(t, "magus")
	}
	if info, err := os.Stat(bin); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("no executable magus at %s; build ./magus or set MAGUS_E2E_MAGUS", bin)
	}
	return bin
}

func newNonce(t *testing.T) string {
	t.Helper()
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return strings.ToUpper(hex.EncodeToString(b))
}

// scrubbedEnv drops what would tie a host or its hooks to the session running this
// suite: a lease in BAGGAGE, a nested-host marker, magus settings for another tree.
// CLAUDE_CODE_OAUTH_TOKEN is a login, not session state, so it passes through.
func scrubbedEnv() []string {
	drop := []string{"MAGUS_", "__MAGUS", "BAGGAGE=", "CLAUDECODE=", "CLAUDE_CODE_", "CLAUDE_PROJECT_DIR=", "CLAUDE_ENV_FILE=",
		"AI_AGENT=", "CODEX_", "OPENCODE_CONFIG", "XDG_STATE_HOME=", "XDG_CONFIG_HOME=", "GIT_DIR=", "GIT_WORK_TREE=", "GIT_INDEX_FILE="}
	var env []string
next:
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "CLAUDE_CODE_OAUTH_TOKEN=") {
			env = append(env, kv)
			continue
		}
		for _, prefix := range drop {
			if strings.HasPrefix(kv, prefix) {
				continue next
			}
		}
		env = append(env, kv)
	}
	return env
}

// newWorkspace builds the fixture the way a user wires a host: the harness spell copied
// into the workspace, the shipped hook templates downloaded beside it, then
// `magus describe harness` merged by the command it prints, verified, and the skills
// installed. The host's wire step adds only what magus leaves to the user as prose: the
// MCP entry, Codex's project trust, and the OpenCode plugin its guide says to copy.
//
// The directory outlives t: owner, the host's test, removes it once every scenario
// reading this session's evidence has run. MAGUS_E2E_KEEP=1 keeps it.
func newWorkspace(t, owner *testing.T, host *hostDriver) *workspace {
	t.Helper()
	tmp, err := os.MkdirTemp("", "magus-host-e2e-"+host.id+"-")
	if err != nil {
		t.Fatal(err)
	}
	owner.Cleanup(func() {
		if os.Getenv("MAGUS_E2E_KEEP") == "1" {
			owner.Logf("kept %s", tmp)
			return
		}
		_ = os.RemoveAll(tmp)
	})
	root, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal(err)
	}
	w := &workspace{host: host.id, root: root, dir: filepath.Join(root, "ws"), bin: filepath.Join(root, "bin"),
		nonce: newNonce(t), logs: filepath.Join(root, "logs")}
	for _, d := range []string{w.dir, w.bin, w.logs, filepath.Join(root, "state")} {
		mustMkdir(t, d)
	}
	magus := magusBinary(t)
	mustSymlink(t, magus, filepath.Join(w.bin, "magus"))
	mustSymlink(t, magus, filepath.Join(w.dir, "magus"))
	stub := filepath.Join(w.bin, "proofread")
	copyFile(t, filepath.Join("testdata", "proofread-stub.sh"), stub, 0o755)

	w.env = append(scrubbedEnv(),
		"PATH="+w.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"XDG_STATE_HOME="+filepath.Join(root, "state"),
		"MAGUS_SERVER_ENABLED=false",
		"MAGUS_LOG_SILENT=true",
		"__PROOFREAD_BIN="+stub,
		"MAGUS_E2E_PROOFREAD_CALLS="+w.proofreadCalls(),
	)
	w.env = append(w.env, host.env(t, w)...)

	w.writeFixture(t, host.spell)
	w.git(t, "init", "-q", "-b", "main")
	w.git(t, "add", "-A")
	w.git(t, "-c", "user.name=e2e", "-c", "user.email=e2e@example.invalid", "-c", "commit.gpgsign=false",
		"commit", "-q", "-m", "fixture")

	w.magus(t, "ls")
	w.mergeHarness(t, host.spell)
	w.magus(t, "agent", "harness", "verify", "--id", host.spell)
	w.magus(t, "agent", "harness", "install", "--id", host.spell)
	w.magus(t, "job", "fork", jobID, "--write-paths", "probe.txt", "--check", "generate .",
		"--criteria", w.code("BRIEF")+" is this lease's code word; a host end-to-end probe")
	w.probeRules(t)
	host.wire(t, w)
	w.ready = time.Now().UnixMilli()
	return w
}

func (w *workspace) writeFixture(t *testing.T, spell string) {
	t.Helper()
	archive, err := txtar.ParseFile(filepath.Join("testdata", "workspace.txtar"))
	if err != nil {
		t.Fatal(err)
	}
	fill := strings.NewReplacer("@SPELL@", spell, "@NONCE@", w.nonce)
	for _, f := range archive.Files {
		writeFile(t, filepath.Join(w.dir, f.Name), fill.Replace(string(f.Data)))
	}

	copyFile(t, repoPath(t, filepath.Join("spells", "harness", spell, "spell.buzz")),
		filepath.Join(w.dir, "spells", "harness", spell, "spell.buzz"), 0o644)

	// The templates a user downloads into docs/guides/integrations/agents/. That
	// directory's own magusfile.buzz is left behind: it would make the copy a project.
	src := repoPath(t, filepath.Join("docs", "guides", "integrations", "agents"))
	dst := filepath.Join(w.dir, "docs", "guides", "integrations", "agents")
	for _, pattern := range []string{"*.buzz", "lib/*.buzz"} {
		matches, err := filepath.Glob(filepath.Join(src, pattern))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range matches {
			rel, _ := filepath.Rel(src, m)
			if rel == "magusfile.buzz" {
				continue
			}
			copyFile(t, m, filepath.Join(dst, rel), 0o644)
		}
	}
}

// mergeHarness runs the merge command `magus describe harness <id> -o json` prints.
func (w *workspace) mergeHarness(t *testing.T, spell string) {
	t.Helper()
	var plan types.HarnessPlan
	if err := json.Unmarshal([]byte(w.magus(t, "describe", "harness", spell, "-o", "json")), &plan); err != nil {
		t.Fatalf("describe harness %s: %v", spell, err)
	}
	if plan.Merge == "" {
		t.Fatalf("describe harness %s printed no merge command for a fresh workspace", spell)
	}
	w.run(t, "sh", "-c", plan.Merge)
}

// replyCheckScript is the template a person downloads to turn the reply check on. No
// harness config ships an entry for it, so each host's wire step adds one by hand.
const replyCheckScript = "docs/guides/integrations/agents/proofread-reply.buzz"

// wireReplyCheck appends entry under event in the hooks object of config, a path in the
// workspace: the Stop entry a person writes to turn the reply check on.
func (w *workspace) wireReplyCheck(t *testing.T, config, event string, entry map[string]any) {
	t.Helper()
	path := filepath.Join(w.dir, config)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	hooks, _ := doc["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		doc["hooks"] = hooks
	}
	existing, _ := hooks[event].([]any)
	hooks[event] = append(existing, entry)
	writeJSON(t, path, doc)
}

// harnessPlan is what `magus describe harness` says after the merge, for the MCP hint.
func (w *workspace) harnessPlan(t *testing.T, spell string) types.HarnessPlan {
	t.Helper()
	var plan types.HarnessPlan
	if err := json.Unmarshal([]byte(w.magus(t, "describe", "harness", spell, "-o", "json")), &plan); err != nil {
		t.Fatalf("describe harness %s: %v", spell, err)
	}
	return plan
}

// probeRules judges the probe command and write once with a host name no scenario
// uses, so a magusfile that fails to load stops the test before any model tokens are
// spent rather than failing open in every hook.
func (w *workspace) probeRules(t *testing.T) {
	t.Helper()
	for _, args := range [][]string{
		{"shell", "--agent-name", "e2e-setup", "echo e2e-deny-probe"},
		{"shell", "--agent-name", "e2e-setup", "--path", filepath.Join(w.dir, "blocked.txt")},
	} {
		out, err := w.exec(context.Background(), "magus", args...)
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 {
			t.Fatalf("magus %s: want a deny (exit 2) from the fixture rule, got %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

func (w *workspace) magus(t *testing.T, args ...string) string {
	t.Helper()
	return w.run(t, "magus", args...)
}

func (w *workspace) git(t *testing.T, args ...string) {
	t.Helper()
	w.run(t, "git", args...)
}

func (w *workspace) run(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := w.exec(context.Background(), name, args...)
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

// exec runs name in the workspace with its environment; stdout alone is returned on
// success so JSON stays parseable, and both streams on failure.
func (w *workspace) exec(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, w.lookPath(name), args...)
	cmd.Dir = w.dir
	cmd.Env = w.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String() + stderr.String(), err
	}
	return stdout.String(), nil
}

func (w *workspace) lookPath(name string) string {
	if strings.ContainsRune(name, filepath.Separator) {
		return name
	}
	if p := filepath.Join(w.bin, name); fileExists(p) {
		return p
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name
}

// hostRun is one headless host invocation and what it left behind.
type hostRun struct {
	stdout, stderr string
	err            error
}

// runHost starts a host binary in its own process group so a timeout takes its MCP
// servers and hook children down with it, and keeps both streams under logs/.
func (w *workspace) runHost(t *testing.T, label, bin string, args []string, extraEnv ...string) hostRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), hostTimeout())
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = w.dir
	cmd.Env = append(append([]string{}, w.env...), extraEnv...)
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 10 * time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("timed out after %s: %w", hostTimeout(), err)
	}
	t.Logf("%s: %s exited after %s: %v", w.host, label, time.Since(start).Round(time.Second), err)
	writeFile(t, filepath.Join(w.logs, label+".stdout"), stdout.String())
	writeFile(t, filepath.Join(w.logs, label+".stderr"), stderr.String())
	return hostRun{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func hostTimeout() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("MAGUS_E2E_TIMEOUT")); err == nil && d > 0 {
		return d
	}
	return 6 * time.Minute
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, src, dst string, mode os.FileMode) {
	t.Helper()
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, filepath.Dir(dst))
	if err := os.WriteFile(dst, body, mode); err != nil {
		t.Fatal(err)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
