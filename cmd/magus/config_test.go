package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type configViewSuite struct {
	suite.Suite
}

func TestConfigViewSuite(t *testing.T) {
	suite.Run(t, new(configViewSuite))
}

// The Cursor hook reads the reading list here; these keys are its contract.
func (s *configViewSuite) TestJSONCarriesTheReadingList() {
	cfg := config.Defaults()
	cfg.Sources = []config.Source{{URL: "https://go.dev/ref/mod", Topic: "go modules",
		Verdict: config.SourceFollow, Why: "the reference", Contradicts: "https://blog.example/go"}}
	out := captureStdout(s.T(), func() {
		s.Require().NoError(runConfigView(cfg, []string{"-o", "json"}))
	})
	var view struct {
		Sources []map[string]string `json:"sources"`
	}
	s.Require().NoError(codec.Unmarshal([]byte(out), &view), out)
	s.Equal([]map[string]string{{"url": "https://go.dev/ref/mod", "topic": "go modules",
		"verdict": "follow", "why": "the reference", "contradicts": "https://blog.example/go"}}, view.Sources)
}

func TestRunConfigSet_Global(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	require.NoError(t, runConfigSet(t.Context(), []string{"--global", "key=log.format,value=json"}))

	path := filepath.Join(dir, "magus", config.Filename)
	_, err := os.Stat(path)
	require.NoError(t, err, "expected %s to exist", path)

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "json", cfg.Log.Format)
}

func TestRunConfigSet_BadInt(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	err := runConfigSet(t.Context(), []string{"key=parallel,value=notanumber"})
	assert.Error(t, err, "expected error for bad int")
}

// TestRunConfigCmd_NoArgs pins that a missing subcommand is a USAGE ERROR, not a
// quiet success. It previously returned nil, so `magus config` exited 0 and a script
// could not tell a bare invocation from one that did work.
// Usage errors exit 2 across the CLI.
func TestRunConfigCmd_NoArgs(t *testing.T) {
	cfg := config.Defaults()
	err := configCommand(context.Background(), "", cfg, nil)
	require.Error(t, err, "no args should be a usage error, not a quiet success")

	assert.Equal(t, exit.Usage, mapExitCode(err))
}

func TestMintTokenNamesAndRefusesAReusedName(t *testing.T) {
	outsideAnyWorkspace(t)
	mint := func(name string, g auth.Access) (auth.Token, error) {
		_, rec, err := mintToken(auth.MintRequest{Name: name, Grant: g, TTL: time.Hour}, false)
		return rec, err
	}
	for _, c := range []struct {
		grant auth.Access
		want  string
	}{{auth.AccessConnector(), "connector-1"}, {auth.AccessConnector(), "connector-2"}, {auth.AccessViewer(), "console-1"}} {
		rec, err := mint("", c.grant)
		require.NoError(t, err)
		assert.Equal(t, c.want, rec.Name)
	}
	_, err := mint("connector-1", auth.AccessConsole())
	assert.ErrorIs(t, err, types.TokenNameExists)
}

