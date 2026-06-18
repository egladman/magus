package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnvName_NoPrefix(t *testing.T) {
	assert.Equal(t, "CACHE_DIR", EnvName("", "cache", "dir"))
}

func TestEnvName_WithPrefix(t *testing.T) {
	assert.Equal(t, "MAGUS_CACHE_DIR", EnvName("MAGUS", "cache", "dir"))
}

func TestEnvName_HyphenReplaced(t *testing.T) {
	assert.Equal(t, "MAGUS_DRY_RUN", EnvName("MAGUS", "dry-run"))
}

func TestFlagName_Parts(t *testing.T) {
	assert.Equal(t, "cache-dir", FlagName("cache", "dir"))
}

func TestFlagName_UnderscoreReplaced(t *testing.T) {
	assert.Equal(t, "dry-run", FlagName("dry_run"))
}
