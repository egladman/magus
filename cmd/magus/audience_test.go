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
		forced     bool
		want       audience.Audience
	}{
		{name: "configured human beats every signal", configured: "human", env: map[string]string{trail.EnvBaggage: lease}, forced: true, want: audience.Human},
		{name: "configured agent", configured: "agent", want: audience.Agent},
		{name: "forced", forced: true, want: audience.Agent},
		{name: "lease in baggage", env: map[string]string{trail.EnvBaggage: lease}, want: audience.Agent},
		{name: "lease among other baggage members", env: map[string]string{trail.EnvBaggage: "userId=alice, " + lease}, want: audience.Agent},
		{name: "baggage without a lease is no signal", env: map[string]string{trail.EnvBaggage: "userId=alice"}, want: audience.Human},
		{name: "nothing told means a person", want: audience.Human},
		{name: "a runner's variables are not read", env: map[string]string{"CI": "true", "GITHUB_ACTIONS": "true"}, want: audience.Human},
		{name: "an unknown configured value falls through", configured: "robot", want: audience.Human},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := func(k string) string { return tc.env[k] }
			assert.Equal(t, tc.want, resolveAudience(tc.configured, env, tc.forced))
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
