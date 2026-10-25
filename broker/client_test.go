package broker

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// acceptStandIn accepts one connection for a stand-in broker after doing what Serve does
// first: it writes the handshake file, then sends the handshake frame.
func acceptStandIn(ln net.Listener) (net.Conn, error) {
	if err := writeHandshake(ln.Addr().String(), "stand-in"); err != nil {
		return nil, err
	}
	nc, err := ln.Accept()
	if err != nil {
		return nil, err
	}
	if err := (&frameWriter{w: nc}).write(typeHandshake, 0, nil); err != nil {
		_ = nc.Close()
		return nil, err
	}
	return nc, nil
}

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
		nc, err := acceptStandIn(ln)
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

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// standIn serves one connection: it grants a claim as "remote-<n>" and answers a hold
// with an id, or with an error frame when refuse is set.
func standIn(ln net.Listener, n string, refuse bool, drop <-chan struct{}) {
	nc, err := acceptStandIn(ln)
	if err != nil {
		return
	}
	defer func() { _ = nc.Close() }()
	if drop != nil {
		go func() {
			<-drop
			_ = nc.Close()
		}()
	}
	r, w := newFrameReader(nc), &frameWriter{w: nc}
	for {
		f, err := r.read()
		if err != nil {
			return
		}
		switch f.Type {
		case typeHello:
			_ = w.write(typeHelloReply, f.ID, helloReply{PID: os.Getpid(), Protocol: ProtocolVersion})
		case typeClaim:
			_ = w.write(typeClaimReply, f.ID, claimReply{Verdict: types.MachineVerdict{Granted: true, Fits: true, ID: "remote-" + n}})
		case typeHold:
			if refuse {
				_ = w.write(typeError, f.ID, errorReply{Code: CodeUnknownType, Message: "no such claim"})
			} else {
				_ = w.write(typeHoldReply, f.ID, holdReply{ID: "hold-" + n})
			}
		default:
			_ = w.write(typeError, f.ID, errorReply{Code: CodeUnknownType, Message: "unknown"})
		}
	}
}

// Two groups ride a claim; the next broker refuses both. The refusal is said once, names
// the claim's project and target, and the first group.
func TestReassertWarnsOnceWhenTheNewBrokerRefusesGroupHolds(t *testing.T) {
	old := redialEvery
	redialEvery = 20 * time.Millisecond
	t.Cleanup(func() { redialEvery = old })
	logged := &lockedBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logged, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	addr := testAddr(t)
	ln, err := Listen(t.Context(), addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	drop := make(chan struct{})
	go standIn(ln, "1", false, drop)
	c := dial(t, addr)
	t.Cleanup(func() { _ = c.Close() })
	v, err := c.Request(t.Context(), types.MachineClaim{Project: "libs/xdg", Target: "go-test", MemoryMB: 100})
	require.NoError(t, err)
	_, err = c.HoldGroup(t.Context(), v.ID, 4242, "sleep 30")
	require.NoError(t, err)
	_, err = c.HoldGroup(t.Context(), v.ID, 4243, "sleep 31")
	require.NoError(t, err)

	go standIn(ln, "2", true, nil)
	close(drop)

	require.Eventually(t, func() bool { return strings.Contains(logged.String(), "child processes of") }, 5*time.Second, 20*time.Millisecond)
	assert.Contains(t, logged.String(), `msg="child processes of libs/xdg:go-test are not tied to its build claim, so they keep running if magus exits abnormally: the broker refused the hold (no such claim). First child: pid `)
	assert.Equal(t, 1, strings.Count(logged.String(), "child processes of"), "two refused groups are said once")
}

// An overrun reaches the broker by the id that broker granted, and a broker that
// predates the frame is said once, not failed on.
func TestOverrunRoundTripAndAnOlderBroker(t *testing.T) {
	var logged bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	addr := testAddr(t)
	ln, err := Listen(t.Context(), addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	overruns := make(chan string, 4)
	go func() {
		nc, err := acceptStandIn(ln)
		if err != nil {
			return
		}
		defer func() { _ = nc.Close() }()
		r, w := newFrameReader(nc), &frameWriter{w: nc}
		seen := 0
		for {
			f, err := r.read()
			if err != nil {
				return
			}
			switch f.Type {
			case typeHello:
				_ = w.write(typeHelloReply, f.ID, helloReply{PID: os.Getpid(), Protocol: ProtocolVersion})
			case typeClaim:
				_ = w.write(typeClaimReply, f.ID, claimReply{Verdict: types.MachineVerdict{Granted: true, Fits: true, ID: "remote-1"}})
			case typeOverrun:
				var req overrunRequest
				if decodeBody(f, &req) == nil {
					overruns <- req.ClaimID
				}
				// The first is answered as this build's broker, the rest as an older one.
				seen++
				if seen == 1 {
					_ = w.write(typeOverrunReply, f.ID, nil)
				} else {
					_ = w.write(typeError, f.ID, errorReply{Code: CodeUnknownType, Message: `broker: unknown frame type "overrun"`})
				}
			default:
				_ = w.write(typeError, f.ID, errorReply{Code: CodeUnknownType, Message: "unknown"})
			}
		}
	}()

	c := dial(t, addr)
	v, err := c.Request(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 900})
	require.NoError(t, err)
	require.True(t, v.Granted)

	c.Overrun(t.Context(), v.ID)
	assert.Equal(t, "remote-1", <-overruns)
	assert.Empty(t, logged.String(), "a broker that knows the frame is not remarked on")

	c.Overrun(t.Context(), v.ID)
	c.Overrun(t.Context(), v.ID)
	assert.Equal(t, "remote-1", <-overruns)
	assert.Equal(t, 1, strings.Count(logged.String(), "the broker predates memory overruns"), "an older broker is said once")

	c.Overrun(t.Context(), "never-granted")
	assert.Len(t, overruns, 1, "an id this client does not hold is never sent")
}
