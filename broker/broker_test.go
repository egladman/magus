// cross-cutting: a real Client against a real Serve over a socket, across client.go and server.go

package broker

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/proc/endpoint"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// holderEnv makes the test binary a claim holder: it dials the broker at the address
// the variable names, takes one claim, prints "held", and blocks until killed. It is
// how a test reaches a real SIGKILL, which no in-process close can stand in for.
const holderEnv = "MAGUS_BROKER_TEST_HOLDER"

func TestMain(m *testing.M) {
	if addr := os.Getenv(holderEnv); addr != "" {
		os.Exit(holdUntilKilled(addr))
	}
	testkit.Main(m)
}

func holdUntilKilled(addr string) int {
	c, err := Dial(context.Background(), addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	v, err := c.Request(context.Background(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 900, Slots: 1})
	if err != nil || !v.Granted {
		fmt.Fprintln(os.Stderr, "claim not granted", err)
		return 2
	}
	fmt.Println("held")
	select {}
}

// testAddr is a broker socket in a short private directory: a t.TempDir() path can
// exceed the unix socket length limit on macOS.
func testAddr(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "brk")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return "unix://" + filepath.Join(dir, SocketName)
}

// serve runs a broker on addr until the test ends, and returns the channel Serve's
// result arrives on.
func serve(t *testing.T, addr string, opts ...Option) (stop func(), done <-chan error) {
	t.Helper()
	ln, err := Listen(t.Context(), addr)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		ch <- Serve(ctx, ln, opts...)
		close(finished)
	}()
	stop = func() {
		cancel()
		<-finished
	}
	t.Cleanup(stop)
	return stop, ch
}

func dial(t *testing.T, addr string) *Client {
	t.Helper()
	c, err := Dial(t.Context(), addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func holders(t *testing.T, addr string) []types.MachineClaimant {
	t.Helper()
	st, err := QueryStatus(t.Context(), addr)
	require.NoError(t, err)
	return st.Capacity.Holders
}

func TestClaimGrantReleaseAndStatus(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(1000, 4), WithVersion("v-test"))
	c := dial(t, addr)

	v, err := c.Request(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 900, Slots: 1})
	require.NoError(t, err)
	require.True(t, v.Granted)

	st, err := c.Status(t.Context())
	require.NoError(t, err)
	assert.Equal(t, os.Getpid(), st.PID)
	assert.Equal(t, ProtocolVersion, st.Protocol)
	assert.Equal(t, "v-test", st.Version)
	require.Len(t, st.Capacity.Holders, 1)
	h := st.Capacity.Holders[0]
	assert.Equal(t, os.Getpid(), h.PID, "a claim that names no pid is attributed to the connection")
	assert.NotEmpty(t, h.Command, "and to the command that opened it")

	other := dial(t, addr)
	refused, err := other.Request(t.Context(), types.MachineClaim{Project: "x", Target: "build", MemoryMB: 900, PID: 1})
	require.NoError(t, err)
	assert.False(t, refused.Granted, "the host is full")
	assert.True(t, refused.Fits)

	c.Release(t.Context(), v.ID)
	assert.Empty(t, holders(t, addr), "a release frees the claim")
}

// TestClosingTheConnectionReleasesItsClaims is the whole reason claims ride a
// connection: a holder that never says release still gives everything back.
func TestClosingTheConnectionReleasesItsClaims(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(1000, 4))
	c := dial(t, addr)
	_, err := c.Request(t.Context(), types.MachineClaim{Project: ".", Target: "a", MemoryMB: 400})
	require.NoError(t, err)
	_, err = c.Request(t.Context(), types.MachineClaim{Project: ".", Target: "b", MemoryMB: 400})
	require.NoError(t, err)
	require.Len(t, holders(t, addr), 2)

	require.NoError(t, c.Close())
	assert.Eventually(t, func() bool { return len(holders(t, addr)) == 0 }, 2*time.Second, 10*time.Millisecond,
		"every claim the connection carried is released when it closes")
}

// TestSIGKILLReleasesAtOnce kills a real holder process outright. It cannot run a
// release, so only the kernel closing its socket can free what it held; the old
// pid-polling budget held such a claim until some later request happened to reap it.
func TestSIGKILLReleasesAtOnce(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr, WithCapacity(1000, 4))

	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), holderEnv+"="+addr)
	out, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	line, err := bufio.NewReader(out).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "held\n", line)
	require.Len(t, holders(t, addr), 1)
	assert.Equal(t, cmd.Process.Pid, holders(t, addr)[0].PID)

	require.NoError(t, cmd.Process.Kill())
	_ = cmd.Wait()
	assert.Eventually(t, func() bool { return len(holders(t, addr)) == 0 }, 2*time.Second, 10*time.Millisecond,
		"a SIGKILLed holder's claim is released as soon as its socket closes")
}

