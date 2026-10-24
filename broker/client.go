package broker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/proc/endpoint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// ErrUnavailable is returned, wrapped, when no broker answers: nothing is listening,
// or the connection closed under a request. Under `broker: required` a step refuses
// with it; under best-effort the step runs unarbitrated.
var ErrUnavailable = errors.New("broker: unavailable")

// Error is an error frame a broker answered with. Code, not Message, is what to act on.
type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string { return e.Message }

// dialTimeout bounds one connect and hello. The broker loads nothing, so an answer
// slower than this is a wedged broker rather than a busy one.
const dialTimeout = 5 * time.Second

// redialEvery paces a holder looking for a new broker after its broker died, so it
// can re-assert the claims its running steps still hold. A var so a test can shorten
// it.
var redialEvery = time.Second

// ClientOption configures a Client.
type ClientOption func(*Client)

// WithIdentity sets the argv and build a client introduces itself with, which status
// shows beside each claim it holds. The default argv is os.Args.
func WithIdentity(argv []string, version string) ClientOption {
	return func(c *Client) { c.hello.Argv, c.hello.Version = argv, version }
}

// WithStart sets how a client brings up a broker when none answers a Request or an
// AcquireService: start runs once per such call and reports whether one should now
// answer, and the call dials again only then. Status, Release and Shutdown never start
// one. Without it, those calls fail with ErrUnavailable.
func WithStart(start func(context.Context) bool) ClientOption {
	return func(c *Client) { c.start = start }
}

// Client is one process's connection to a broker. It dials on first use and holds that
// one connection for its life, so every claim and service reference it takes is
// released when the process exits, however it exits. Safe for concurrent use; call
// Close to release everything before the process ends.
//
// When its broker dies, a Client holding claims redials on its own and re-asserts them,
// with the process groups riding them, on the next broker to answer.
type Client struct {
	addr      string
	hello     hello
	start     func(context.Context) bool
	crashHint string

	connectMu sync.Mutex

	mu        sync.Mutex
	cur       *conn
	held      map[string]*heldClaim
	riders    map[string]*heldRider
	closed    bool
	redialing bool
	done      chan struct{}

	nextID atomic.Uint64

	// scopeOnce resolves the hello's job tree and validation cap; see resolveScope.
	scopeOnce sync.Once
	scopeErr  error
	// seatWaiting is set while a claim waits on its job tree's validation seat, so the
	// wait is said once.
	seatWaiting atomic.Bool
	// overrunUnknown says once that the broker predates the overrun frame.
	overrunUnknown sync.Once
}

// heldClaim is a claim this client holds, and the id the current broker knows it by;
// remote is empty while no broker does. overrun is set once Overrun names the claim, so
// a broker the claim is re-asserted on charges its peak too.
type heldClaim struct {
	claim   types.MachineClaim
	remote  string
	overrun bool
}

// heldRider is a process group this client handed the broker to end with one of its
// claims, keyed in held by claim, and the hold id the current broker knows it by;
// remote is empty while no broker does.
type heldRider struct {
	claim  string
	hold   Hold
	remote string
}

// NewClient returns a client for the broker at addr (a unix:// URL or a bare path). It
// does not dial; the first request does.
func NewClient(addr string, opts ...ClientOption) *Client {
	dir, _ := os.Getwd()
	lease := trail.SpawnFromEnv().Lease
	c := &Client{
		addr:   addr,
		hello:  hello{Magic: helloMagic, Protocol: ProtocolVersion, PID: os.Getpid(), Dir: dir, Argv: os.Args, Lease: lease},
		held:   map[string]*heldClaim{},
		riders: map[string]*heldRider{},
		done:   make(chan struct{}),
	}
	for _, fn := range opts {
		fn(c)
	}
	return c
}

