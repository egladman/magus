package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tokenStateFixture pins the user state dir to a temp dir and returns it with a location that
// runs calls from inside it, so relative spellings resolve there.
func tokenStateFixture(t *testing.T) (state string, at location) {
	t.Helper()
	xdg := t.TempDir()
	t.Setenv("XDG_STATE_HOME", xdg)
	state = filepath.Join(xdg, "magus")
	require.NoError(t, os.MkdirAll(filepath.Join(state, "tokens.d"), 0o700))
	return state, location{dir: state, workspace: t.TempDir()}
}

// Every shell spelling that reads, copies, writes or names the operator token file or the token
// store is refused, whatever the command and however the path is written.
func TestTokenStateCommandsAreDenied(t *testing.T) {
	state, at := tokenStateFixture(t)
	for _, cmd := range []string{
		"cat " + filepath.Join(state, "mcp_token"),
		"cat ~/.local/state/magus/mcp_token",
		`cat "$XDG_STATE_HOME/magus/mcp_token"`,
		"cat ${HOME}/Library/Application\\ Support/magus/mcp_token",
		fmt.Sprintf("cp %s /tmp/x", filepath.Join(state, "mcp_token")),
		fmt.Sprintf("cp -r %s /tmp/x", filepath.Join(state, "tokens.d")),
		fmt.Sprintf("cp -r %s /tmp/x", state),
		"cat tokens.d/laptop.json",
		"cat ./mcp_token",
		"echo '{}' > " + filepath.Join(state, "tokens.d", "evil.json"),
		"tee ~/.local/state/magus/tokens.d/evil.json < rec.json",
		"base64 < ~/.local/state/magus/mcp_token",
		`python3 -c "print(open('/home/u/.local/state/magus/mcp_token').read())"`,
		`sh -c 'cat ~/.local/state/magus/mcp_token'`,
		`jq . "$(ls ~/.local/state/magus/tokens.d/*.json)"`,
		"grep -r mgs_ ~/.local/state/magus/tokens.d",
		"cat ~/.local/state/magus/mcp_token && (", // unparsable: matched as text
		// A help flag does not exempt a line naming the token state.
		"cat ~/.local/state/magus/mcp_token --help",
	} {
		assert.NotEmpty(t, denyTokenStateCommand(at, cmd, DialectBash), "should be denied: %s", cmd)
	}
	for _, cmd := range []string{
		"cat README.md",
		"magus config console token ls",
		"grep -rn tokens.d internal/auth",
		"cat internal/auth/store.go",
		"ls ~/.local/state/magus-other",
		"cat mcp_token.go",
	} {
		assert.Empty(t, denyTokenStateCommand(location{dir: t.TempDir()}, cmd, DialectBash), "should not fire: %s", cmd)
	}
}

// A bare listing shows file names, and no name in the state dir is a secret: the operator
// file is always `mcp_token` and a store entry is `<token name>.json`, which `magus config
// mcp connector ls` already prints. It passes while the names stay names; a listing whose
// output reaches a reader, or that shares a line with one, is refused.
func TestTokenStateListingPasses(t *testing.T) {
	state, at := tokenStateFixture(t)
	s := state
	for _, cmd := range []string{
		"ls " + s,
		fmt.Sprintf("ls -la %s/", s),
		"ls " + filepath.Join(s, "tokens.d"),
		"ls ~/.local/state/magus/mcp_token",
		"du -sh " + s,
		"stat ~/.local/state/magus/mcp_token",
		"test -e ~/.local/state/magus/mcp_token && echo present",
		"[ -d ~/.local/state/magus/tokens.d ]",
		fmt.Sprintf("ls %s 2>/dev/null | head", s),
		"ls ~/.local/state/magus/tokens.d | grep laptop | wc -l",
	} {
		assert.Empty(t, denyTokenStateCommand(at, cmd, DialectBash), "a listing reads no secret: %s", cmd)
	}
	for _, cmd := range []string{
		// The names reach something that opens them.
		"ls -d ~/.local/state/magus/tokens.d/* | xargs cat",
		"ls ~/.local/state/magus/tokens.d | awk '{system(\"cat \" $0)}'",
		`jq . "$(ls ~/.local/state/magus/tokens.d/*.json)"`,
		"cat <(ls ~/.local/state/magus/tokens.d)",
		// A listing beside a read, or written over.
		fmt.Sprintf("ls %s && cat ~/.local/state/magus/mcp_token", s),
		"ls > ~/.local/state/magus/mcp_token",
		// A loop's words are not a lister's arguments.
		fmt.Sprintf("for d in %s; do du -sh \"$d\"; done", s),
	} {
		assert.NotEmpty(t, denyTokenStateCommand(at, cmd, DialectBash), "should be denied: %s", cmd)
	}
}

