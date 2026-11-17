package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/clikit/exit"
)

func TestRunConfigView_Text(t *testing.T) {
	cfg := config.Defaults()
	require.NoError(t, runConfigView(cfg, nil))
}

func TestRunConfigView_JSON(t *testing.T) {
	cfg := config.Defaults()
	require.NoError(t, runConfigView(cfg, []string{"-o", "json"}))
}

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
	s.Require().NoError(json.Unmarshal([]byte(out), &view), out)
	s.Equal([]map[string]string{{"url": "https://go.dev/ref/mod", "topic": "go modules",
		"verdict": "follow", "why": "the reference", "contradicts": "https://blog.example/go"}}, view.Sources)
}

func TestRunConfigView_YAML(t *testing.T) {
	cfg := config.Defaults()
	require.NoError(t, runConfigView(cfg, []string{"-o", "yaml"}))
}

func TestRunConfigView_Name(t *testing.T) {
	cfg := config.Defaults()

	// Capture stdout by redirecting within the test is complex; instead just
	// verify no error and that KnownKeys() is populated.
	_ = cfg
	keys := config.KnownKeys()
	assert.NotEmpty(t, keys)
	require.NoError(t, runConfigView(cfg, []string{"-o", "name"}))
}

func TestRunConfigSet_Local(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	require.NoError(t, runConfigSet(t.Context(), []string{"key=cache.dir,value=/tmp/mycache"}))

	path := filepath.Join(dir, config.Filename)
	_, err := os.Stat(path)
	require.NoError(t, err, "expected %s to exist", path)

	cfg, err := config.Load(path)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/mycache", cfg.Cache.Dir)
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

func TestRunConfigSet_UnknownKey(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	err := runConfigSet(t.Context(), []string{"key=not.a.real.key,value=v"})
	assert.Error(t, err, "expected error for unknown key")
}

func TestRunConfigSet_BadInt(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	err := runConfigSet(t.Context(), []string{"key=parallel,value=notanumber"})
	assert.Error(t, err, "expected error for bad int")
}

func TestRunConfigCmd_UnknownSubcommand(t *testing.T) {
	cfg := config.Defaults()
	err := configCommand(context.Background(), "", cfg, []string{"frobnicate"})
	require.Error(t, err, "expected error for unknown subcommand")
	assert.Contains(t, err.Error(), "frobnicate", "error should mention subcommand name")
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

func TestStrOrDefAndIntOrDef(t *testing.T) {
	assert.Equal(t, "(default)", stringOrDef("", "(default)"))
	assert.Equal(t, "set", stringOrDef("set", "(default)"))

	// Zero means "nothing was configured", not "zero builds may run", so it renders as
	// the placeholder rather than as the number.
	assert.Equal(t, "(default)", intOrDef(0, "(default)"))
	assert.Equal(t, "8", intOrDef(8, "(default)"))
}
func TestSplitConfigCommasHonorsEscapes(t *testing.T) {
	assert.Equal(t, []string{"a", "b", "c"}, splitConfigCommas("a,b,c"))
	assert.Equal(t, []string{"go,rust"}, splitConfigCommas(`go\,rust`))
	assert.Equal(t, []string{"key=a,b", "value=c"}, splitConfigCommas(`key=a\,b,value=c`))
	assert.Equal(t, []string{""}, splitConfigCommas(""))
	// A trailing backslash is literal, not an escape with nothing to escape.
	assert.Equal(t, []string{`a\`}, splitConfigCommas(`a\`))
}
func TestParseConfigSetArg(t *testing.T) {
	key, value, err := parseConfigSetArg("key=cache.mode,value=write")
	require.NoError(t, err)
	assert.Equal(t, "cache.mode", key)
	assert.Equal(t, "write", value)

	// The value keeps its own '=' and its escaped commas.
	key, value, err = parseConfigSetArg(`key=spells,value=go\,rust`)
	require.NoError(t, err)
	assert.Equal(t, "spells", key)
	assert.Equal(t, "go,rust", value)

	// A key with no value is legal: it is how a key is set to empty.
	key, value, err = parseConfigSetArg("key=cache.dir")
	require.NoError(t, err)
	assert.Equal(t, "cache.dir", key)
	assert.Equal(t, "", value)

	_, _, err = parseConfigSetArg("cache.mode")
	assert.ErrorContains(t, err, "not in key=value form")

	_, _, err = parseConfigSetArg("k=cache.mode")
	assert.ErrorContains(t, err, `unknown field "k"`)

	_, _, err = parseConfigSetArg("value=write")
	assert.ErrorContains(t, err, "key is required")
}
