package main

import (
	"testing"

	graphurl "github.com/egladman/magus/internal/graph/url"
	"github.com/stretchr/testify/require"
)

// TestBuildGraphLink covers the pure seam under liveExplorerLink: with
// host/token/base injected, it must format the exact live deep-link, drop the
// token directive when there is no token, and omit (return "") only when there is
// no host to link to.
func TestBuildGraphLink(t *testing.T) {
	const (
		host  = "127.0.0.1:7391"
		token = "tok"
		base  = "https://example.test/console/graph/"
	)

	t.Run("full link with directives", func(t *testing.T) {
		got := buildGraphLink(host, token, base, graphurl.GraphLinkOpts{
			View: "blast",
			Node: "project:pkg/foo",
		})
		require.Equal(t,
			"https://example.test/console/graph/#live=127.0.0.1:7391&token=tok&view=blast&node=project%3Apkg%2Ffoo",
			got)
	})

	t.Run("empty base falls back to DefaultExplorerBase", func(t *testing.T) {
		got := buildGraphLink(host, token, "", graphurl.GraphLinkOpts{Node: "project:pkg/foo"})
		require.Equal(t,
			"https://eli.gladman.cc/magus/console/graph/#live=127.0.0.1:7391&token=tok&node=project%3Apkg%2Ffoo",
			got)
	})

	t.Run("no host omits the link", func(t *testing.T) {
		require.Equal(t, "", buildGraphLink("", token, base, graphurl.GraphLinkOpts{View: "blast"}))
	})

	t.Run("no token drops the token directive but keeps the link", func(t *testing.T) {
		got := buildGraphLink(host, "", base, graphurl.GraphLinkOpts{View: "blast"})
		require.Equal(t,
			"https://example.test/console/graph/#live=127.0.0.1:7391&view=blast",
			got)
	})
}

// TestGraphExplorerBase covers deriving the explorer base from cfg.Console.URL:
// the configured console base plus "graph/", tolerant of a missing trailing
// slash, and "" (so GraphLink uses its own default) when the URL is unset.
func TestGraphExplorerBase(t *testing.T) {
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })

	t.Run("appends graph to configured console url", func(t *testing.T) {
		globalCfg.Console.URL = "https://foo.test/console/"
		require.Equal(t, "https://foo.test/console/graph/", graphExplorerBase())
	})

	t.Run("tolerates a missing trailing slash", func(t *testing.T) {
		globalCfg.Console.URL = "https://foo.test/console"
		require.Equal(t, "https://foo.test/console/graph/", graphExplorerBase())
	})

	t.Run("empty console url yields empty base", func(t *testing.T) {
		globalCfg.Console.URL = ""
		require.Equal(t, "", graphExplorerBase())
	})
}
