package config

import "testing"

func TestEnvName_NoPrefix(t *testing.T) {
	if got := EnvName("", "cache", "dir"); got != "CACHE_DIR" {
		t.Errorf("EnvName(\"\", cache, dir) = %q, want CACHE_DIR", got)
	}
}

func TestEnvName_WithPrefix(t *testing.T) {
	if got := EnvName("MAGUS", "cache", "dir"); got != "MAGUS_CACHE_DIR" {
		t.Errorf("EnvName(MAGUS, cache, dir) = %q, want MAGUS_CACHE_DIR", got)
	}
}

func TestEnvName_HyphenReplaced(t *testing.T) {
	if got := EnvName("MAGUS", "dry-run"); got != "MAGUS_DRY_RUN" {
		t.Errorf("EnvName with hyphen = %q, want MAGUS_DRY_RUN", got)
	}
}

func TestFlagName_Parts(t *testing.T) {
	if got := FlagName("cache", "dir"); got != "cache-dir" {
		t.Errorf("FlagName(cache, dir) = %q, want cache-dir", got)
	}
}

func TestFlagName_UnderscoreReplaced(t *testing.T) {
	if got := FlagName("dry_run"); got != "dry-run" {
		t.Errorf("FlagName(dry_run) = %q, want dry-run", got)
	}
}
