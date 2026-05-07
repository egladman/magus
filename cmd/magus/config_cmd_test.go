package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/config"
)

func TestRunConfigView_Text(t *testing.T) {
	cfg := config.Defaults()
	if err := runConfigView(cfg, nil); err != nil {
		t.Fatalf("runConfigView text: %v", err)
	}
}

func TestRunConfigView_JSON(t *testing.T) {
	cfg := config.Defaults()
	if err := runConfigView(cfg, []string{"-o", "json"}); err != nil {
		t.Fatalf("runConfigView json: %v", err)
	}
}

func TestRunConfigView_YAML(t *testing.T) {
	cfg := config.Defaults()
	if err := runConfigView(cfg, []string{"-o", "yaml"}); err != nil {
		t.Fatalf("runConfigView yaml: %v", err)
	}
}

func TestRunConfigView_Name(t *testing.T) {
	cfg := config.Defaults()

	// Capture stdout by redirecting within the test is complex; instead just
	// verify no error and that KnownKeys() is populated.
	_ = cfg
	keys := config.KnownKeys()
	if len(keys) == 0 {
		t.Error("KnownKeys is empty")
	}
	if err := runConfigView(cfg, []string{"-o", "name"}); err != nil {
		t.Fatalf("runConfigView name: %v", err)
	}
}

func TestRunConfigSet_Local(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if err := runConfigSet([]string{"key=cache.dir,value=/tmp/mycache"}); err != nil {
		t.Fatalf("runConfigSet local: %v", err)
	}

	path := filepath.Join(dir, config.Filename)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected %s to exist: %v", path, err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Cache.Dir != "/tmp/mycache" {
		t.Errorf("Cache.Dir = %q, want %q", cfg.Cache.Dir, "/tmp/mycache")
	}
}

func TestRunConfigSet_Global(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if err := runConfigSet([]string{"--global", "key=log.format,value=json"}); err != nil {
		t.Fatalf("runConfigSet global: %v", err)
	}

	path := filepath.Join(dir, "magus", config.Filename)
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected %s to exist: %v", path, err)
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Log.Format != "json" {
		t.Errorf("Log.Format = %q, want %q", cfg.Log.Format, "json")
	}
}

func TestRunConfigSet_UnknownKey(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	err := runConfigSet([]string{"key=not.a.real.key,value=v"})
	if err == nil {
		t.Error("expected error for unknown key, got nil")
	}
}

func TestRunConfigSet_BadInt(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	err := runConfigSet([]string{"key=parallel,value=notanumber"})
	if err == nil {
		t.Error("expected error for bad int, got nil")
	}
}

func TestRunConfigCmd_UnknownSubcommand(t *testing.T) {
	cfg := config.Defaults()
	err := configCmd(context.Background(), "", cfg, []string{"frobnicate"})
	if err == nil {
		t.Error("expected error for unknown subcommand, got nil")
	}
	if !strings.Contains(err.Error(), "frobnicate") {
		t.Errorf("error should mention subcommand name, got: %v", err)
	}
}

func TestRunConfigCmd_NoArgs(t *testing.T) {
	cfg := config.Defaults()
	if err := configCmd(context.Background(), "", cfg, nil); err != nil {
		t.Errorf("no args should print usage, not error: %v", err)
	}
}
