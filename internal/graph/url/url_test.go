package url

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// loopHost is the discovered daemon host:port the caller would pass in. It is the
// link's ORIGIN under the daemon-origin grammar: http://<loopHost>/console/graph/.
const loopHost = "127.0.0.1:7391"

// tok is a representative bearer token (mgs_ + alnum, no chars that encode).
const tok = "mgs_abc123DEF456"

func TestGraphLink(t *testing.T) {
	tests := []struct {
		name string
		opts GraphLinkOpts
		want string
		err  error
	}{
		{
			name: "query only",
			opts: GraphLinkOpts{
				Host:  loopHost,
				Token: tok,
				Query: "kind:target",
			},
			want: "http://127.0.0.1:7391/console/graph/#q=kind%3Atarget&token=mgs_abc123DEF456",
		},
		{
			name: "view plus node",
			opts: GraphLinkOpts{
				Host:  loopHost,
				Token: tok,
				View:  "blast",
				Node:  "target:build",
			},
			want: "http://127.0.0.1:7391/console/graph/#view=blast&node=target%3Abuild&token=mgs_abc123DEF456",
		},
		{
			name: "all set: trace view with node and to, plus query",
			opts: GraphLinkOpts{
				Host:  loopHost,
				Token: tok,
				Query: "a b",
				View:  "trace",
				Node:  "target:a",
				To:    "target:b",
			},
			// q= is percent-encoded (space -> %20, not +) so the page's
			// decodeURIComponent reads it back verbatim. Content directives lead; the
			// token is emitted last.
			want: "http://127.0.0.1:7391/console/graph/#q=a%20b&view=trace&node=target%3Aa&to=target%3Ab&token=mgs_abc123DEF456",
		},
		{
			name: "only the token: bare token fragment",
			opts: GraphLinkOpts{
				Host:  loopHost,
				Token: tok,
			},
			want: "http://127.0.0.1:7391/console/graph/#token=mgs_abc123DEF456",
		},
		{
			name: "no daemon: empty host yields sentinel",
			opts: GraphLinkOpts{
				Host:  "",
				Token: tok,
				Query: "kind:target",
			},
			err: ErrNoDaemon,
		},
		{
			name: "empty token omits the token directive",
			opts: GraphLinkOpts{
				Host: loopHost,
				View: "blast",
			},
			want: "http://127.0.0.1:7391/console/graph/#view=blast",
		},
		{
			name: "nothing set: bare console origin, empty fragment",
			opts: GraphLinkOpts{
				Host: loopHost,
			},
			want: "http://127.0.0.1:7391/console/graph/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GraphLink(tt.opts)
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
				require.Equal(t, "", got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
