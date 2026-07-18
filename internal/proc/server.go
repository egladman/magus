package proc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/proc/endpoint"
	"github.com/egladman/magus/types"
)

// maxArgs caps RunRequest.Args to prevent OOM from untrusted callers.
const maxArgs = 256

// handshakeTimeout bounds how long an accepted connection may take to deliver its
// request frame. Without it a client that connects and never writes parks the
// handleConn goroutine forever inside readFrame — leaking a goroutine/fd, blocking
// Server.Close (connWg.Wait), and letting any local caller DoS the socket.
const handshakeTimeout = 30 * time.Second

type contextKey int

const (
	rootCtxKey contextKey = iota
	cwdCtxKey
	jobCtxKey
)

// WithRoot returns ctx carrying the client-sent workspace root, readable via RootFromContext.
func WithRoot(ctx context.Context, root string) context.Context {
	return context.WithValue(ctx, rootCtxKey, root)
}

// WithCwd returns ctx carrying the client's working directory, readable via CwdFromContext.
func WithCwd(ctx context.Context, cwd string) context.Context {
	return context.WithValue(ctx, cwdCtxKey, cwd)
}

// RootFromContext returns the workspace root stored by the proc server, or "".
func RootFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(rootCtxKey).(string); ok {
		return v
	}
	return ""
}

// CwdFromContext returns the child's working directory stored by the proc server, or "".
func CwdFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(cwdCtxKey).(string); ok {
		return v
	}
	return ""
}

// withJob marks ctx as a background job invocation (submitJob), distinct from an adopted run.
// The daemon's handler reads it via IsJob to route jobs through the full command set while a
// plain adopted run stays limited to run/affected.
func withJob(ctx context.Context) context.Context {
	return context.WithValue(ctx, jobCtxKey, true)
}

// IsJob reports whether ctx belongs to a background job (submitted via SubmitJob) rather than
// an adopted run. The daemon's dispatch handler branches on it.
func IsJob(ctx context.Context) bool {
	v, _ := ctx.Value(jobCtxKey).(bool)
	return v
}

// Options configures the proc server created by New.
type Options struct {
	Handler         func(ctx context.Context, args []string) error // required; ctx carries Root/Cwd
	Context         context.Context                                // nil → context.Background
	Limiter         *cache.Limiter                                 // shared budget; nil → private limiter
	Concurrency     int                                            // ignored when Limiter is set; 0 → default
	Version         string                                         // "" disables version-mismatch check
	Address         string                                         // "" → auto-generate in SockDir()
	WorkspaceLister func() []Workspace                             // optional; used by daemon Status RPC
	ServiceLister   func() []types.StatusService                   // optional; hosted-services snapshot for the daemon Status RPC
	ServiceHost     ServiceHost                                    // optional; hosts shared services across invocations (daemon only)
	// OnJobDone, if set, is called after every BACKGROUND job (submitJob) completes - never for
	// an adopted foreground run - with the job's args, wall-clock duration, and outcome. The
	// ctx still carries Root/Cwd. The daemon uses it to record a KIND_JOB activity event; proc
	// stays decoupled from the trail and cache layout.
	OnJobDone func(ctx context.Context, args []string, dur time.Duration, err error)
}

// Server listens on a Unix-domain socket and accepts forwarded RPC requests from child processes.
type Server struct {
	ep       endpoint.Endpoint
	listener net.Listener
	svc      *service
	cancel   context.CancelFunc
	once     sync.Once
	connWg   sync.WaitGroup // tracks in-flight handleConn goroutines
	done     chan struct{}  // closed by Close to stop the signal watcher goroutine
}

// Addr returns the canonical unix:// URL that children dial. Valid after New.
func (s *Server) Addr() string { return s.ep.String() }

// Done returns a channel closed when the server has been Closed, whether by an RPC
// shutdown request or a signal. A blocking daemon loop selects on it so an RPC-driven
// `magus server stop` unblocks the process the same way a signal does: without it the
// shutdown handler tears down the listener but the process keeps running, since the
// listener's context is a sibling of the process context, not its parent.
func (s *Server) Done() <-chan struct{} { return s.done }

// Close shuts down the listener, removes the socket file, and waits for all in-flight handlers.
// Safe to call multiple times.
func (s *Server) Close() {
	s.once.Do(func() {
		close(s.done) // unblocks watchSignals before we cancel/close
		if s.cancel != nil {
			s.cancel()
		}
		if s.listener != nil {
			_ = s.listener.Close()
		}
		_ = os.Remove(s.ep.Addr)
	})
	s.connWg.Wait() // wait outside the once so concurrent callers all block
}

