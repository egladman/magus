package clispec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRoffPages(t *testing.T) {
	pages := RoffPages("2026-07-26", "v1.2.3")
	require.Len(t, pages, 1+len(All))

	byName := make(map[string]string, len(pages))
	for _, page := range pages {
		byName[page.Name] = string(page.Content)
	}
	assert.Contains(t, byName["magus.1"], `.TH MAGUS 1 "2026-07-26" "magus v1.2.3"`)
	assert.Contains(t, byName["magus.1"], "magus\\-man")
	assert.Contains(t, byName["magus-run.1"], ".SH OPTIONS")
	assert.Contains(t, byName["magus-man.1"], "embedded section 1 man pages")
}

// TestInitCommandHasSynopsis pins that magus-init.1's SYNOPSIS section actually names
// the command, rather than emitting a bare ".B " line - which happens whenever a
// Command carries no Usage field (renderCommandRoff has nothing to escape and print).
func TestInitCommandHasSynopsis(t *testing.T) {
	pages := RoffPages("2026-07-26", "v1.2.3")
	byName := make(map[string]string, len(pages))
	for _, page := range pages {
		byName[page.Name] = string(page.Content)
	}
	content, ok := byName["magus-init.1"]
	require.True(t, ok, "magus-init.1 rendered")
	assert.NotContains(t, content, ".SH SYNOPSIS\n.B \n.SH DESCRIPTION",
		"SYNOPSIS must not be a bare .B line")
	assert.Contains(t, content, ".B magus\n.RI \"init [flags]\"", "SYNOPSIS names the command")
}

// TestExitStatusRoffSectionIsOptional pins both halves of the contract: a command
// declaring codes gets the section, and one declaring none gets no empty heading.
func TestExitStatusRoffSectionIsOptional(t *testing.T) {
	with := Command{
		Name:  "guard",
		Short: "judge one command",
		Usage: "magus guard [flags]",
		ExitStatus: []ExitCode{
			{0, "allowed"},
			{2, "denied, and also a well-formed call magus could not parse"},
		},
	}
	rendered := string(renderCommandRoff(with, "2026-07-26", "v1.2.3"))
	assert.Contains(t, rendered, ".SH EXIT STATUS")
	assert.Contains(t, rendered, `\fB0\fR`)
	assert.Contains(t, rendered, `\fB2\fR`)
	// The meaning is escaped like any other prose: an unescaped hyphen renders as a
	// soft-hyphen break rather than a minus.
	assert.Contains(t, rendered, `well\-formed`)

	without := Command{Name: "guard", Short: "judge one command", Usage: "magus guard [flags]"}
	assert.NotContains(t, string(renderCommandRoff(without, "2026-07-26", "v1.2.3")), "EXIT STATUS")
}

func TestRoffPagesAreDeterministic(t *testing.T) {
	first := RoffPages("", "")
	second := RoffPages("", "")
	require.Equal(t, first, second)
	for _, page := range first {
		assert.True(t, strings.HasSuffix(page.Name, ".1"))
	}
}