// The state dir holds more than the secrets, so a file beside them is readable and a command
// that walks the dir, or a glob that would match a secret, is not.
func TestTokenStateSiblingsPassAndWalksAreDenied(t *testing.T) {
	state, at := tokenStateFixture(t)
	for _, cmd := range []string{
		"cat " + filepath.Join(state, "server.log"),
		"tail -n 20 " + filepath.Join(state, "daemon.pid"),
		fmt.Sprintf("cat %s/*.log", state),
		"ls " + state,
	} {
		assert.Empty(t, denyTokenStateCommand(at, cmd, DialectBash), "a sibling of the secrets is not one: %s", cmd)
	}
	for _, cmd := range []string{
		"grep -r token " + state,
		"rg token " + state,
		fmt.Sprintf("find %s -type f", state),
		"tar cf /tmp/x.tar " + state,
		fmt.Sprintf("cat %s/*", state),
		fmt.Sprintf("for f in %s; do cat $f; done", state),
	} {
		assert.NotEmpty(t, denyTokenStateCommand(at, cmd, DialectBash), "should be denied: %s", cmd)
	}
}

// The editor write: a write aimed at the token state is refused, by name or by resolving it.
func TestTokenStatePathsAreDenied(t *testing.T) {
	state, at := tokenStateFixture(t)
	for _, p := range []string{
		filepath.Join(state, "mcp_token"),
		filepath.Join(state, "tokens.d", "evil.json"),
		filepath.Join(state, "tokens.d", "..", "tokens.d", "evil.json"),
		"tokens.d/evil.json",
		"/home/someone/.local/state/magus/tokens.d/x.json",
	} {
		assert.NotEmpty(t, denyTokenStatePath(at, p), "should be denied: %s", p)
	}
	for _, p := range []string{"internal/auth/store.go", filepath.Join(at.workspace, "tokens.go")} {
		assert.Empty(t, denyTokenStatePath(location{dir: at.workspace}, p), "should not fire: %s", p)
	}
}

// Through Judge, on both graded inputs, with the rule named, and ahead of every other verdict.
func TestJudgeDeniesTheTokenStateOnEveryInput(t *testing.T) {
	state, at := tokenStateFixture(t)
	ctx := context.WithValue(t.Context(), locationKey{}, location{cacheDir: t.TempDir(), workspace: at.workspace})
	op := filepath.Join(state, "mcp_token")
	for _, envelope := range []string{
		fmt.Sprintf("{\"hook_event_name\":\"PreToolUse\",\"tool_name\":\"Bash\",\"cwd\":\"%s\",\"tool_input\":{\"command\":\"cat %s\"}}", at.workspace, op),
		fmt.Sprintf("{\"hook_event_name\":\"PreToolUse\",\"tool_name\":\"Write\",\"cwd\":\"%s\",\"tool_input\":{\"file_path\":\"%s\"}}", at.workspace, filepath.Join(state, "tokens.d", "evil.json")),
		fmt.Sprintf("{\"hook_event_name\":\"PreToolUse\",\"tool_name\":\"Edit\",\"cwd\":\"%s\",\"tool_input\":{\"file_path\":\"%s\"}}", at.workspace, op),
	} {
		v := Judge(ctx, testDependencies(), Request{Input: envelope})
		assert.Equal(t, verdictWithRule("deny", string(denyRuleTokenState)), unworded(v), envelope)
		assert.Contains(t, v.Reason, "token", envelope)
	}
}
