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

