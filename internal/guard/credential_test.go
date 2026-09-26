package guard

import (
	"context"
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
		"cp " + filepath.Join(state, "mcp_token") + " /tmp/x",
		"cp -r " + filepath.Join(state, "tokens.d") + " /tmp/x",
		"cp -r " + state + " /tmp/x",
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
		"ls -la " + s + "/",
		"ls " + filepath.Join(s, "tokens.d"),
		"ls ~/.local/state/magus/mcp_token",
		"du -sh " + s,
		"stat ~/.local/state/magus/mcp_token",
		"test -e ~/.local/state/magus/mcp_token && echo present",
		"[ -d ~/.local/state/magus/tokens.d ]",
		"ls " + s + " 2>/dev/null | head",
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
		"ls " + s + " && cat ~/.local/state/magus/mcp_token",
		"ls > ~/.local/state/magus/mcp_token",
		// A loop's words are not a lister's arguments.
		"for d in " + s + `; do du -sh "$d"; done`,
	} {
		assert.NotEmpty(t, denyTokenStateCommand(at, cmd, DialectBash), "should be denied: %s", cmd)
	}
}

// The editor surface: a write aimed at the token state is refused, by name or by resolving it.
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

// Through Judge, on both graded surfaces, with the rule named, and ahead of every other verdict.
func TestJudgeDeniesTheTokenStateOnEverySurface(t *testing.T) {
	state, at := tokenStateFixture(t)
	ctx := context.WithValue(t.Context(), locationKey{}, location{cacheDir: t.TempDir(), workspace: at.workspace})
	op := filepath.Join(state, "mcp_token")
	for _, envelope := range []string{
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","cwd":"` + at.workspace + `","tool_input":{"command":"cat ` + op + `"}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Write","cwd":"` + at.workspace + `","tool_input":{"file_path":"` + filepath.Join(state, "tokens.d", "evil.json") + `"}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Edit","cwd":"` + at.workspace + `","tool_input":{"file_path":"` + op + `"}}`,
	} {
		v := Judge(ctx, testDependencies(), Request{Input: envelope})
		assert.Equal(t, "deny", v.Decision, envelope)
		assert.Equal(t, string(denyRuleTokenState), v.Rule, envelope)
		assert.Contains(t, v.Reason, "token", envelope)
	}
}