// TestHoldersReassertOnANewBroker restarts the broker under a running holder. The step
// is still running, so its claim must reach the successor, or the new broker would
// admit work against memory the step is using.
func TestHoldersReassertOnANewBroker(t *testing.T) {
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
	serve(t, addr, WithCapacity(1000, 4))

	assert.Eventually(t, func() bool {
		st, err := QueryStatus(t.Context(), addr)
		return err == nil && len(st.Capacity.Holders) == 1
	}, 3*time.Second, 20*time.Millisecond, "the holder re-asserts its claim on the new broker without being asked")

	stranger := dial(t, addr)
	refused, err := stranger.Request(t.Context(), types.MachineClaim{Project: "x", Target: "b", MemoryMB: 900, PID: 1})
	require.NoError(t, err)
	assert.False(t, refused.Granted, "the new broker counts the re-asserted claim")

	c.Release(t.Context(), v.ID)
	assert.Eventually(t, func() bool { return len(holders(t, addr)) == 0 }, 2*time.Second, 10*time.Millisecond,
		"the id the holder kept still releases the claim on the new broker")
}

// replyGate forwards connections from its own socket to a broker, and on every
// connection after the first holds back claim replies until open closes. A reassert
// the gate is holding is recorded on the broker while the client has not yet read it.
type replyGate struct {
	addr     string
	recorded chan struct{}
	open     chan struct{}
}

func newReplyGate(t *testing.T, upstream string) *replyGate {
	t.Helper()
	g := &replyGate{addr: testAddr(t), recorded: make(chan struct{}), open: make(chan struct{})}
	ln, err := Listen(t.Context(), g.addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	up, err := endpoint.Parse(upstream)
	require.NoError(t, err)
	var once sync.Once
	go func() {
		for n := 0; ; n++ {
			down, err := ln.Accept()
			if err != nil {
				return
			}
			conn, err := up.Dial(context.Background())
			if err != nil {
				_ = down.Close()
				continue
			}
			hold := n > 0
			go func() {
				_, _ = io.Copy(conn, down)
				_ = conn.Close()
			}()
			go func() {
				defer func() { _ = down.Close(); _ = conn.Close() }()
				r, w := newFrameReader(conn), &frameWriter{w: down}
				for {
					f, err := r.read()
					if err != nil {
						return
					}
					if hold && f.Type == typeClaimReply {
						once.Do(func() { close(g.recorded) })
						<-g.open
					}
					if w.write(f.Type, f.ID, f.Body) != nil {
						return
					}
				}
			}()
		}
	}()
	return g
}

// TestAReleaseDuringReassertReachesTheNewBroker releases a claim after the new broker
// recorded its reassert but before the client read the reply. The client has no
// connection to send that release on, yet the successor must not keep the claim until
// the process exits.
func TestAReleaseDuringReassertReachesTheNewBroker(t *testing.T) {
	old := redialEvery
	redialEvery = 20 * time.Millisecond
	t.Cleanup(func() { redialEvery = old })

	addr := testAddr(t)
	stopFirst, _ := serve(t, addr, WithCapacity(1000, 4))
	gate := newReplyGate(t, addr)
	c := dial(t, gate.addr)
	v, err := c.Request(t.Context(), types.MachineClaim{Project: ".", Target: "test", MemoryMB: 900})
	require.NoError(t, err)
	require.True(t, v.Granted)

	stopFirst()
	serve(t, addr, WithCapacity(1000, 4))
	select {
	case <-gate.recorded:
	case <-time.After(3 * time.Second):
		t.Fatal("the holder never re-asserted on the new broker")
	}
	require.Len(t, holders(t, addr), 1)

	c.Release(t.Context(), v.ID)
	close(gate.open)
	assert.Eventually(t, func() bool { return len(holders(t, addr)) == 0 }, 2*time.Second, 10*time.Millisecond,
		"a release that lands mid-reassert still frees the claim on the new broker")
}

// TestIdleExitWaitsForWhatIsHeld pins the one lifetime rule: the broker exits once it
// has held nothing for its window, and a held claim keeps it up.
func TestIdleExitWaitsForWhatIsHeld(t *testing.T) {
	addr := testAddr(t)
	_, done := serve(t, addr, WithIdleExit(150*time.Millisecond))
	c := dial(t, addr)
	v, err := c.Request(t.Context(), types.MachineClaim{Project: ".", Target: "test"})
	require.NoError(t, err)

	select {
	case err := <-done:
		t.Fatalf("the broker exited (%v) while a claim was held", err)
	case <-time.After(500 * time.Millisecond):
	}

	c.Release(t.Context(), v.ID)
	select {
	case err := <-done:
		assert.NoError(t, err, "idle exit is a clean exit")
	case <-time.After(3 * time.Second):
		t.Fatal("the broker did not exit after it held nothing")
	}
	assert.False(t, Live(t.Context(), addr), "and it unbinds its socket")
}

// TestAConnectionWithoutStateDoesNotPinTheBroker is the server's case: a client that
// holds a connection open but no claim and no service must not keep the broker awake.
func TestAConnectionWithoutStateDoesNotPinTheBroker(t *testing.T) {
	addr := testAddr(t)
	_, done := serve(t, addr, WithIdleExit(150*time.Millisecond))
	_ = dial(t, addr)

	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("an idle connection kept the broker up")
	}
}

