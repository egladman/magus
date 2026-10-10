package main

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/log/audience"
	"github.com/egladman/magus/internal/trail"
	"github.com/stretchr/testify/assert"
)

func TestResolveAudience(t *testing.T) {
	const lease = "magus.lease=terse-output/audience"
	cases := []struct {
		name       string
		configured string
		env        map[string]string
		terminal   bool
		forced     bool
		want       audience.Audience
	}{
		{name: "configured human beats every signal", configured: "human", env: map[string]string{trail.EnvBaggage: lease}, forced: true, want: audience.Human},
		{name: "configured agent beats a terminal and CI", configured: "agent", env: map[string]string{"CI": "true"}, terminal: true, want: audience.Agent},
		{name: "forced beats a terminal", forced: true, terminal: true, want: audience.Agent},
		{name: "forced beats CI", forced: true, env: map[string]string{"CI": "true"}, want: audience.Agent},
		{name: "lease in baggage beats a terminal", env: map[string]string{trail.EnvBaggage: lease}, terminal: true, want: audience.Agent},
		{name: "lease among other baggage members", env: map[string]string{trail.EnvBaggage: "userId=alice, " + lease}, terminal: true, want: audience.Agent},
		{name: "baggage without a lease is no signal", env: map[string]string{trail.EnvBaggage: "userId=alice"}, terminal: true, want: audience.Human},
		{name: "terminal on stderr", terminal: true, want: audience.Human},
		{name: "CI without a terminal", env: map[string]string{"CI": "true"}, want: audience.Human},
		{name: "a pipe outside CI", want: audience.Agent},
		{name: "an unknown configured value falls through", configured: "robot", terminal: true, want: audience.Human},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := func(k string) string { return tc.env[k] }
			assert.Equal(t, tc.want, resolveAudience(tc.configured, env, tc.terminal, tc.forced))
		})
	}
}

// The guard learns a host called it only from its own flags, after the display is
// installed, so forcing filters the live default; a configured human still wins.
func TestForceAgentAudienceFiltersTheInstalledDisplay(t *testing.T) {
	savedCfg, savedGlobal, savedLogger, savedForced := globalCfg, global, slog.Default(), audienceForced.Load()
	t.Cleanup(func() {
		globalCfg, global = savedCfg, savedGlobal
		slog.SetDefault(savedLogger)
		audienceForced.Store(savedForced)
	})

	for _, tc := range []struct {
		configured string
		wantWhy    bool
	}{
		{configured: "", wantWhy: false},
		{configured: "human", wantWhy: true},
	} {
		globalCfg, global = config.Config{Log: config.Log{Audience: tc.configured}}, globalFlags{}
		audienceForced.Store(false)
		var buf bytes.Buffer
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

		forceAgentAudience()
		slog.Warn("index stale", attr.Why("stale symbols mislead"))

		assert.True(t, audienceForced.Load())
		assert.Equal(t, tc.wantWhy, bytes.Contains(buf.Bytes(), []byte("why=")), "configured %q: %s", tc.configured, buf.String())
	}
}
