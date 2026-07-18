package main

import (
	"testing"

	"github.com/egladman/magus/internal/graph/graphurl"
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
		got := buildGraphLink(host, token, graphurl.GraphLinkOpts{
			View: "blast",
			Node: "project:pkg/foo",
		})
		require.Equal(t,
			"http://127.0.0.1:7391/console/graph/#view=blast&node=project%3Apkg%2Ffoo&token=tok",
			got)
	})

	t.Run("no host omits the link", func(t *testing.T) {
		require.Equal(t, "", buildGraphLink("", token, graphurl.GraphLinkOpts{View: "blast"}))
	})

	t.Run("no token drops the token directive but keeps the link", func(t *testing.T) {
		got := buildGraphLink(host, "", graphurl.GraphLinkOpts{View: "blast"})
		require.Equal(t,
			"http://127.0.0.1:7391/console/graph/#view=blast",
			got)
	})
}