type fakeHost struct {
	mu       sync.Mutex
	refs     map[string]int
	stopped  bool
	startErr error
}

func (h *fakeHost) Acquire(_ context.Context, key string, _ spells.Service) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.startErr != nil {
		return h.startErr
	}
	if h.refs == nil {
		h.refs = map[string]int{}
	}
	h.refs[key]++
	return nil
}

func (h *fakeHost) Release(key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.refs[key]--
}

func (h *fakeHost) StopAll() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopped = true
	return len(h.refs)
}

func (h *fakeHost) Snapshot() []types.StatusService {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []types.StatusService
	for k, n := range h.refs {
		out = append(out, types.StatusService{ID: k, Dependents: n})
	}
	return out
}

func (h *fakeHost) count(key string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.refs[key]
}

// TestServiceReferencesRideTheConnection fixes the leak the one-shot service RPC had: a
// run killed mid-build never released its reference, so the service never idled out.
func TestServiceReferencesRideTheConnection(t *testing.T) {
	addr := testAddr(t)
	host := &fakeHost{}
	serve(t, addr, WithServices(host))
	c := dial(t, addr)
	svc := spells.Service{Command: spells.Command{Bin: "postgres"}}
	require.NoError(t, c.AcquireService(t.Context(), "pg", svc))
	require.NoError(t, c.AcquireService(t.Context(), "pg", svc))
	require.NoError(t, c.ReleaseService(t.Context(), "pg"))
	assert.Equal(t, 1, host.count("pg"))

	st, err := c.Status(t.Context())
	require.NoError(t, err)
	require.Len(t, st.Services, 1)

	require.NoError(t, c.Close())
	assert.Eventually(t, func() bool { return host.count("pg") == 0 }, 2*time.Second, 10*time.Millisecond,
		"a closed connection drops every reference it still held")

	stopper := dial(t, addr)
	n, err := stopper.StopServices(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.True(t, host.stopped)
}

func TestServiceErrorsCarryTheirCode(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr)
	c := dial(t, addr)
	err := c.AcquireService(t.Context(), "pg", spells.Service{})
	var be *Error
	require.ErrorAs(t, err, &be)
	assert.Equal(t, CodeNoServices, be.Code, "a broker hosting nothing says so by code, and the run hosts the service itself")

	failing := testAddr(t)
	serve(t, failing, WithServices(&fakeHost{startErr: errors.New("readiness failed")}))
	err = dial(t, failing).AcquireService(t.Context(), "pg", spells.Service{})
	require.ErrorAs(t, err, &be)
	assert.Equal(t, CodeService, be.Code)
	assert.Contains(t, be.Message, "readiness failed")
}

func TestNoBrokerIsUnavailable(t *testing.T) {
	addr := testAddr(t)
	_, err := Dial(t.Context(), addr)
	require.ErrorIs(t, err, ErrUnavailable)

	c := NewClient(addr)
	_, err = c.Request(t.Context(), types.MachineClaim{Project: ".", Target: "test"})
	require.ErrorIs(t, err, ErrUnavailable, "a lazy client reports the same when its first request finds nothing")
}

// TestAHelloFromAnotherProtocolIsRefused pins the guard every later frame relies on.
func TestAHelloFromAnotherProtocolIsRefused(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr)
	conn, err := net.Dial("unix", addr[len("unix://"):])
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	w := &frameWriter{w: conn}
	require.NoError(t, w.write(typeHello, 1, hello{Magic: helloMagic, Protocol: ProtocolVersion + 1}))
	f, err := newFrameReader(conn).read()
	require.NoError(t, err)
	assert.Equal(t, typeError, f.Type)
	var er errorReply
	require.NoError(t, decodeBody(f, &er))
	assert.Equal(t, CodeProtocol, er.Code)
}

func TestListenLosesToALiveBroker(t *testing.T) {
	addr := testAddr(t)
	serve(t, addr)
	_, err := Listen(t.Context(), addr)
	assert.ErrorIs(t, err, ErrRunning, "bind is the lock: a second broker exits instead of serving")
}

