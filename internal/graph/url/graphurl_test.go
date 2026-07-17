package url

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// injectedBase is a stand-in explorer origin so the tests assert exact URLs
// without depending on the hosted default (and without a live daemon).
const injectedBase = "http://127.0.0.1:7391/console/graph/"

// loopHost is the discovered daemon host:port the caller would pass in.
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
				ExplorerBase: injectedBase,
				Host:         loopHost,
				Token:        tok,
				Query:        "kind:target",
			},
			want: "http://127.0.0.1:7391/console/graph/#live=127.0.0.1:7391&token=mgs_abc123DEF456&q=kind%3Atarget",
		},
		{
			name: "view plus node",
			opts: GraphLinkOpts{
				ExplorerBase: injectedBase,
				Host:         loopHost,
				Token:        tok,
				View:         "blast",
				Node:         "target:build",
			},
			want: "http://127.0.0.1:7391/console/graph/#live=127.0.0.1:7391&token=mgs_abc123DEF456&view=blast&node=target%3Abuild",
		},
		{
			name: "all set: trace view with node and to, plus query",
			opts: GraphLinkOpts{
				ExplorerBase: injectedBase,
				Host:         loopHost,
				Token:        tok,
				Query:        "a b",
				View:         "trace",
				Node:         "target:a",
				To:           "target:b",
			},
			// q= is percent-encoded (space -> %20, not +) so the page's
			// decodeURIComponent reads it back verbatim.
			want: "http://127.0.0.1:7391/console/graph/#live=127.0.0.1:7391&token=mgs_abc123DEF456&q=a%20b&view=trace&node=target%3Aa&to=target%3Ab",
		},
		{
			name: "none set: bare live directive",
			opts: GraphLinkOpts{
				ExplorerBase: injectedBase,
				Host:         loopHost,
				Token:        tok,
			},
			want: "http://127.0.0.1:7391/console/graph/#live=127.0.0.1:7391&token=mgs_abc123DEF456",
		},
		{
			name: "default explorer base when unset",
			opts: GraphLinkOpts{
				Host:  loopHost,
				Token: tok,
				Query: "orphans",
			},
			want: "https://eli.gladman.cc/magus/console/graph/#live=127.0.0.1:7391&token=mgs_abc123DEF456&q=orphans",
		},
		{
			name: "trailing slashes on base are trimmed to exactly one",
			opts: GraphLinkOpts{
				ExplorerBase: "http://127.0.0.1:7391/console/graph///",
				Host:         loopHost,
				Token:        tok,
			},
			want: "http://127.0.0.1:7391/console/graph/#live=127.0.0.1:7391&token=mgs_abc123DEF456",
		},
		{
			name: "no daemon: empty host yields sentinel",
			opts: GraphLinkOpts{
				ExplorerBase: injectedBase,
				Host:         "",
				Token:        tok,
				Query:        "kind:target",
			},
			err: ErrNoDaemon,
		},
		{
			name: "empty token omits the token directive",
			opts: GraphLinkOpts{
				ExplorerBase: injectedBase,
				Host:         loopHost,
				Token:        "",
				View:         "blast",
			},
			want: "http://127.0.0.1:7391/console/graph/#live=127.0.0.1:7391&view=blast",
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

// TestEncodeComponent pins the encodeURIComponent-equivalent encoding: a space
// becomes %20 (never +), and a literal + is preserved as %2B, so the page's
// decodeURIComponent round-trips both.
func TestEncodeComponent(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"kind:target", "kind%3Atarget"},
		{"a b", "a%20b"},
		{"c++", "c%2B%2B"},
		{"x&y=z", "x%26y%3Dz"},
		{"plain", "plain"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			require.Equal(t, tt.want, encodeComponent(tt.in))
		})
	}
}