// A console link's code that has expired is gone after the next mint: every `open` line mints
// one, and they must not pile up in tokens.d.
func TestExpiredConsoleLinkCodeDisappearsOnTheNextMint(t *testing.T) {
	outsideAnyWorkspace(t)
	first, err := mintConsoleLinkCode()
	require.NoError(t, err)
	store, err := openTokenStore()
	require.NoError(t, err)
	toks, err := store.List()
	require.NoError(t, err)
	require.Len(t, toks, 1)
	old := toks[0]

	// Time passes: rewrite the record into the past.
	dir, err := auth.StoreDir()
	require.NoError(t, err)
	path := filepath.Join(dir, old.Name+".json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	expired := time.Now().Add(-time.Second).UTC()
	stale := strings.Replace(string(raw), old.Expires.Format(time.RFC3339Nano), expired.Format(time.RFC3339Nano), 1)
	stale = strings.Replace(stale, old.Created.Format(time.RFC3339Nano), expired.Add(-auth.ExchangeCodeTTL).Format(time.RFC3339Nano), 1)
	require.NotEqual(t, string(raw), stale, "the fixture must actually move the expiry")
	require.NoError(t, os.WriteFile(path, []byte(stale), 0o600))
	later := time.Now().Add(time.Second)
	require.NoError(t, os.Chtimes(path, later, later))

	_, err = mintConsoleLinkCode()
	require.NoError(t, err)
	toks, err = store.List()
	require.NoError(t, err)
	require.Len(t, toks, 1, "the expired code is removed, the new one kept")
	assert.NotEqual(t, old.ID, toks[0].ID)
	_, _, err = store.Redeem(first)
	assert.ErrorIs(t, err, auth.ErrTokenNotFound)
	files, err := filepath.Glob(filepath.Join(dir, "*"))
	require.NoError(t, err)
	assert.Len(t, files, 1, "exactly one file left on disk")
}

// outsideAnyWorkspace moves the test to a directory no magusfile governs, so a CLI mint's
// audit has no workspace trail to land in and writes none into this repository's.
func outsideAnyWorkspace(t *testing.T) {
	t.Helper()
	testkit.Isolate(t)
	t.Chdir(t.TempDir())
}

// A console link carries a one-time code rather than a token: the code verifies nowhere, and
// traded once it yields a console=write token living console.LinkTokenLifetime.
func TestConsoleLinkCarriesAOneTimeCode(t *testing.T) {
	outsideAnyWorkspace(t)
	code, err := mintConsoleLinkCode()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(code, "mgx_"), code)
	_, ok := auth.Verify(code)
	assert.False(t, ok, "a code is no bearer")

	store, err := openTokenStore()
	require.NoError(t, err)
	secret, tok, err := store.Redeem(code)
	require.NoError(t, err)
	cred, ok := auth.Verify(secret.Reveal())
	require.True(t, ok)
	assert.Equal(t, auth.Credential{Kind: auth.KindStored, ID: tok.ID, Name: tok.Name, Grant: auth.AccessConsole()}, cred)
	assert.WithinDuration(t, time.Now().Add(console.LinkTokenLifetime), tok.Expires, time.Minute)
	_, _, err = store.Redeem(code)
	assert.ErrorIs(t, err, auth.ErrTokenNotFound, "spent on first use")
}

// Every CLI mint writes one trail event naming what was minted, by class, id and grant, and
// by whom, and none carries the secret.
func TestCLIMintsAreAudited(t *testing.T) {
	testkit.Isolate(t)
	w := testkit.NewWorkspace(t)
	w.Magusfile("")
	root := w.Root()
	t.Chdir(root)

	secret, rec, err := mintToken(auth.MintRequest{Name: "laptop", Grant: auth.AccessConsole(), TTL: time.Hour}, false)
	require.NoError(t, err)
	code, codeRec, err := mintToken(auth.MintRequest{Grant: auth.AccessViewer(), TTL: time.Hour}, true)
	require.NoError(t, err)

	base, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
	require.NoError(t, err)
	events, err := loggedCalls(t.Context(), base, 10)
	require.NoError(t, err)
	require.Len(t, events, 2)
	want := map[string]auth.Token{"cli.mint": rec, "link.code": codeRec}
	for _, e := range events {
		minted, ok := want[e.Name]
		require.True(t, ok, e.Name)
		assert.Equal(t, journal.CallKindTokenLifecycle, e.Kind)
		assert.Contains(t, e.Preview, minted.ID)
		assert.Contains(t, e.Preview, minted.Grant.String())
		blob, err := loggedPayload(base, e.Request.Ref)
		require.NoError(t, err)
		for _, s := range []string{secret, code} {
			assert.NotContains(t, string(blob), s)
			assert.NotContains(t, e.Preview, s)
		}
		assert.Contains(t, string(blob), fmt.Sprintf("\"class\":\"%s\"", string(minted.Kind)))
		assert.Contains(t, string(blob), `"class":"operator"`, "the minter is named")
	}
}

// Both token commands list the whole store, so neither hides a token the other minted, and
// revoke by an exact id or an exact name only.
func TestTokenCommandsShareOneStore(t *testing.T) {
	outsideAnyWorkspace(t)
	_, conn, err := mintToken(auth.MintRequest{Grant: auth.AccessConnector(), TTL: time.Hour}, false)
	require.NoError(t, err)
	_, _, err = mintToken(auth.MintRequest{Name: "laptop", Grant: auth.AccessConsole(), TTL: time.Hour}, false)
	require.NoError(t, err)

	require.Error(t, tokenRevoke("config console token revoke", []string{conn.ID[:4]}), "no prefix")
	require.NoError(t, tokenRevoke("config console token revoke", []string{conn.ID}), "a console command revokes a connector by id")
	require.NoError(t, tokenRevoke("config mcp connector revoke", []string{"laptop"}), "and the reverse by name")
	store, err := openTokenStore()
	require.NoError(t, err)
	left, err := store.List()
	require.NoError(t, err)
	assert.Empty(t, left)
}
