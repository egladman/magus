package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestUsageNamesEverySubcommand: the usage line was maintained by hand and named
// 13 of the 20 registered scribes, so seven subcommands were unreachable to
// anyone who did not read the source.
func TestUsageNamesEverySubcommand(t *testing.T) {
	got := usageLine()
	for name := range scribes {
		require.Contains(t, got, name, "usage does not name %q", name)
	}
	require.Equal(t, len(scribes)-1, strings.Count(got, "|"), "one separator per subcommand pair")
}
