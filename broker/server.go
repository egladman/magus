package broker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/types"
)

// DefaultIdleExit is how long a broker stays up once it holds nothing. Longer than the
// gap between commands in an edit-run loop, so an interactive session never pays the
// restart, and short enough that a machine left alone reclaims the process.
const DefaultIdleExit = 10 * time.Minute

// helloTimeout bounds how long an accepted connection may take to say hello, so a
// client that connects and never writes cannot park a goroutine forever.
const helloTimeout = 10 * time.Second

// ServiceHost runs the shared services a broker hosts. Acquire returns once the
// service is ready and adds one dependent; Release drops one. The broker calls Release
// once per Acquire a connection made when that connection closes, however it closed.
type ServiceHost interface {
	Acquire(ctx context.Context, key string, spec ServiceSpec) error
	Release(key string)
	// StopAll stops every hosted service, returning how many, and leaves the host usable.
	StopAll() int
	Snapshot() []types.StatusService
}

// Option configures Serve.
type Option func(*serveOptions)

type serveOptions struct {
	budgetMB, budgetSlots int
	services              ServiceHost
	idleExit              time.Duration
	log                   *slog.Logger
	version               string
}

// WithCapacity sizes the host's budget. A non-positive figure leaves that axis
// unlimited, the fallback for a host magus cannot measure.
func WithCapacity(memoryMB, slots int) Option {
	return func(o *serveOptions) { o.budgetMB, o.budgetSlots = memoryMB, slots }
}

// WithServices hosts shared services on h. Without it a service request is answered
// with CodeNoServices and the client runs the service itself.
func WithServices(h ServiceHost) Option { return func(o *serveOptions) { o.services = h } }

// WithIdleExit sets how long Serve keeps going once it holds nothing. Zero never exits
// for idleness; the default is DefaultIdleExit.
func WithIdleExit(d time.Duration) Option { return func(o *serveOptions) { o.idleExit = d } }

// WithLogger sets where Serve reports what it does. The default discards.
func WithLogger(l *slog.Logger) Option { return func(o *serveOptions) { o.log = l } }

// WithVersion is the build Serve reports in status and hello.
func WithVersion(v string) Option { return func(o *serveOptions) { o.version = v } }

// Serve runs a broker on ln until ctx ends, a client sends shutdown, or it has held
// nothing for its idle window. It closes ln and every connection before returning, so
// every claim is gone by then; stopping hosted services is the caller's, since the
// caller owns the host. It returns nil on any of those three, and an error only when
// accepting fails for another reason.
func Serve(ctx context.Context, ln net.Listener, opts ...Option) error {
	o := serveOptions{idleExit: DefaultIdleExit, log: slog.New(slog.DiscardHandler)}
	for _, fn := range opts {
		fn(&o)
	}
	exe, _ := os.Executable()
	s := &server{
		opts:       o,
		budget:     cache.NewMachineBudget(o.budgetMB, o.budgetSlots),
		socket:     "unix://" + ln.Addr().String(),
		executable: exe,
		started:    time.Now(),
		conns:      map[net.Conn]*session{},
		stop:       make(chan struct{}),
		ctx:        ctx,
	}
	s.touch()

	acceptErr := make(chan error, 1)
	go func() { acceptErr <- s.accept(ln) }()

	var tick <-chan time.Time
	if o.idleExit > 0 {
		t := time.NewTicker(min(max(o.idleExit/4, 10*time.Millisecond), 30*time.Second))
		defer t.Stop()
		tick = t.C
	}

	var err error
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case <-s.stop:
			break loop
		case err = <-acceptErr:
			break loop
		case now := <-tick:
			if idle := s.idleFor(now); idle >= o.idleExit {
				o.log.InfoContext(ctx, "broker: exiting; it has held nothing", slog.Duration("idle", idle.Round(time.Second)))
				break loop
			}
		}
	}
	_ = ln.Close()
	s.closeConns()
	s.wg.Wait()
	return err
}

