package main

import (
	"testing"

	"github.com/egladman/magus/internal/graph/url"
	"github.com/stretchr/testify/require"
)

// TestBuildGraphLink covers the pure seam under liveExplorerLink: with host/token
// injected, it must format the exact daemon-origin deep-link
// (http://<host>/console/graph/#<directives>&token=), drop the token directive
// when there is no token, and omit (return "") only when there is no host to link to.
func TestBuildGraphLink(t *testing.T) {
	const (
		host  = "127.0.0.1:7391"
		token = "tok"
	)

	t.Run("full link with directives", func(t *testing.T) {
		got := buildGraphLink(host, token, url.GraphLinkOpts{
			View: "blast",
			Node: "project:pkg/foo",
		})
		require.Equal(t,
			"http://127.0.0.1:7391/console/graph/#view=blast&node=project%3Apkg%2Ffoo&token=tok",
			got)
	})

	t.Run("no host omits the link", func(t *testing.T) {
		require.Equal(t, "", buildGraphLink("", token, url.GraphLinkOpts{View: "blast"}))
	})

	t.Run("no token drops the token directive but keeps the link", func(t *testing.T) {
		got := buildGraphLink(host, "", url.GraphLinkOpts{View: "blast"})
		require.Equal(t,
			"http://127.0.0.1:7391/console/graph/#view=blast",
			got)
	})
}

// The printed link must never carry the bearer token. liveExplorerLink used to load it
// and embed it, which put a live credential in stdout - and so in scrollback, in any
// captured run log, and in the context of whatever agent ran `magus explain`. This
// asserts the omission at the seam that actually prints, not just at buildGraphLink,
// because the regression would be a one-line reintroduction of the auth.Load() call.
func TestLiveExplorerLinkCarriesNoToken(t *testing.T) {
	got := liveExplorerLink(url.GraphLinkOpts{View: "blast", Node: "spell:go"})
	if got == "" {
		t.Skip("no daemon address configured here, so there is no link to assert on")
	}
	require.NotContains(t, got, "token=",
		"the deep-link must stay unauthenticated; the token is composed in by authHint")
}