func TestListenReclaimsADeadSocket(t *testing.T) {
	addr := testAddr(t)
	path := addr[len("unix://"):]
	ln, err := net.Listen("unix", path)
	require.NoError(t, err)
	// Leave the file behind the way a killed broker does.
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	require.NoError(t, ln.Close())
	require.FileExists(t, path)

	ln2, err := Listen(t.Context(), addr)
	require.NoError(t, err)
	_ = ln2.Close()
}

func TestShutdownStopsTheBroker(t *testing.T) {
	addr := testAddr(t)
	_, done := serve(t, addr)
	require.NoError(t, dial(t, addr).Shutdown(t.Context()))
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not stop the broker")
	}
}

// awaitDrain waits until the broker reports it is draining, the point after which every
// new claim meets the refusal.
func awaitDrain(t *testing.T, addr string) {
	t.Helper()
	require.Eventually(t, func() bool {
		st, err := QueryStatus(t.Context(), addr)
		return err == nil && st.Draining
	}, 5*time.Second, 5*time.Millisecond, "the broker never reported draining")
}

func awaitServe(t *testing.T, done <-chan error, why string) {
	t.Helper()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal(why)
	}
}

// TestDrainSeatsNothingNewAndStopsWhenTheLastHolderLeaves is the first SIGTERM's
// contract: runs already holding the broker finish, new ones are told why they were
// turned away, and the broker goes as soon as nothing holds it.
func TestDrainSeatsNothingNewAndStopsWhenTheLastHolderLeaves(t *testing.T) {
	addr := testAddr(t)
	drain := make(chan struct{})
	host := &fakeHost{}
	_, done := serve(t, addr, WithCapacity(4000, 4), WithServices(host), WithDrain(drain, time.Hour))
	holder := dial(t, addr)
	v, err := holder.Request(t.Context(), types.MachineClaim{Project: ".", Target: "test", Slots: 1})
	require.NoError(t, err)
	require.True(t, v.Granted)

	close(drain)
	awaitDrain(t, addr)

	late := dial(t, addr)
	_, err = late.Request(t.Context(), types.MachineClaim{Project: "api", Target: "build", Slots: 1})
	var be *Error
	require.ErrorAs(t, err, &be)
	assert.Equal(t, CodeDraining, be.Code)
	assert.Contains(t, be.Message, fmt.Sprintf("the broker (pid %d) is shutting down", os.Getpid()),
		"the refusal names the process that turned the run away")

	err = late.AcquireService(t.Context(), "pg", spells.Service{})
	require.ErrorAs(t, err, &be)
	assert.Equal(t, CodeDraining, be.Code, "a new service reference is a new hold, and is refused the same way")

	assert.Len(t, holders(t, addr), 1, "the holder keeps its claim through the drain")
	holder.Release(t.Context(), v.ID)
	awaitServe(t, done, "the broker did not stop once its last holder released")
}

func TestDrainGivesUpAfterItsGrace(t *testing.T) {
	addr := testAddr(t)
	drain := make(chan struct{})
	_, done := serve(t, addr, WithCapacity(4000, 4), WithDrain(drain, 10*time.Millisecond))
	v, err := dial(t, addr).Request(t.Context(), types.MachineClaim{Project: ".", Target: "test", Slots: 1})
	require.NoError(t, err)
	require.True(t, v.Granted)

	close(drain)
	awaitServe(t, done, "a drain whose grace passed kept waiting for a holder")
}

func TestDrainWithNothingHeldStopsAtOnce(t *testing.T) {
	addr := testAddr(t)
	drain := make(chan struct{})
	_, done := serve(t, addr, WithDrain(drain, time.Hour))
	dial(t, addr) // a connection holding nothing does not keep a drain waiting
	close(drain)
	awaitServe(t, done, "a broker holding nothing waited out its drain grace")
}

// TestDrainStillRecordsAReassertion: a claim re-asserted during a drain belongs to a step
// already running, so refusing it would only hide that step from status.
func TestDrainStillRecordsAReassertion(t *testing.T) {
	addr := testAddr(t)
	drain := make(chan struct{})
	serve(t, addr, WithCapacity(4000, 4), WithDrain(drain, time.Hour))
	holder := dial(t, addr)
	_, err := holder.Request(t.Context(), types.MachineClaim{Project: ".", Target: "test", Slots: 1})
	require.NoError(t, err)
	close(drain)
	awaitDrain(t, addr)

	rejoined := dial(t, addr)
	cn, err := rejoined.connect(t.Context())
	require.NoError(t, err)
	var reply claimReply
	require.NoError(t, cn.roundTrip(t.Context(), rejoined.next(), typeClaim,
		claimRequest{Claim: types.MachineClaim{Project: "api", Target: "build", Slots: 1}, Reassert: true},
		typeClaimReply, &reply, nil))
	assert.True(t, reply.Verdict.Granted)
	assert.Len(t, holders(t, addr), 2)
}
