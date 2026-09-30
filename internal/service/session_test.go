package service

import (
	"context"
	"testing"
	"time"

	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionAcquireReportsOwnership(t *testing.T) {
	tests := []struct {
		name         string
		runner       *fakeRunner
		broker       func(context.Context, string, spells.Service) (bool, error)
		wantOwned    bool
		wantBrokered bool
	}{
		{"in-process start", &fakeRunner{}, nil, true, false},
		{"in-process adopt", &fakeRunner{adopt: true}, nil, false, false},
		{"broker started it", &fakeRunner{}, func(context.Context, string, spells.Service) (bool, error) { return true, nil }, true, true},
		{"broker adopted it", &fakeRunner{}, func(context.Context, string, spells.Service) (bool, error) { return false, nil }, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := NewSession(New(tt.runner, 0), tt.broker, func(context.Context, string) {})
			owned, brokered, err := sess.Acquire(context.Background(), "machine", svc())
			require.NoError(t, err)
			assert.Equal(t, tt.wantOwned, owned)
			assert.Equal(t, tt.wantBrokered, brokered)
		})
	}
}

// TestSessionReleaseDropsOneReference pins an early release: the broker hears one
// release for a brokered key, and the session no longer holds it at ReleaseAll.
func TestSessionReleaseDropsOneReference(t *testing.T) {
	var released []string
	sess := NewSession(New(&fakeRunner{}, time.Hour),
		func(context.Context, string, spells.Service) (bool, error) { return true, nil },
		func(_ context.Context, key string) { released = append(released, key) },
	)
	ctx := context.Background()
	_, _, err := sess.Acquire(ctx, "machine", svc())
	require.NoError(t, err)

	sess.Release(ctx, "machine")
	sess.ReleaseAll(ctx)
	assert.Equal(t, []string{"machine"}, released, "released once, not again at ReleaseAll")
}

// TestScopeReleasesWhatAScriptHeld pins the end of a script: whatever it acquired and
// did not release is released by the scope, and an in-process service it owns stops.
func TestScopeReleasesWhatAScriptHeld(t *testing.T) {
	f := &fakeRunner{}
	ctx, sc := WithScope(context.Background())
	require.Same(t, sc, ScopeFrom(ctx))

	opened := 0
	open := func() *Session {
		opened++
		return NewSession(New(f, 0), nil, nil)
	}
	for range 2 {
		_, _, err := ScopeFrom(ctx).Session(open).Acquire(ctx, "machine", svc())
		require.NoError(t, err)
	}
	assert.Equal(t, 1, opened, "one session per script")

	sc.ReleaseAll(context.Background())
	started, stopped := f.counts()
	assert.Equal(t, 1, started)
	assert.Equal(t, 1, stopped, "the script's end stops what it started")
}

// TestScopeWithNothingAcquiredOpensNothing pins that a script taking no lease never
// opens a session, so it never dials a broker.
func TestScopeWithNothingAcquiredOpensNothing(t *testing.T) {
	_, sc := WithScope(context.Background())
	sc.ReleaseAll(context.Background())
	assert.Nil(t, ScopeFrom(context.Background()))
	assert.Nil(t, sc.sess)
}