// Dial returns a client whose handshake with the broker at addr is done, or an error
// wrapping ErrUnavailable when none answers or, as a *HandshakeError, none admits it.
func Dial(ctx context.Context, addr string, opts ...ClientOption) (*Client, error) {
	c := NewClient(addr, opts...)
	if err := c.Handshake(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// Handshake connects to the broker unless the client already holds a live connection.
// Every request runs it first, so calling it is only for learning early whether the
// broker admits this process. It is safe to call more than once. A failure wraps
// ErrUnavailable; a broker that answered without admitting this client is also a
// *HandshakeError.
func (c *Client) Handshake(ctx context.Context) error {
	_, err := c.connect(ctx)
	return err
}

// HandshakeError is a broker that answered a connection without admitting it: it sent
// no handshake frame, its handshake file could not be read, or it refused the value
// read. A refusal's Err is an *Error with CodeHandshake.
type HandshakeError struct {
	// Path is the handshake file beside the broker socket.
	Path string
	Err  error
}

func (e *HandshakeError) Error() string {
	return fmt.Sprintf("handshake via %s: %s", e.Path, e.Err.Error())
}

func (e *HandshakeError) Unwrap() error { return e.Err }

// QueryStatus dials the broker at addr, reads its status and hangs up. The error wraps
// ErrUnavailable when none answers.
func QueryStatus(ctx context.Context, addr string) (types.StatusBroker, error) {
	c, err := Dial(ctx, addr)
	if err != nil {
		return types.StatusBroker{}, err
	}
	defer func() { _ = c.Close() }()
	return c.Status(ctx)
}

// Addr is the address this client dials.
func (c *Client) Addr() string { return c.addr }

// Request asks the broker to seat claim, once, and answers at once: a grant records the
// claim against this connection until Release or until the connection closes. A claim
// that does not fit is not recorded; the verdict says whether waiting could help.
//
// If ctx ends after the broker granted but before the answer arrived, the grant is
// handed straight back.
func (c *Client) Request(ctx context.Context, claim types.MachineClaim) (types.MachineVerdict, error) {
	cn, err := c.open(ctx)
	if err != nil {
		return types.MachineVerdict{}, err
	}
	var reply claimReply
	late := func(f frame) {
		var r claimReply
		if f.Type == typeClaimReply && decodeBody(f, &r) == nil && r.Verdict.Granted {
			_, _ = cn.call(context.Background(), c.next(), typeRelease, releaseRequest{ClaimID: r.Verdict.ID})
		}
	}
	if err := cn.roundTrip(ctx, c.next(), typeClaim, claimRequest{Claim: claim}, typeClaimReply, &reply, late); err != nil {
		return types.MachineVerdict{}, err
	}
	c.noteSeat(ctx, reply.Seat)
	if reply.Verdict.Granted {
		c.mu.Lock()
		c.held[reply.Verdict.ID] = &heldClaim{claim: claim, remote: reply.Verdict.ID}
		c.mu.Unlock()
	}
	return reply.Verdict, nil
}

// Release hands back a claim Request granted, by the id its verdict carried. An unknown
// id is ignored, and so is a broker that is gone: it released the claim when the
// connection closed.
func (c *Client) Release(ctx context.Context, id string) {
	c.mu.Lock()
	h, ok := c.held[id]
	delete(c.held, id)
	// The broker ends the groups riding the claim with it.
	for rider, r := range c.riders {
		if r.claim == id {
			delete(c.riders, rider)
		}
	}
	cn := c.cur
	c.mu.Unlock()
	if !ok || h.remote == "" || cn == nil {
		return
	}
	_, _ = cn.call(ctx, c.next(), typeRelease, releaseRequest{ClaimID: h.remote})
}

// Overrun tells the broker a claim Request granted held more than its memory curve
// predicted, so the broker charges it its full peak until Release. Best-effort like
// Release: an unknown id or a gone broker is ignored, and a broker too old to know the
// frame is logged once per Client, never an error the run fails on. A claim re-asserted
// on a later broker is marked overrun there as well.
func (c *Client) Overrun(ctx context.Context, id string) {
	c.mu.Lock()
	var remote string
	if h, ok := c.held[id]; ok {
		h.overrun = true
		remote = h.remote
	}
	cn := c.cur
	c.mu.Unlock()
	if remote == "" || cn == nil {
		return
	}
	c.sendOverrun(ctx, cn, remote)
}

func (c *Client) sendOverrun(ctx context.Context, cn *conn, remote string) {
	err := cn.roundTrip(ctx, c.next(), typeOverrun, overrunRequest{ClaimID: remote}, typeOverrunReply, nil, nil)
	if be := (*Error)(nil); errors.As(err, &be) && be.Code == CodeUnknownType {
		c.overrunUnknown.Do(func() {
			slog.WarnContext(ctx, "the broker predates memory overruns: a claim over its curve stays charged by the curve; restart the broker to pick up this build",
				attr.Component("broker"))
		})
	}
}

// Reserve asks the broker to hold a seat the size of claim for process reader, once, and
// answers at once. A grant does not ride this connection: the seat lasts until the
// reader's first claim takes it, the reader exits, or the reader claims nothing within
// the broker's bound, so it holds even after this client closes. A verdict that is not
// Granted reserved nothing. It never starts a broker.
func (c *Client) Reserve(ctx context.Context, claim types.MachineClaim, reader int) (types.MachineVerdict, error) {
	cn, err := c.connect(ctx)
	if err != nil {
		return types.MachineVerdict{}, err
	}
	var reply reserveReply
	if err := cn.roundTrip(ctx, c.next(), typeReserve, reserveRequest{Claim: claim, Reader: reader}, typeReserveReply, &reply, nil); err != nil {
		return types.MachineVerdict{}, err
	}
	return reply.Verdict, nil
}

// AcquireService starts, or reuses, the shared service spec under key and returns once
// it is ready, reporting whether the broker started it and so stops it at idle (false:
// it was already running and the broker leaves it alone). The reference rides this
// connection: ReleaseService drops it, and so does the connection closing. An *Error
// with CodeNoServices means the broker hosts none and the caller runs the service
// itself; CodeMalformed means spec has neither a command nor a start.
func (c *Client) AcquireService(ctx context.Context, key string, spec ServiceSpec) (owned bool, err error) {
	cn, err := c.open(ctx)
	if err != nil {
		return false, err
	}
	var reply serviceReply
	if err := cn.roundTrip(ctx, c.next(), typeServiceAcquire, serviceAcquireRequest{Key: key, Service: spec.wire()}, typeServiceReply, &reply, nil); err != nil {
		return false, err
	}
	return reply.Owned, nil
}

// ReleaseService drops one reference AcquireService took. The broker keeps the service
// warm for its idle window, so a later run reuses it.
func (c *Client) ReleaseService(ctx context.Context, key string) error {
	cn, err := c.connect(ctx)
	if err != nil {
		return err
	}
	return cn.roundTrip(ctx, c.next(), typeServiceRelease, serviceReleaseRequest{Key: key}, typeServiceReply, nil, nil)
}

// StopServices stops every service the broker hosts and returns how many, leaving the
// broker running.
func (c *Client) StopServices(ctx context.Context) (int, error) {
	cn, err := c.connect(ctx)
	if err != nil {
		return 0, err
	}
	var reply serviceReply
	if err := cn.roundTrip(ctx, c.next(), typeServiceStopAll, nil, typeServiceReply, &reply, nil); err != nil {
		return 0, err
	}
	return reply.Stopped, nil
}

// Status is the broker's own report: its capacity, every claim holding it, and the
// services it hosts.
func (c *Client) Status(ctx context.Context) (types.StatusBroker, error) {
	cn, err := c.connect(ctx)
	if err != nil {
		return types.StatusBroker{}, err
	}
	var st types.StatusBroker
	err = cn.roundTrip(ctx, c.next(), typeStatus, nil, typeStatusReply, &st, nil)
	return st, err
}

// Shutdown stops the broker and returns once it has hung up, which it does after it
// stops accepting. Every claim on the host is dropped with it; runs already going keep
// going, unarbitrated or refused according to their broker policy. Under a supervisor
// holding the socket, the next connection starts another broker.
func (c *Client) Shutdown(ctx context.Context) error {
	cn, err := c.connect(ctx)
	if err != nil {
		return err
	}
	if err := cn.roundTrip(ctx, c.next(), typeShutdown, shutdownRequest{Magic: shutdownMagic}, typeShutdownReply, nil, nil); err != nil {
		return err
	}
	select {
	case <-cn.dead:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close hangs up, which releases everything this client holds on the broker. Safe to
// call more than once.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	close(c.done)
	cn := c.cur
	c.cur = nil
	c.held = map[string]*heldClaim{}
	c.riders = map[string]*heldRider{}
	c.mu.Unlock()
	if cn != nil {
		return cn.nc.Close()
	}
	return nil
}

func (c *Client) next() uint64 { return c.nextID.Add(1) }

// connect returns the live connection, dialing one when there is none. A fresh
// connection re-asserts every claim this client still holds, and the groups riding
// them, before any other request uses it, so a new broker learns about running steps
// before it seats anything else.
// open is connect for a call that takes something from the broker, which is the only
// kind that may start one. The redial loop uses connect, so a broker that will not
// start is not respawned every tick.
func (c *Client) open(ctx context.Context) (*conn, error) {
	cn, err := c.connect(ctx)
	if err == nil || c.start == nil || !errors.Is(err, ErrUnavailable) {
		return cn, err
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed || !c.start(ctx) {
		return nil, err
	}
	return c.connect(ctx)
}

// dialHello connects and says hello once. The hello carries the handshake read after the
// broker's handshake frame, so a refusal is final: it names why and is not retried.
func (c *Client) dialHello(ctx context.Context, ep endpoint.Endpoint) (net.Conn, *conn, error) {
	nc, err := ep.Dial(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: dial %s: %w", ErrUnavailable, ep, err)
	}
	cn := newConn(nc, c.lost)
	if err := c.sayHello(ctx, cn, ep.Addr); err != nil {
		_ = nc.Close()
		return nil, nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return nc, cn, nil
}

func (c *Client) sayHello(ctx context.Context, cn *conn, sock string) error {
	refused := func(err error) error { return &HandshakeError{Path: handshakePath(sock), Err: err} }
	if err := cn.awaitHandshake(ctx); err != nil {
		return refused(err)
	}
	h := c.hello
	var err error
	if h.Handshake, err = readHandshake(sock); err != nil {
		return refused(err)
	}
	var hr helloReply
	if err := cn.roundTrip(ctx, c.next(), typeHello, h, typeHelloReply, &hr, nil); err != nil {
		var refusal *Error
		if errors.As(err, &refusal) && refusal.Code == CodeHandshake {
			return refused(err)
		}
		return fmt.Errorf("hello: %w", err)
	}
	return nil
}

func (c *Client) connect(ctx context.Context) (*conn, error) {
	if cn := c.live(); cn != nil {
		return cn, nil
	}
	c.connectMu.Lock()
	defer c.connectMu.Unlock()
	if cn := c.live(); cn != nil {
		return cn, nil
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("%w: client closed", ErrUnavailable)
	}
	if err := c.resolveScope(); err != nil {
		return nil, err
	}

	ep, err := endpoint.Parse(c.addr)
	if err != nil {
		return nil, fmt.Errorf("broker: %w", err)
	}
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	nc, cn, err := c.dialHello(dctx, ep)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	held := maps.Clone(c.held)
	c.mu.Unlock()
	marked := map[*heldClaim]bool{}
	for _, h := range held {
		var reply claimReply
		if err := cn.roundTrip(dctx, c.next(), typeClaim, claimRequest{Claim: h.claim, Reassert: true}, typeClaimReply, &reply, nil); err != nil {
			_ = nc.Close()
			return nil, err
		}
		c.mu.Lock()
		h.remote = reply.Verdict.ID
		overrun := h.overrun
		c.mu.Unlock()
		if overrun {
			c.sendOverrun(dctx, cn, h.remote)
			marked[h] = true
		}
	}
	riders, err := c.reassertRiders(ctx, dctx, cn)
	if err != nil {
		_ = nc.Close()
		return nil, err
	}
	if c.crashHint != "" {
		c.watchCrashes(dctx, cn)
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = nc.Close()
		return nil, fmt.Errorf("%w: client closed", ErrUnavailable)
	}
	c.cur = cn
	// A Release or ReleaseHold that ran before cn was published found no connection to
	// send on, so what it dropped is handed back here; one after it finds cn itself. So
	// is an Overrun that ran after its claim's reassert.
	var released, unheld, overrun []string
	for id, h := range held {
		switch {
		case c.held[id] != h:
			released = append(released, h.remote)
		case h.overrun && !marked[h]:
			overrun = append(overrun, h.remote)
		}
	}
	for id, r := range riders {
		if c.riders[id] != r && r.remote != "" {
			unheld = append(unheld, r.remote)
		}
	}
	c.mu.Unlock()
	// Riders first: a claim's release stops the groups still riding it.
	for _, id := range unheld {
		_, _ = cn.call(dctx, c.next(), typeHoldRelease, holdReleaseRequest{ID: id})
	}
	for _, id := range released {
		_, _ = cn.call(dctx, c.next(), typeRelease, releaseRequest{ClaimID: id})
	}
	for _, id := range overrun {
		c.sendOverrun(dctx, cn, id)
	}
	return cn, nil
}

// reassertRiders hands cn's broker every process group riding a claim this client
// holds, once the claims are re-asserted on it, and returns the riders it sent. A group
// the broker refuses runs unheld from then on, which is said; any other failure is
// returned, and cn is not used.
func (c *Client) reassertRiders(ctx, dctx context.Context, cn *conn) (map[string]*heldRider, error) {
	c.mu.Lock()
	riders := maps.Clone(c.riders)
	c.mu.Unlock()
	warned := false
	for id, r := range riders {
		c.mu.Lock()
		hold := r.hold
		var claim types.MachineClaim
		if h, ok := c.held[r.claim]; ok {
			hold.Claim = h.remote
			claim = h.claim
		}
		c.mu.Unlock()
		if hold.Claim == "" {
			continue
		}
		var reply holdReply
		err := cn.roundTrip(dctx, c.next(), typeHold, holdRequest{Hold: hold}, typeHoldReply, &reply, nil)
		var refused *Error
		switch {
		case errors.As(err, &refused):
			if !warned {
				warned = true
				slog.WarnContext(ctx, cache.UnheldGroupWarning(claim.Project, claim.Target, hold.PGID, hold.Command, err))
			}
			c.mu.Lock()
			if c.riders[id] == r {
				delete(c.riders, id)
			}
			c.mu.Unlock()
		case err != nil:
			return nil, err
		default:
			c.mu.Lock()
			r.remote = reply.ID
			c.mu.Unlock()
		}
	}
	return riders, nil
}

// live is the current connection if it is still open.
func (c *Client) live() *conn {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cur == nil {
		return nil
	}
	select {
	case <-c.cur.dead:
		c.cur = nil
		return nil
	default:
		return c.cur
	}
}

// lost runs when cn's connection closes. Claims it carried are gone with the broker
// that held them; when this client still holds any, it starts looking for the next
// broker to re-assert them on.
func (c *Client) lost(cn *conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cur == cn {
		c.cur = nil
	}
	if c.cur != nil {
		return
	}
	for _, h := range c.held {
		h.remote = ""
	}
	for _, r := range c.riders {
		r.remote = ""
	}
	if c.closed || c.redialing || len(c.held) == 0 {
		return
	}
	c.redialing = true
	go c.redial()
}

// redial reconnects while claims are held, so they are re-asserted on the next broker
// to answer even when this client makes no other request.
func (c *Client) redial() {
	defer func() {
		c.mu.Lock()
		c.redialing = false
		c.mu.Unlock()
	}()
	t := time.NewTicker(redialEvery)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
		}
		c.mu.Lock()
		holding := len(c.held) > 0
		c.mu.Unlock()
		if !holding {
			return
		}
		if _, err := c.connect(context.Background()); err == nil {
			return
		}
	}
}
