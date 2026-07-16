package proc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/egladman/magus/internal/proc/endpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestForwardVersionGate drives the adoption gate end-to-end through Forward: the server is
// built with one display version, the client forwards under another, and the outcome is
// asserted from both the call (adopted -> exit 0, no error; refused -> ErrVersionMismatch,
// classified NotAdopted) and whether the handler ran. Both ends run adoptionIdentity over
// the SAME test binary, so the two "unknown" cases fingerprint identically and adopt; the
// release cases exercise the exact-match and mismatch halves.
func TestForwardVersionGate(t *testing.T) {
	cases := []struct {
		name          string
		serverVersion string
		clientVersion string
		wantAdopt     bool
	}{
		{"same release adopts", "v1.0.0", "v1.0.0", true},
		{"different release refuses", "v1.0.0", "v2.0.0", false},
		{"release daemon refuses dev client", "v1.0.0", devVersionSentinel, false},
		{"empty server version disables the gate", "", devVersionSentinel, true},
		{"same dev build adopts (identical fingerprint)", devVersionSentinel, devVersionSentinel, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var called atomic.Bool
			srv, err := New(Options{
				Version: tc.serverVersion,
				Handler: func(context.Context, []string) error { called.Store(true); return nil },
			})
			require.NoError(t, err)
			defer srv.Close()
			require.NoError(t, srv.Start())
			t.Setenv("MAGUS_DAEMON_SOCKET", srv.Addr())

			code, err := Forward(context.Background(), []string{"run", "build", "x"}, tc.clientVersion, "")
			if tc.wantAdopt {
				require.NoError(t, err)
				assert.Equal(t, 0, code)
				assert.True(t, called.Load(), "an adopted call runs the handler")
				return
			}
			require.Error(t, err, "a version mismatch surfaces as an error")
			assert.True(t, errors.Is(err, ErrVersionMismatch), "got %v", err)
			assert.True(t, NotAdopted(err), "a version mismatch is classified not-adopted so the client falls back silently")
			assert.False(t, called.Load(), "a refused call must not run the handler")
		})
	}
}

// TestForwardDevDifferentFingerprintRefused proves the core fix at the wire level: a dev
// daemon (identity fingerprinted from its build) refuses a forwarded run whose version is a
// DIFFERENT dev fingerprint. Two distinct dev builds can't coexist in one test process, so
// the mismatching client frame is hand-crafted with a fabricated "dev-*" identity that
// cannot equal this binary's own. This is the stale-daemon incident in miniature.
func TestForwardDevDifferentFingerprintRefused(t *testing.T) {
	var called atomic.Bool
	srv, err := New(Options{
		Version: devVersionSentinel, // gateVersion = this binary's real dev fingerprint
		Handler: func(context.Context, []string) error { called.Store(true); return nil },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())

	ep, err := endpoint.ParseEndpoint(srv.Addr())
	require.NoError(t, err)
	conn, err := ep.Dial(context.Background())
	require.NoError(t, err)
	defer conn.Close()

	// A dev identity that provably differs from any real fingerprint of this binary.
	frame := `{"type":"run","args":["run","build","x"],"version":"dev-0000000000000000deadbeef","cwd":"/tmp","protocol":"v2"}` + "\n"
	_, err = conn.Write([]byte(frame))
	require.NoError(t, err)

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	require.NoError(t, err)

	var envelope struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(bytes.TrimRight(buf[:n], "\n"), &envelope))
	assert.Equal(t, "error", envelope.Type)
	assert.Equal(t, ErrVersionMismatch.Error(), envelope.Message, "a mismatched dev fingerprint is refused as a version mismatch")
	assert.False(t, called.Load(), "the refused run must not execute")
}

// TestSubmitJobVersionGate confirms the job gate uses the same identity as run: a background
// job carrying a mismatched version is refused, while a matching (or empty) one is accepted.
// The client SubmitJob sends adoptionIdentity(version), so an empty version disables the gate
// and a differing release version trips it.
func TestSubmitJobVersionGate(t *testing.T) {
	t.Run("mismatched version refused", func(t *testing.T) {
		var reply JobReply
		s := &service{gateVersion: "v1.0.0", parentCtx: context.Background()}
		err := s.submitJob(JobRequest{Magic: JobMagic, Version: "v2.0.0", Args: []string{"graph", "build"}}, &reply)
		assert.ErrorIs(t, err, ErrVersionMismatch)
		assert.Empty(t, reply.Inv, "a refused job returns no invocation id")
	})
}