type server struct {
	opts       serveOptions
	budget     *cache.MachineBudget
	socket     string
	executable string
	started    time.Time
	ctx        context.Context

	mu         sync.Mutex
	conns      map[net.Conn]*session
	lastActive time.Time
	inflight   int
	closing    bool

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// session is what one connection holds. It is released whole when the connection
// closes.
type session struct {
	hello    hello
	claims   map[string]struct{}
	services map[string]int
}

func (s *server) accept(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("broker: accept: %w", err)
		}
		s.mu.Lock()
		if s.closing {
			s.mu.Unlock()
			_ = conn.Close()
			return nil
		}
		s.conns[conn] = nil
		s.wg.Add(1)
		s.mu.Unlock()
		go s.handle(conn)
	}
}

func (s *server) closeConns() {
	s.mu.Lock()
	s.closing = true
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
}

// touch records that the broker did something a person would call use.
func (s *server) touch() {
	s.mu.Lock()
	s.lastActive = time.Now()
	s.mu.Unlock()
}

// idleFor is how long the broker has held nothing: no claim, no service reference, no
// request in flight. A connection that holds none of those (a server that only says
// hello, a status query) does not keep it up.
func (s *server) idleFor(now time.Time) time.Duration {
	holding := len(s.budget.Snapshot().Holders) > 0
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inflight > 0 {
		holding = true
	}
	for _, sess := range s.conns {
		if sess != nil && len(sess.services) > 0 {
			holding = true
		}
	}
	if holding {
		s.lastActive = now
		return 0
	}
	return now.Sub(s.lastActive)
}

func (s *server) handle(conn net.Conn) {
	defer s.wg.Done()
	defer func() { _ = conn.Close() }()

	r := newFrameReader(conn)
	w := &frameWriter{w: conn}
	_ = conn.SetReadDeadline(time.Now().Add(helloTimeout))
	first, err := r.read()
	if err != nil {
		s.forget(conn, nil)
		return
	}
	var h hello
	if first.Type != typeHello || decodeBody(first, &h) != nil || h.Magic != helloMagic || h.Protocol != ProtocolVersion {
		_ = w.write(typeError, first.ID, errorReply{Code: CodeProtocol,
			Message: fmt.Sprintf("broker: expected a hello speaking protocol %d", ProtocolVersion)})
		s.forget(conn, nil)
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	sess := &session{hello: h, claims: map[string]struct{}{}, services: map[string]int{}}
	s.mu.Lock()
	s.conns[conn] = sess
	s.mu.Unlock()
	_ = w.write(typeHelloReply, first.ID, helloReply{PID: os.Getpid(), Protocol: ProtocolVersion, Version: s.opts.version})

	var reqs sync.WaitGroup
	for {
		f, err := r.read()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				s.opts.log.DebugContext(s.ctx, "broker: connection ended", slog.Int("pid", h.PID), slog.String("error", err.Error()))
			}
			break
		}
		if f.Type != typeStatus {
			s.touch()
		}
		s.mu.Lock()
		s.inflight++
		s.mu.Unlock()
		reqs.Add(1)
		go func() {
			defer reqs.Done()
			defer func() {
				s.mu.Lock()
				s.inflight--
				s.mu.Unlock()
			}()
			s.dispatch(sess, w, f)
		}()
	}
	// A request still running may add to the session (a service still starting), so
	// release only once every one has answered.
	reqs.Wait()
	s.forget(conn, sess)
}

// forget drops conn and releases everything its session holds.
func (s *server) forget(conn net.Conn, sess *session) {
	s.mu.Lock()
	delete(s.conns, conn)
	var claims []string
	var services map[string]int
	if sess != nil {
		for id := range sess.claims {
			claims = append(claims, id)
		}
		services = sess.services
		sess.claims, sess.services = map[string]struct{}{}, map[string]int{}
	}
	s.mu.Unlock()
	for _, id := range claims {
		s.budget.Release(id)
	}
	for key, n := range services {
		for range n {
			s.opts.services.Release(key)
		}
	}
	if len(claims) > 0 || len(services) > 0 {
		s.opts.log.InfoContext(s.ctx, "broker: released a closed connection's hold",
			slog.Int("pid", sess.hello.PID), slog.Int("claims", len(claims)), slog.Int("services", len(services)))
	}
}

