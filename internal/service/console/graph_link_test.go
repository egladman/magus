package console

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	loopHost = "127.0.0.1:7391"
	tok      = "mgx_abc123DEF456"
)

func TestGraphLinkQueryOnly(t *testing.T) {
	got, err := graphLink(GraphLinkOpts{Host: loopHost, Code: tok, Query: "kind:target"})
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:7391/console/graph/#q=kind%3Atarget&code=mgx_abc123DEF456", got)
}

func TestGraphLinkViewAndNode(t *testing.T) {
	got, err := graphLink(GraphLinkOpts{Host: loopHost, Code: tok, View: "blast", Node: "target:build"})
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:7391/console/graph/#view=blast&node=target%3Abuild&code=mgx_abc123DEF456", got)
}

func TestGraphLinkEveryDirectiveEncodesAndLeadsTheCode(t *testing.T) {
	got, err := graphLink(GraphLinkOpts{
		Host: loopHost, Code: tok, Query: "a b", View: "trace", Node: "target:a", To: "target:b",
	})
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:7391/console/graph/#q=a%20b&view=trace&node=target%3Aa&to=target%3Ab&code=mgx_abc123DEF456", got)
}

func TestGraphLinkOnlyTheCode(t *testing.T) {
	got, err := graphLink(GraphLinkOpts{Host: loopHost, Code: tok})
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:7391/console/graph/#code=mgx_abc123DEF456", got)
}

func TestGraphLinkEmptyHostIsAnError(t *testing.T) {
	got, err := graphLink(GraphLinkOpts{Code: tok, Query: "kind:target"})
	require.Error(t, err)
	require.Equal(t, "", got)
}

func TestGraphLinkNothingSetIsTheBareConsole(t *testing.T) {
	got, err := graphLink(GraphLinkOpts{Host: loopHost})
	require.NoError(t, err)
	require.Equal(t, "http://127.0.0.1:7391/console/graph/", got)
}

// TestBuildGraphLink covers the pure seam under LiveExplorerLink: with host/code
// injected, it must format the exact server-origin deep-link
// (http://<host>/console/graph/#<directives>&code=), drop the code directive
// when there is no code, and omit (return "") only when there is no host to link to.
func TestBuildGraphLink(t *testing.T) {
	const (
		host = "127.0.0.1:7391"
		code = "mgx_code"
	)

	t.Run("full link with directives", func(t *testing.T) {
		got := BuildGraphLink(host, code, GraphLinkOpts{
			View: "blast",
			Node: "project:pkg/foo",
		})
		require.Equal(t,
			"http://127.0.0.1:7391/console/graph/#view=blast&node=project%3Apkg%2Ffoo&code=mgx_code",
			got)
	})

	t.Run("no host omits the link", func(t *testing.T) {
		require.Equal(t, "", BuildGraphLink("", code, GraphLinkOpts{View: "blast"}))
	})

	t.Run("no code drops the code directive but keeps the link", func(t *testing.T) {
		got := BuildGraphLink(host, "", GraphLinkOpts{View: "blast"})
		require.Equal(t,
			"http://127.0.0.1:7391/console/graph/#view=blast",
			got)
	})
}

// The sign-in line is a command a person can run as printed: the code is a substitution
// their shell expands, never a value this process read, and what reaches the opener's argv
// is a one-time code rather than a token.
func TestAuthHintIsRunnable(t *testing.T) {
	got := AuthHint("http://127.0.0.1:7391/console/plan/#job=a")
	require.Contains(t, got, `"http://127.0.0.1:7391/console/plan/#job=a&code=$(`)
	require.Contains(t, got, `config console token create --code --expires 12h)"`)
	require.NotContains(t, got, "token=$(", "no token in argv")
	require.NotContains(t, got, "config token print", "a sign-in line never hands the operator token to a browser")
}
