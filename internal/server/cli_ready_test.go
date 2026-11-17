package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsServerRun(t *testing.T) {
	assert.True(t, IsServerRun([]string{"start"}))
	assert.True(t, IsServerRun([]string{"start", "--foreground"}))
	assert.True(t, IsServerRun([]string{"--foreground"}), "the detached child runs the server")
	assert.False(t, IsServerRun([]string{"start", "-h"}), "help prints usage and builds nothing")
	assert.False(t, IsServerRun([]string{"stop"}))
	assert.False(t, IsServerRun(nil))
}

func TestIsServerStartHelpSkipsTheSubcommand(t *testing.T) {
	assert.True(t, isServerStartHelp([]string{"start", "-h"}))
	assert.True(t, isServerStartHelp([]string{"start", "--foreground", "--help"}))
	assert.True(t, isServerStartHelp([]string{"start", "help"}))
	assert.False(t, isServerStartHelp([]string{"start"}))
	assert.False(t, isServerStartHelp([]string{"start", "--foreground"}))
	assert.False(t, isServerStartHelp([]string{"help"}), "the subcommand itself is not a help flag")
}

func TestWantsForeground(t *testing.T) {
	assert.True(t, WantsForeground([]string{"start", "--foreground"}))
	assert.True(t, WantsForeground([]string{"start", "-foreground"}))
	// A pre-parse scanner takes only the bare spellings; an =value form is not a bool.
	assert.False(t, WantsForeground([]string{"start", "--foreground=true"}))
	assert.False(t, WantsForeground([]string{"start"}))
}
