package audience

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

func TestAudienceWrapHumanReturnsTheHandler(t *testing.T) {
	var buf bytes.Buffer
	h := textHandler(&buf)
	assert.Equal(t, h, Wrap(h, Human, false))
}

func TestAudienceWrapAgentPolicy(t *testing.T) {
	cases := []struct {
		name    string
		verbose bool
		log     func(*slog.Logger)
		want    string
	}{
		{
			name: "drops why on the record",
			log:  func(l *slog.Logger) { l.WarnContext(context.Background(), "index stale", attr.Why("stale symbols mislead"), "dir", "api") },
			want: "level=WARN msg=\"index stale\" dir=api\n",
		},
		{
			name:    "keeps why when verbose",
			verbose: true,
			log:     func(l *slog.Logger) { l.WarnContext(context.Background(), "index stale", attr.Why("stale symbols mislead")) },
			want:    "level=WARN msg=\"index stale\" why=\"stale symbols mislead\"\n",
		},
		{
			name: "drops why added through With",
			log:  func(l *slog.Logger) { l.With(attr.Why("stale symbols mislead"), "dir", "api").WarnContext(context.Background(), "index stale") },
			want: "level=WARN msg=\"index stale\" dir=api\n",
		},
		{
			name: "drops why inside a group",
			log:  func(l *slog.Logger) { l.WithGroup("g").WarnContext(context.Background(), "index stale", attr.Why("x"), "dir", "api") },
			want: "level=WARN msg=\"index stale\" g.dir=api\n",
		},
		{
			name: "drops a short wait",
			log:  func(l *slog.Logger) { l.InfoContext(context.Background(), "waiting for the broker", attr.Elapsed(59*time.Second)) },
			want: "",
		},
		{
			name:    "drops a short wait even when verbose",
			verbose: true,
			log:     func(l *slog.Logger) { l.InfoContext(context.Background(), "waiting for the broker", attr.Elapsed(time.Second)) },
			want:    "",
		},
		{
			name: "keeps a wait of a minute",
			log:  func(l *slog.Logger) { l.InfoContext(context.Background(), "waiting for the broker", attr.Elapsed(time.Minute)) },
			want: "level=INFO msg=\"waiting for the broker\" elapsed=1m0s\n",
		},
		{
			name: "passes everything else",
			log:  func(l *slog.Logger) { l.ErrorContext(context.Background(), "build failed", attr.Component("cache"), "target", "go-build") },
			want: "level=ERROR msg=\"build failed\" component=cache target=go-build\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			tc.log(slog.New(Wrap(textHandler(&buf), Agent, tc.verbose)))
			assert.Equal(t, tc.want, buf.String())
		})
	}
}

func TestAudienceWrapAgentKeepsEnabled(t *testing.T) {
	var buf bytes.Buffer
	inner := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	h := Wrap(inner, Agent, false)
	ctx := context.Background()
	require.False(t, h.Enabled(ctx, slog.LevelInfo))
	require.True(t, h.Enabled(ctx, slog.LevelWarn))
	require.False(t, h.WithAttrs(nil).WithGroup("g").Enabled(ctx, slog.LevelInfo))
}
