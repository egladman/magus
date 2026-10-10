package quiet

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/egladman/magus/internal/log/attr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func textHandler(buf *bytes.Buffer) slog.Handler {
	return slog.NewTextHandler(buf, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
}

func TestWrapPolicy(t *testing.T) {
	cases := []struct {
		name    string
		verbose bool
		log     func(*slog.Logger)
		want    string
	}{
		{
			name: "drops why on the record",
			log: func(l *slog.Logger) {
				l.WarnContext(context.Background(), "index stale", attr.Why("stale symbols mislead"), slog.String("dir", "api"))
			},
			want: "level=WARN msg=\"index stale\" dir=api\n",
		},
		{
			name:    "keeps why when verbose",
			verbose: true,
			log: func(l *slog.Logger) {
				l.WarnContext(context.Background(), "index stale", attr.Why("stale symbols mislead"))
			},
			want: "level=WARN msg=\"index stale\" why=\"stale symbols mislead\"\n",
		},
		{
			name: "drops why added through With",
			log: func(l *slog.Logger) {
				l.With(attr.Why("stale symbols mislead"), slog.String("dir", "api")).WarnContext(context.Background(), "index stale")
			},
			want: "level=WARN msg=\"index stale\" dir=api\n",
		},
		{
			name: "drops why inside a group",
			log: func(l *slog.Logger) {
				l.WithGroup("g").WarnContext(context.Background(), "index stale", attr.Why("x"), slog.String("dir", "api"))
			},
			want: "level=WARN msg=\"index stale\" g.dir=api\n",
		},
		{
			name: "drops a short wait",
			log: func(l *slog.Logger) {
				l.InfoContext(context.Background(), "waiting for the broker", attr.Elapsed(59*time.Second))
			},
			want: "",
		},
		{
			name:    "drops a short wait even when verbose",
			verbose: true,
			log: func(l *slog.Logger) {
				l.InfoContext(context.Background(), "waiting for the broker", attr.Elapsed(time.Second))
			},
			want: "",
		},
		{
			name: "keeps a wait of a minute",
			log: func(l *slog.Logger) {
				l.InfoContext(context.Background(), "waiting for the broker", attr.Elapsed(time.Minute))
			},
			want: "level=INFO msg=\"waiting for the broker\" elapsed=1m0s\n",
		},
		{
			name: "passes everything else",
			log: func(l *slog.Logger) {
				l.ErrorContext(context.Background(), "build failed", attr.Component("cache"), slog.String("target", "go-build"))
			},
			want: "level=ERROR msg=\"build failed\" component=cache target=go-build\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			tc.log(slog.New(Wrap(textHandler(&buf), tc.verbose)))
			assert.Equal(t, tc.want, buf.String())
		})
	}
}

// log.silent quiets the display without raising its level, so an info note is dropped by
// quiet itself, not by the level.
func TestWrapDropsInfoAtAnInfoLevel(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(Wrap(textHandler(&buf), false))
	l.InfoContext(context.Background(), "using this workspace's own binary", attr.Notice(""))
	l.InfoContext(context.Background(), "http://127.0.0.1:7700/jobs", attr.Notice("console"), attr.Next("magus console open"))
	assert.Equal(t, "level=INFO msg=\"magus console open\" notice=console\n", buf.String())
}

// Info passes Enabled so a hint or a next command can reach Handle; every other level
// stays the inner handler's call.
func TestWrapEnabledFloorsAtInfo(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})
	h := Wrap(inner, false)
	ctx := context.Background()
	require.False(t, h.Enabled(ctx, slog.LevelDebug))
	require.True(t, h.Enabled(ctx, slog.LevelInfo))
	require.False(t, h.Enabled(ctx, slog.LevelWarn))
	require.True(t, h.WithAttrs(nil).WithGroup("g").Enabled(ctx, slog.LevelInfo))
}

func TestWrapBelowLevel(t *testing.T) {
	cases := []struct {
		name string
		log  func(*slog.Logger)
		want string
	}{
		{
			name: "drops a plain record",
			log: func(l *slog.Logger) {
				l.InfoContext(context.Background(), "using this workspace's own binary", attr.Component("magus"))
			},
			want: "",
		},
		{
			name: "keeps a hint without its why",
			log: func(l *slog.Logger) {
				l.InfoContext(context.Background(), "pass --force to replace it", attr.Hint(), attr.Why("x"))
			},
			want: "level=INFO msg=\"pass --force to replace it\" notice=hint\n",
		},
		{
			name: "drops a warning's next with the warning",
			log: func(l *slog.Logger) {
				l.WarnContext(context.Background(), "index stale", attr.Next("magus graph build"))
			},
			want: "",
		},
		{
			name: "keeps a next command alone",
			log: func(l *slog.Logger) {
				l.InfoContext(context.Background(), "http://127.0.0.1:7700/jobs", attr.Notice("console"),
					attr.Next("magus console open"), attr.Why("server is v0.4"), slog.String("dir", "api"))
			},
			want: "level=INFO msg=\"magus console open\" notice=console\n",
		},
		{
			name: "keeps an error and its next at the level",
			log: func(l *slog.Logger) {
				l.ErrorContext(context.Background(), "index stale", attr.Next("magus graph build"), attr.Why("x"))
			},
			want: "level=ERROR msg=\"index stale\" next=\"magus graph build\"\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{
				Level: slog.LevelError,
				ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
					if a.Key == slog.TimeKey {
						return slog.Attr{}
					}
					return a
				},
			})
			tc.log(slog.New(Wrap(inner, false)))
			assert.Equal(t, tc.want, buf.String())
		})
	}
}