// New constructs an unstarted Server; returns ErrAlreadyAdopted when MAGUS_DAEMON_SOCKET is set.
// Call Start to bind the socket.
func New(opts Options) (*Server, error) {
	if os.Getenv("MAGUS_DAEMON_SOCKET") != "" {
		return nil, ErrAlreadyAdopted
	}

	var ep endpoint.Endpoint
	if opts.Address != "" {
		var err error
		ep, err = endpoint.ParseEndpoint(opts.Address)
		if err != nil {
			return nil, fmt.Errorf("proc: invalid address: %w", err)
		}
	} else {
		rnd := make([]byte, 4)
		if _, err := rand.Read(rnd); err != nil {
			return nil, fmt.Errorf("proc: random bytes: %w", err)
		}
		sockName := fmt.Sprintf("magus-%d-%s.sock", os.Getpid(), hex.EncodeToString(rnd))
		ep = endpoint.Endpoint{Scheme: "unix", Addr: filepath.Join(sockDir(), sockName)}
	}

	lim := opts.Limiter
	if lim == nil {
		par := opts.Concurrency
		if par <= 0 {
			par = cache.DefaultConcurrency()
		}
		lim = cache.NewLimiter(par)
	}

	parentCtx := opts.Context
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	serverCtx, cancel := context.WithCancel(parentCtx)

	svc := &service{
		handler:         opts.Handler,
		serviceHost:     opts.ServiceHost,
		parentCtx:       serverCtx,
		lim:             lim,
		version:         opts.Version,
		gateVersion:     adoptionIdentity(opts.Version),
		workspaceLister: opts.WorkspaceLister,
		serviceLister:   opts.ServiceLister,
		onJobDone:       opts.OnJobDone,
	}
	srv := &Server{
		ep:     ep,
		svc:    svc,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	svc.shutdownFn = srv.Close
	return srv, nil
}

// Start binds the socket and begins serving. Must be called once; on error the Server is unusable.
func (s *Server) Start() error {
	ln, err := s.ep.Listen()
	if err != nil {
		// If the address is already in use, check whether the socket is live.
		// If it's a stale leftover from a previous crash, remove it and retry
		// once. This avoids the TOCTOU race of unconditionally removing first.
		var opErr *net.OpError
		if errors.As(err, &opErr) && (strings.Contains(opErr.Err.Error(), "address already in use") ||
			strings.Contains(opErr.Err.Error(), "bind: address already in use")) {
			if !isSocketLive(s.svc.parentCtx, s.ep.Addr) {
				_ = os.Remove(s.ep.Addr)
				ln, err = s.ep.Listen()
			}
		}
		if err != nil {
			s.cancel()
			return fmt.Errorf("proc: listen %s: %w", s.ep, err)
		}
	}
	// Socket security comes from the parent directory (0700 per sockdir_unix.go);
	// a post-Listen chmod would create a brief world-accessible window.
	s.listener = ln

	go serve(s, s.svc)
	watchSignals(s, s.cancel)
	return nil
}

// isSocketLive probes addr; returns false for stale sockets. 100 ms hard cap prevents blocking.
func isSocketLive(ctx context.Context, addr string) bool {
	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", addr)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func serve(srv *Server, svc *service) {
	for {
		conn, err := srv.listener.Accept()
		if err != nil {
			return // listener was closed
		}
		srv.connWg.Add(1)
		go handleConn(svc, conn, &srv.connWg)
	}
}

// writeErr sends an error reply; ignores send errors (best-effort on a broken connection).
func writeErr(conn net.Conn, msg string) {
	_ = writeFrame(conn, typeError, ErrorReply{Message: msg})
}

// handleConn reads one JSONL request frame, dispatches to the service, and writes the reply.
func handleConn(svc *service, conn net.Conn, wg *sync.WaitGroup) {
	defer wg.Done()
	defer func() { _ = conn.Close() }()

	_ = conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	typ, line, err := readFrame(conn)
	if errors.Is(err, io.EOF) {
		return // bare liveness probe (isSocketLive dialled and closed), silent no-op
	}
	if err != nil {
		writeErr(conn, err.Error())
		return
	}

	switch typ {
	case typeRun:
		var req RunRequest
		if err := codec.Unmarshal(line, &req); err != nil {
			writeErr(conn, "proc: decode run request: "+err.Error())
			return
		}
		var reply RunReply
		if err := svc.run(req, &reply); err != nil {
			writeErr(conn, err.Error())
			return
		}
		_ = writeFrame(conn, typeRunReply, reply)

	case typeJob:
		var req JobRequest
		if err := codec.Unmarshal(line, &req); err != nil {
			writeErr(conn, "proc: decode job request: "+err.Error())
			return
		}
		var reply JobReply
		if err := svc.submitJob(req, &reply); err != nil {
			writeErr(conn, err.Error())
			return
		}
		_ = writeFrame(conn, typeJobReply, reply)

	case typeStatus:
		var req StatusRequest
		if err := codec.Unmarshal(line, &req); err != nil {
			writeErr(conn, "proc: decode status request: "+err.Error())
			return
		}
		var reply StatusReply
		if err := svc.status(req, &reply); err != nil {
			writeErr(conn, err.Error())
			return
		}
		_ = writeFrame(conn, typeStatusReply, reply)

	case typeShutdown:
		var req ShutdownRequest
		if err := codec.Unmarshal(line, &req); err != nil {
			writeErr(conn, "proc: decode shutdown request: "+err.Error())
			return
		}
		var reply ShutdownReply
		if err := svc.shutdown(req, &reply); err != nil {
			writeErr(conn, err.Error())
			return
		}
		_ = writeFrame(conn, typeShutdownReply, reply)

	case typeServiceAcquire:
		var req ServiceAcquireRequest
		if err := codec.Unmarshal(line, &req); err != nil {
			writeErr(conn, "proc: decode service.acquire request: "+err.Error())
			return
		}
		var reply ServiceAcquireReply
		svc.serviceAcquire(req, &reply)
		_ = writeFrame(conn, typeServiceAcquireReply, reply)

	case typeServiceRelease:
		var req ServiceReleaseRequest
		if err := codec.Unmarshal(line, &req); err != nil {
			writeErr(conn, "proc: decode service.release request: "+err.Error())
			return
		}
		svc.serviceRelease(req)
		_ = writeFrame(conn, typeServiceReleaseReply, ServiceReleaseReply{})

	case typeServiceStopAll:
		var req ServiceStopAllRequest
		if err := codec.Unmarshal(line, &req); err != nil {
			writeErr(conn, "proc: decode service.stopall request: "+err.Error())
			return
		}
		_ = req
		count := 0
		if svc.serviceHost != nil {
			count = svc.serviceHost.StopAll()
		}
		_ = writeFrame(conn, typeServiceStopAllReply, ServiceStopAllReply{Count: count})

	default:
		writeErr(conn, fmt.Sprintf("proc: unknown frame type %q", typ))
	}
}

// serviceAcquire starts (or reuses) a shared service on the daemon's ServiceHost so
// it stays warm across invocations. A daemon with no host (a per-process proc server)
// reports that hosting is unavailable, so the client falls back to running the
// service in-process for the current run.
func (s *service) serviceAcquire(req ServiceAcquireRequest, reply *ServiceAcquireReply) {
	if s.serviceHost == nil {
		reply.Err = "proc: service.acquire: this server does not host shared services"
		return
	}
	// The acquire runs under the daemon's own context, not the caller's, so the
	// service outlives the requesting invocation.
	if err := s.serviceHost.Acquire(s.parentCtx, req.Key, req.Service); err != nil {
		reply.Err = err.Error()
	}
}

// serviceRelease drops one dependent's hold on a shared service. Releasing an unknown
// key, or on a server without a host, is a no-op.
func (s *service) serviceRelease(req ServiceReleaseRequest) {
	if s.serviceHost != nil {
		s.serviceHost.Release(req.Key)
	}
}

// activeCall is the per-request state tracked for the Status RPC.
type activeCall struct {
	Call
	SubOp *SubOp
}

type service struct {
	handler         func(ctx context.Context, args []string) error
	parentCtx       context.Context
	lim             *cache.Limiter
	version         string // human-facing display version; surfaced as StatusReply.DaemonVersion
	gateVersion     string // adoption identity for the version gate (see adoptionIdentity); "" disables the gate
	workspaceLister func() []Workspace
	serviceLister   func() []types.StatusService
	serviceHost     ServiceHost
	onJobDone       func(ctx context.Context, args []string, dur time.Duration, err error)
	inflight        sync.Map // cycleKey → struct{}, for cycle detection
	calls           sync.Map // uint64 id → *activeCall, for Status reporting
	nextID          atomic.Uint64
	shutdownFn      func() // called by shutdown handler; set by New to srv.Close
}

// versionAdmits reports whether a request carrying reqVersion may be adopted by this
// server. It is the single gate shared by run and submitJob: both the client's reqVersion
// and the server's gateVersion are adoption identities (see adoptionIdentity), so a match
// means the two builds are provably the same code. An empty identity on EITHER side
// disables the check - the "" escape hatch for test injection and pre-versioning clients.
func (s *service) versionAdmits(reqVersion string) bool {
	return s.gateVersion == "" || reqVersion == "" || reqVersion == s.gateVersion
}

func (s *service) run(req RunRequest, reply *RunReply) error {
	if len(req.Args) > maxArgs {
		return fmt.Errorf("proc: RunRequest.Args exceeds limit (%d > %d)", len(req.Args), maxArgs)
	}
	if req.Protocol != "" && req.Protocol != ProtocolV2 {
		return ErrProtocolMismatch
	}
	if !s.versionAdmits(req.Version) {
		return ErrVersionMismatch
	}

	ctx, cancel := context.WithCancel(s.parentCtx)
	defer cancel()

	ctx = WithRoot(ctx, req.Root)
	ctx = WithCwd(ctx, req.Cwd)

	key := cycleKey(req.Root, req.Cwd, req.Args)
	if _, loaded := s.inflight.LoadOrStore(key, struct{}{}); loaded {
		reply.ExitCode = 1
		reply.Err = ErrCycleDetected.Error()
		return nil
	}
	defer s.inflight.Delete(key)

	// Mint the invocation id here, before dispatch, and thread it onto ctx so the adopted
	// run's BeginInvocation reuses it (rather than minting its own). That lets this pool
	// entry carry its inv - the key a dashboard uses to deep-link into the run's live log.
	inv := journal.NewInvocationID()
	ctx = journal.WithInvocationID(ctx, inv)

	id := s.nextID.Add(1)
	call := &activeCall{
		Call:  Call{Args: req.Args, Workspace: req.Root, StartedAt: time.Now(), Inv: inv},
		SubOp: &SubOp{},
	}
	s.calls.Store(id, call)
	defer s.calls.Delete(id)
	ctx = WithSubOp(ctx, call.SubOp)

	if err := s.lim.Acquire(ctx); err != nil {
		reply.ExitCode = 1
		reply.Err = err.Error()
		return nil
	}
	defer s.lim.Release()

	// The acquired slot gates admission, but the forwarded build runs its own
	// RunAll against the same shared Limiter. Holding our slot for the whole
	// forwarded run would steal one slot from that pool per adopted child and
	// inflate Status.Running, so yield it for the duration of the handler and
	// reacquire before returning (mirrors client.RunChildSync's Yield).
	if err := s.lim.Yield(ctx, func() error { return s.handler(ctx, req.Args) }); err != nil {
		if errors.Is(err, ErrNotAdoptable) { // propagate so client falls back to local execution
			return err
		}
		// os.exit(code) from a magusfile: honor the requested code in the reply
		// rather than collapsing every failure to 1. Code 0 is a clean early exit.
		var exitErr types.ExitError
		if errors.As(err, &exitErr) {
			reply.ExitCode = exitErr.Code
			if exitErr.Code != 0 {
				reply.Err = err.Error()
			}
			return nil
		}
		reply.ExitCode = 1
		reply.Err = err.Error()
		return nil
	}
	reply.ExitCode = 0
	return nil
}

// submitJob accepts a fire-and-forget background job: it registers the invocation (so it
// shows in the Dashboard like an adopted run), spawns the handler on the server's
// long-lived context, and returns immediately - unlike run, which blocks until done. A
// duplicate job already in flight (same workspace + args) is coalesced: no second run
// starts, so a rapid series of checkouts collapses to one refresh. The job's own
// success/failure is observed via the Dashboard/logs, not the reply.
func (s *service) submitJob(req JobRequest, reply *JobReply) error {
	if req.Magic != JobMagic {
		return nil // ignore unauthenticated submissions, matching status/shutdown
	}
	if len(req.Args) > maxArgs {
		return fmt.Errorf("proc: JobRequest.Args exceeds limit (%d > %d)", len(req.Args), maxArgs)
	}
	if req.Protocol != "" && req.Protocol != ProtocolV2 {
		return ErrProtocolMismatch
	}
	if !s.versionAdmits(req.Version) {
		return ErrVersionMismatch
	}

	// Namespace the job key so it never collides with run's cycle-detection keyspace:
	// a foreground `run` of the same args must not see a background job as a cycle (and
	// vice versa). The prefix keeps job coalescing (dedupe identical in-flight jobs)
	// separate from cycle detection.
	key := "job\x00" + cycleKey(req.Root, req.Cwd, req.Args)
	if _, loaded := s.inflight.LoadOrStore(key, struct{}{}); loaded {
		return nil // an identical job is already running; coalesce
	}

	// The Dashboard labels the job by workspace; when the caller left Root empty (the
	// daemon resolves it from Cwd), fall back to Cwd so the label is never blank.
	workspace := req.Root
	if workspace == "" {
		workspace = req.Cwd
	}
	inv := journal.NewInvocationID()
	id := s.nextID.Add(1)
	call := &activeCall{
		Call:  Call{Args: req.Args, Workspace: workspace, StartedAt: time.Now(), Inv: inv},
		SubOp: &SubOp{},
	}
	s.calls.Store(id, call)
	reply.Inv = inv

	// Run on the server's context, not the connection's: the job must outlive the
	// socket round-trip that submitted it.
	go func() {
		defer s.inflight.Delete(key)
		defer s.calls.Delete(id)

		ctx, cancel := context.WithCancel(s.parentCtx)
		defer cancel()
		ctx = WithRoot(ctx, req.Root)
		ctx = WithCwd(ctx, req.Cwd)
		ctx = journal.WithInvocationID(ctx, inv)
		ctx = WithSubOp(ctx, call.SubOp)
		ctx = withJob(ctx) // route through the full job command set, not the run/affected adoption allowlist

		if err := s.lim.Acquire(ctx); err != nil {
			return
		}
		defer s.lim.Release()
		// Yield the admission slot for the handler's duration, as run does, so the job
		// competes fairly in the shared pool instead of pinning a slot.
		jobStart := time.Now()
		err := s.lim.Yield(ctx, func() error { return s.handler(ctx, req.Args) })
		if s.onJobDone != nil {
			s.onJobDone(ctx, req.Args, time.Since(jobStart), err)
		}
		if err != nil {
			slog.WarnContext(ctx, "proc: background job failed", slog.Any("args", req.Args), slog.String("error", err.Error()))
		}
	}()
	return nil
}

func (s *service) status(req StatusRequest, reply *StatusReply) error {
	if req.Magic != StatusMagic {
		return nil
	}
	if req.Protocol != "" && req.Protocol != ProtocolV2 {
		return ErrProtocolMismatch
	}
	reply.ParentPID = os.Getpid()
	reply.DaemonVersion = s.version
	if s.workspaceLister != nil {
		reply.Mode = "daemon"
	} else {
		reply.Mode = "proc"
	}
	snap := s.lim.Snapshot()
	reply.Capacity, reply.Running, reply.Queued = snap.Capacity, snap.Running, snap.Queued
	s.calls.Range(func(_, v any) bool {
		c, ok := v.(*activeCall)
		if !ok {
			return true
		}
		e := c.Call
		e.SubOp = c.SubOp.Load()
		reply.Calls = append(reply.Calls, e)
		return true
	})
	slices.SortFunc(reply.Calls, func(a, b Call) int {
		return a.StartedAt.Compare(b.StartedAt)
	})
	if s.workspaceLister != nil {
		reply.Workspaces = s.workspaceLister()
	}
	if s.serviceLister != nil {
		reply.Services = s.serviceLister()
	}
	return nil
}

func (s *service) shutdown(req ShutdownRequest, _ *ShutdownReply) error {
	if req.Magic != ShutdownMagic {
		return nil
	}
	if req.Protocol != "" && req.Protocol != ProtocolV2 {
		return ErrProtocolMismatch
	}
	if s.shutdownFn != nil {
		go s.shutdownFn()
	}
	return nil
}

func cycleKey(root, cwd string, args []string) string {
	scope := root // when empty (old clients), cwd disambiguates workspaces
	if scope == "" {
		scope = cwd
	}
	return scope + "\x00" + cwd + "\x00" + strings.Join(args, "\x00")
}