func (s *server) dispatch(sess *session, w *frameWriter, f frame) {
	fail := func(code ErrorCode, format string, args ...any) {
		_ = w.write(typeError, f.ID, errorReply{Code: code, Message: fmt.Sprintf(format, args...)})
	}
	switch f.Type {
	case typeClaim:
		var req claimRequest
		if err := decodeBody(f, &req); err != nil {
			fail(CodeMalformed, "broker: decode claim: %v", err)
			return
		}
		c := s.attribute(sess, req.Claim)
		var v types.MachineVerdict
		if req.Reassert {
			v = types.MachineVerdict{Granted: true, Fits: true, ID: s.budget.Assert(c)}
		} else {
			v = s.budget.Request(c)
		}
		if v.Granted {
			s.mu.Lock()
			sess.claims[v.ID] = struct{}{}
			s.mu.Unlock()
		}
		_ = w.write(typeClaimReply, f.ID, claimReply{Verdict: v})

	case typeRelease:
		var req releaseRequest
		if err := decodeBody(f, &req); err != nil {
			fail(CodeMalformed, "broker: decode release: %v", err)
			return
		}
		s.mu.Lock()
		_, mine := sess.claims[req.ClaimID]
		delete(sess.claims, req.ClaimID)
		s.mu.Unlock()
		if mine {
			s.budget.Release(req.ClaimID)
		}
		_ = w.write(typeReleaseReply, f.ID, nil)

	case typeServiceAcquire:
		var req serviceAcquireRequest
		if err := decodeBody(f, &req); err != nil || req.Key == "" || len(req.Service.Command) == 0 {
			fail(CodeMalformed, "broker: a service acquire needs a key and a command")
			return
		}
		if s.opts.services == nil {
			fail(CodeNoServices, "broker: this broker hosts no services")
			return
		}
		// The broker's own context, not the request's: the service outlives the run
		// that asked for it.
		if err := s.opts.services.Acquire(s.ctx, req.Key, req.Service.spec()); err != nil {
			fail(CodeService, "%v", err)
			return
		}
		s.mu.Lock()
		sess.services[req.Key]++
		s.mu.Unlock()
		_ = w.write(typeServiceReply, f.ID, serviceReply{})

	case typeServiceRelease:
		var req serviceReleaseRequest
		if err := decodeBody(f, &req); err != nil {
			fail(CodeMalformed, "broker: decode service release: %v", err)
			return
		}
		s.mu.Lock()
		held := sess.services[req.Key] > 0
		if held {
			sess.services[req.Key]--
			if sess.services[req.Key] == 0 {
				delete(sess.services, req.Key)
			}
		}
		s.mu.Unlock()
		if held && s.opts.services != nil {
			s.opts.services.Release(req.Key)
		}
		_ = w.write(typeServiceReply, f.ID, serviceReply{})

	case typeServiceStopAll:
		n := 0
		if s.opts.services != nil {
			n = s.opts.services.StopAll()
		}
		_ = w.write(typeServiceReply, f.ID, serviceReply{Stopped: n})

	case typeStatus:
		_ = w.write(typeStatusReply, f.ID, s.status())

	case typeShutdown:
		var req shutdownRequest
		if err := decodeBody(f, &req); err != nil || req.Magic != shutdownMagic {
			fail(CodeMalformed, "broker: shutdown needs its magic")
			return
		}
		_ = w.write(typeShutdownReply, f.ID, nil)
		s.stopOnce.Do(func() { close(s.stop) })

	default:
		fail(CodeUnknownType, "broker: unknown frame type %q", f.Type)
	}
}

// attribute fills in who holds a claim from the connection's hello, where the claim
// left it out.
func (s *server) attribute(sess *session, c types.MachineClaim) types.MachineClaim {
	if c.PID == 0 {
		c.PID = sess.hello.PID
	}
	if c.Dir == "" {
		c.Dir = sess.hello.Dir
	}
	if c.Command == "" && len(sess.hello.Argv) > 0 {
		c.Command = strings.Join(sess.hello.Argv, " ")
	}
	return c
}

func (s *server) status() types.StatusBroker {
	st := types.StatusBroker{
		PID:             os.Getpid(),
		Version:         s.opts.version,
		Protocol:        ProtocolVersion,
		Socket:          s.socket,
		Executable:      s.executable,
		StartTime:       s.started,
		Capacity:        s.budget.Snapshot(),
		IdleExitSeconds: int(s.opts.idleExit / time.Second),
	}
	if s.opts.services != nil {
		st.Services = s.opts.services.Snapshot()
	}
	return st
}
