package console

import (
	"strings"
	"testing"

	"github.com/egladman/magus/internal/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLogViewerURL builds the log-viewer deep link: the ref identity AND the
// gzip+base64url Journal both ride the URL fragment (after #), which the browser never
// transmits, so nothing about the run, not even its ref, leaves the machine.
func TestLogViewerURL(t *testing.T) {
	const base = "https://eli.gladman.cc/magus/logs/"
	events := []journal.Event{
		{Ts: 1, Kind: journal.KindOutput, Stream: journal.StreamStdout, Text: "build failed: boom"},
		{Ts: 2, Kind: journal.KindResult, Status: journal.StatusFail, Ref: "outdeadbeef"},
	}
	url, err := LogViewerURL(base, "outdeadbeef", events,
		journal.Invocation{ID: "inv1", Command: journal.Command{Arguments: []string{"run", "build"}}}, "")
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(url, base+"#ref=outdeadbeef&data="),
		"url should carry the ref identity then the fragment data, got %q", url)
	// Everything rides after #, which browsers never send to the server: no query string.
	before, after, found := strings.Cut(url, "#")
	require.True(t, found, "url must have a fragment")
	assert.NotContains(t, before, "?", "nothing must ride the query string")
	assert.Contains(t, after, "ref=outdeadbeef", "ref must live in the fragment")
	assert.Contains(t, after, "data=", "data must live in the fragment")
}

// TestLink pins the daemon-origin grammar Link mints: the ORIGIN is the daemon's own
// loopback host, the surface is a /console/<surface>/ path, and only the bearer token rides
// the fragment (never the query string, so it is not transmitted on the document GET). There
// is no #live= host directive; the origin already names which daemon.
func TestLink(t *testing.T) {
	got := Link(LinkOpts{Host: "127.0.0.1:7391", Surface: "dashboard", Token: "mgs_abc123"})
	assert.Equal(t, "http://127.0.0.1:7391/console/dashboard/#token=mgs_abc123", got)
	assert.NotContains(t, got, "#live=", "the daemon-origin grammar carries no #live= host directive")

	before, after, found := strings.Cut(got, "#")
	require.True(t, found, "url must have a fragment")
	assert.NotContains(t, before, "?", "the surface rides the clean path, not a query string")
	assert.Contains(t, after, "token=mgs_abc123", "the token must live in the fragment")
}

// TestLinkFragmentThenToken pins the ordering the consolidated grammar promises: content
// directives lead, in the order given, and the token is emitted LAST. A tokenless link is a
// bare surface path with a directive-only fragment.
func TestLinkFragmentThenToken(t *testing.T) {
	got := Link(LinkOpts{
		Host:     "127.0.0.1:7391",
		Surface:  "graph",
		Token:    "mgs_abc123",
		Fragment: []FragmentParam{{Key: "flavor", Value: "targets"}},
	})
	assert.Equal(t, "http://127.0.0.1:7391/console/graph/#flavor=targets&token=mgs_abc123", got)

	noTok := Link(LinkOpts{Host: "127.0.0.1:7391", Surface: "graph"})
	assert.Equal(t, "http://127.0.0.1:7391/console/graph/", noTok, "no token and no directives yields a bare surface path")
}

// TestEncodeComponent pins the encodeURIComponent-equivalent policy shared by every producer
// of the console URL grammar: a space becomes %20 (never +), and a literal + is preserved as
// %2B, so the page's decodeURIComponent round-trips both.
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
			assert.Equal(t, tt.want, encodeComponent(tt.in))
		})
	}
}

// TestKnownSurfaces guards the canonical clean-path surface list the daemon SPA fallback and the
// minted links share: IsSurfaceRoute matches a bare surface segment and nothing else.
func TestKnownSurfaces(t *testing.T) {
	assert.True(t, IsSurfaceRoute("graph"))
	assert.True(t, IsSurfaceRoute("dashboard"))
	assert.True(t, IsSurfaceRoute("logs"))
	assert.True(t, IsSurfaceRoute("activity"))
	assert.False(t, IsSurfaceRoute("graph/explorer.js"), "a sub-path is a static file, not a surface route")
	assert.False(t, IsSurfaceRoute(""), "the console root is not a surface route")
	assert.False(t, IsSurfaceRoute("settings"), "settings is not a clean-path deep-link surface")
}

// TestLogViewerURLKeyDirective: the key directive rides the fragment ahead of the
// payload, carries only class digests (never key content), and is omitted entirely
// when the run recorded no key inputs, so an old ref's link keeps its pre-key shape.
func TestLogViewerURLKeyDirective(t *testing.T) {
	digests := KeyDigestsParam([]KeyClassDigest{
		{Class: "src", Digest: "aabbccddeeff"},
		{Class: "env", Digest: "112233445566"},
	})
	assert.Equal(t, "src:aabbccddeeff,env:112233445566", digests)

	withKey, err := LogViewerURL("https://example.test/logs/", "out1a2b3c4d5e6f", nil, journal.Invocation{}, digests)
	require.NoError(t, err)
	assert.Contains(t, withKey, "#ref=out1a2b3c4d5e6f&key=src%3Aaabbccddeeff%2Cenv%3A112233445566&data=")

	plain, err := LogViewerURL("https://example.test/logs/", "out1a2b3c4d5e6f", nil, journal.Invocation{}, "")
	require.NoError(t, err)
	assert.NotContains(t, plain, "&key=")

	empty, err := LogViewerURL("https://example.test/logs/", "out1a2b3c4d5e6f", nil, journal.Invocation{}, "")
	require.NoError(t, err)
	assert.Equal(t, plain, empty, "no digests must reproduce the plain link exactly")
}

// TestKeyDigestsParamEmpty: no digests renders empty, which is what suppresses the
// key directive entirely rather than emitting a bare "key=".
func TestKeyDigestsParamEmpty(t *testing.T) {
	assert.Empty(t, KeyDigestsParam(nil))
}

// A job's console link is the Jobs view with that job selected, and it is ABSOLUTE: a CLI
// hands it to somebody who is not in the console already, so a path alone resolves against
// nothing. With no daemon serving there is no origin, and the empty string is what lets the
// caller say how to start one rather than print a dead link.
func TestJobLink(t *testing.T) {
	assert.Equal(t, "/console/plan/#job=pwa%2Fjob-watch", JobSurfaceLink("pwa/job-watch"))
	assert.Equal(t, "http://127.0.0.1:7777/console/plan/#job=pwa%2Fjob-watch", JobLink("127.0.0.1:7777", "pwa/job-watch"))
	assert.Empty(t, JobLink("", "pwa/job-watch"), "no serving daemon has no origin, so there is no link to print")
	assert.Empty(t, JobLink("127.0.0.1:7777", ""), "a link to no job in particular is the surface link, not this")
	assert.Contains(t, KnownSurfaces, JobSurface, "the Jobs view has to be a surface the daemon serves the shell for")
}

func TestOpenCommandAs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		goos, link, want string
	}{
		{"darwin", "http://127.0.0.1:7391/console/plan/",
			`open "http://127.0.0.1:7391/console/plan/#token=$(magus config token print)"`},
		{"linux", "http://127.0.0.1:7391/console/plan/#job=a",
			`xdg-open "http://127.0.0.1:7391/console/plan/#job=a&token=$(magus config token print)"`},
		{"windows", "http://127.0.0.1:7391/console/",
			`Start-Process "http://127.0.0.1:7391/console/#token=$(magus config token print)"`},
	} {
		assert.Equal(t, tc.want, OpenCommandAs(tc.link, tc.goos, "magus"), tc.goos)
	}
}
