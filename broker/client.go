package broker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egladman/magus/internal/proc/endpoint"
	"github.com/egladman/magus/types"
)

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
// When its broker dies, a Client holding claims redials on its own and re-asserts them
// on the next broker to answer.
type Client struct {
	addr  string
	hello hello
	start func(context.Context) bool

	connectMu sync.Mutex

	mu        sync.Mutex
	cur       *conn
	held      map[string]*heldClaim
	closed    bool
	redialing bool
	done      chan struct{}

	nextID atomic.Uint64
}

// heldClaim is a claim this client holds, and the id the current broker knows it by;
// remote is empty while no broker does.
type heldClaim struct {
	claim  types.MachineClaim
	remote string
}

// NewClient returns a client for the broker at addr (a unix:// URL or a bare path). It
// does not dial; the first request does.
func NewClient(addr string, opts ...ClientOption) *Client {
	dir, _ := os.Getwd()
	c := &Client{
		addr:  addr,
		hello: hello{Magic: helloMagic, Protocol: ProtocolVersion, MinProtocol: MinProtocolVersion, PID: os.Getpid(), Dir: dir, Argv: os.Args},
		held:  map[string]*heldClaim{},
		done:  make(chan struct{}),
	}
	for _, fn := range opts {
		fn(c)
	}
	return c
}

// Dial returns a client already connected to the broker at addr, or an error wrapping
// ErrUnavailable when none answers.
func Dial(ctx context.Context, addr string, opts ...ClientOption) (*Client, error) {
	c := NewClient(addr, opts...)
	if _, err := c.connect(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

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
			_, _ = cn.call(context.Background(), c.next(), typeRelease, releaseRequest{ClaimID: r.Verdict.ID}, nil)
		}
	}
	if err := cn.roundTrip(ctx, c.next(), typeClaim, claimRequest{Claim: claim}, typeClaimReply, &reply, late); err != nil {
		return types.MachineVerdict{}, err
	}
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
	cn := c.cur
	c.mu.Unlock()
	if !ok || h.remote == "" || cn == nil {
		return
	}
	_, _ = cn.call(ctx, c.next(), typeRelease, releaseRequest{ClaimID: h.remote}, nil)
}

// AcquireService starts, or reuses, the shared service spec under key and returns once
// it is ready. The reference rides this connection: ReleaseService drops it, and so
// does the connection closing. ErrNoServices (the broker hosts none) and ErrUnsupported
// (it predates something spec asks for) both mean the caller runs the service itself.
func (c *Client) AcquireService(ctx context.Context, key string, spec ServiceSpec) error {
	cn, err := c.open(ctx)
	if err != nil {
		return err
	}
	return cn.roundTrip(ctx, c.next(), typeServiceAcquire, serviceAcquireRequest{Key: key, Service: spec.wire()}, typeServiceReply, nil, nil)
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
	c.mu.Unlock()
	if cn != nil {
		return cn.nc.Close()
	}
	return nil
}

func (c *Client) next() uint64 { return c.nextID.Add(1) }

// open is connect for a call that takes something from the broker, which is the only
// kind that may start one. The redial loop uses connect, so a broker that will not
// start is not respawned every tick. A broker that answered but refused the hello
// (ErrProtocol) is live, and starting another would only lose the bind to it.
func (c *Client) open(ctx context.Context) (*conn, error) {
	cn, err := c.connect(ctx)
	if err == nil || c.start == nil || !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrProtocol) {
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

// connect returns the live connection, dialing one when there is none. A fresh
// connection re-asserts every claim this client still holds before any other request
// uses it, so a new broker learns about running steps before it seats anything else.
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

	ep, err := endpoint.Parse(c.addr)
	if err != nil {
		return nil, fmt.Errorf("broker: %w", err)
	}
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	nc, err := ep.Dial(dctx)
	if err != nil {
		return nil, fmt.Errorf("%w: dial %s: %w", ErrUnavailable, ep, err)
	}
	cn := newConn(nc, c.lost)
	var hr helloReply
	err = cn.roundTrip(dctx, c.next(), typeHello, c.hello, typeHelloReply, &hr, nil)
	if err == nil && (hr.Protocol < c.hello.MinProtocol || hr.Protocol > c.hello.Protocol) {
		err = &Error{Code: CodeProtocol, Message: fmt.Sprintf("broker: chose protocol %d, outside the %d..%d this client offered",
			hr.Protocol, c.hello.MinProtocol, c.hello.Protocol)}
	}
	if err != nil {
		_ = nc.Close()
		// A broker speaking another protocol is no arbiter for this client either.
		return nil, fmt.Errorf("%w: hello: %w", ErrUnavailable, err)
	}

	c.mu.Lock()
	held := make([]*heldClaim, 0, len(c.held))
	for _, h := range c.held {
		held = append(held, h)
	}
	c.mu.Unlock()
	for _, h := range held {
		var reply claimReply
		if err := cn.roundTrip(dctx, c.next(), typeClaim, claimRequest{Claim: h.claim, Reassert: true}, typeClaimReply, &reply, nil); err != nil {
			_ = nc.Close()
			return nil, err
		}
		c.mu.Lock()
		h.remote = reply.Verdict.ID
		c.mu.Unlock()
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		_ = nc.Close()
		return nil, fmt.Errorf("%w: client closed", ErrUnavailable)
	}
	c.cur = cn
	return cn, nil
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
