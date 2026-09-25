package broker

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// A claim released while the client is re-asserting it on a new broker must still be
// released there. The stand-in broker below calls Release after the re-assert arrives and
// before it answers, which is the window where the client knows neither the new id nor the
// new connection.
func TestReleaseDuringReassertReachesTheNewBroker(t *testing.T) {
	old := redialEvery
	redialEvery = 20 * time.Millisecond
	t.Cleanup(func() { redialEvery = old })

	addr := testAddr(t)
	stopFirst, _ := serve(t, addr, WithCapacity(1000, 4))
	c := dial(t, addr)
	v, err := c.Request(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 900})
	require.NoError(t, err)
	require.True(t, v.Granted)
	stopFirst()

	ln, err := Listen(t.Context(), addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	released := make(chan string, 1)
	go func() {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = nc.Close() }()
		r, w := newFrameReader(nc), &frameWriter{w: nc}
		f, err := r.read()
		if err != nil || f.Type != typeHello {
			return
		}
		_ = w.write(typeHelloReply, f.ID, helloReply{PID: os.Getpid(), Protocol: ProtocolVersion})
		f, err = r.read()
		if err != nil || f.Type != typeClaim {
			return
		}
		c.Release(context.Background(), v.ID)
		_ = w.write(typeClaimReply, f.ID, claimReply{Verdict: types.MachineVerdict{Granted: true, ID: "reasserted"}})
		f, err = r.read()
		if err != nil || f.Type != typeRelease {
			return
		}
		var req releaseRequest
		if decodeBody(f, &req) == nil {
			released <- req.ClaimID
		}
		_ = w.write(typeReleaseReply, f.ID, nil)
	}()

	select {
	case id := <-released:
		assert.Equal(t, "reasserted", id)
	case <-time.After(3 * time.Second):
		t.Fatal("the release made during the re-assert never reached the new broker")
	}
}
