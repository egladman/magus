package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The printed link must never carry the bearer token. liveExplorerLink used to load it
// and embed it, which put a live credential in stdout, and so in scrollback, in any
// captured run log, and in the context of whatever agent ran `magus explain`. This
// asserts the omission at the seam that actually prints, not just at buildGraphLink,
// because the regression would be a one-line reintroduction of the auth.Load() call.
func TestLiveExplorerLinkCarriesNoToken(t *testing.T) {
	got := liveExplorerLink(graphLinkOpts{View: "blast", Node: "spell:go"})
	if got == "" {
		t.Skip("no server address configured here, so there is no link to assert on")
	}
	require.NotContains(t, got, "token=",
		"the deep-link must stay unauthenticated; the token is composed in by authHint")
}

// TestConsoleLinksCarryNoToken extends TestLiveExplorerLinkCarriesNoToken to the other two
// console links, which kept embedding the token long after the Graph Explorer stopped.
//
// They relied on a terminal check instead: the token only reached an interactive user. That
// is a weaker property than it sounds: an interactive terminal is still scrollback, a
// termcast, and a screen share, and this repository has already had to rotate tokens that
// escaped through exactly those. The gate is gone now because there is no secret left to
// gate, and this is the assertion that keeps a one-line auth.Load() from bringing it back.
func TestConsoleLinksCarryNoToken(t *testing.T) {
	for name, got := range map[string]string{
		"dashboard": consoleWatchURL(),
		"diff":      consoleDiffURL(),
	} {
		if got == "" {
			continue // console disabled in this environment
		}
		require.NotContains(t, got, "token=",
			"the %s link must stay unauthenticated; the token is composed in by authHint", name)
	}
}
