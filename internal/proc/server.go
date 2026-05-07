package proc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
)

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

// Options configures the proc server created by New.
type Options struct {
	Handler         func(ctx context.Context, args []string) error // required; ctx carries Root/Cwd
	Context         context.Context                                // nil → context.Background
	Limiter         *cache.Limiter                                 // shared budget; nil → private limiter
	Concurrency     int                                            // ignored when Limiter is set; 0 → default
	Version         string                                         // "" disables version-skew check
	Address         string                                         // "" → auto-generate in SockDir()
	WorkspaceLister func() []Workspace                             // optional; used by daemon Status RPC
}

// Server listens on a Unix-domain socket and accepts forwarded RPC requests from child processes.
type Server struct {
	ep       Endpoint
	listener net.Listener
	svc      *service
	cancel   context.CancelFunc
	once     sync.Once
	connWg   sync.WaitGroup // tracks in-flight handleConn goroutines
	done     chan struct{}  // closed by Close to stop the signal watcher goroutine
}

// Addr returns the canonical unix:// URL that children dial. Valid after New.
func (s *Server) Addr() string { return s.ep.String() }

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

	var ep Endpoint
	if opts.Address != "" {
		var err error
		ep, err = ParseEndpoint(opts.Address)
		if err != nil {
			return nil, fmt.Errorf("proc: invalid address: %w", err)
		}
	} else {
		rnd := make([]byte, 4)
		if _, err := rand.Read(rnd); err != nil {
			return nil, fmt.Errorf("proc: random bytes: %w", err)
		}
		sockName := fmt.Sprintf("magus-%d-%s.sock", os.Getpid(), hex.EncodeToString(rnd))
		ep = Endpoint{Scheme: "unix", Addr: filepath.Join(sockDir(), sockName)}
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
		parentCtx:       serverCtx,
		lim:             lim,
		version:         opts.Version,
		workspaceLister: opts.WorkspaceLister,
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
	_ = writeFrame(conn, typeError, ErrorReply{Message: msg}) //nolint:errcheck
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
		_ = writeFrame(conn, typeRunReply, reply) //nolint:errcheck

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
		_ = writeFrame(conn, typeStatusReply, reply) //nolint:errcheck

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
		_ = writeFrame(conn, typeShutdownReply, reply) //nolint:errcheck

	default:
		writeErr(conn, fmt.Sprintf("proc: unknown frame type %q", typ))
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
	version         string
	workspaceLister func() []Workspace
	inflight        sync.Map // cycleKey → struct{}, for cycle detection
	calls           sync.Map // uint64 id → *activeCall, for Status reporting
	nextID          atomic.Uint64
	shutdownFn      func() // called by shutdown handler; set by New to srv.Close
}

func (s *service) run(req RunRequest, reply *RunReply) error {
	if len(req.Args) > maxArgs {
		return fmt.Errorf("proc: RunRequest.Args exceeds limit (%d > %d)", len(req.Args), maxArgs)
	}
	if req.Protocol != "" && req.Protocol != ProtocolV2 {
		return ErrProtocolSkew
	}
	// Both-empty intentionally passes (test injection / pre-versioning clients).
	if s.version != "" && req.Version != "" && req.Version != s.version {
		return ErrVersionSkew
	}

	ctx, cancel := context.WithCancel(s.parentCtx)
	defer cancel()

	ctx = context.WithValue(ctx, rootCtxKey, req.Root)
	ctx = context.WithValue(ctx, cwdCtxKey, req.Cwd)

	key := cycleKey(req.Root, req.Cwd, req.Args)
	if _, loaded := s.inflight.LoadOrStore(key, struct{}{}); loaded {
		reply.ExitCode = 1
		reply.Err = ErrCycleDetected.Error()
		return nil
	}
	defer s.inflight.Delete(key)

	id := s.nextID.Add(1)
	call := &activeCall{
		Call:  Call{Args: req.Args, Workspace: req.Root, StartedAt: time.Now()},
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

	if err := s.handler(ctx, req.Args); err != nil {
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

func (s *service) status(req StatusRequest, reply *StatusReply) error {
	if req.Magic != StatusMagic {
		return nil
	}
	if req.Protocol != "" && req.Protocol != ProtocolV2 {
		return ErrProtocolSkew
	}
	reply.ParentPID = os.Getpid()
	reply.DaemonVersion = s.version
	if s.workspaceLister != nil {
		reply.Mode = "daemon"
	} else {
		reply.Mode = "proc"
	}
	snap := s.lim.Snapshot()
	reply.Capacity, reply.InUse, reply.Waiting = snap.Capacity, snap.InUse, snap.Waiting
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
	return nil
}

func (s *service) shutdown(req ShutdownRequest, _ *ShutdownReply) error {
	if req.Magic != ShutdownMagic {
		return nil
	}
	if req.Protocol != "" && req.Protocol != ProtocolV2 {
		return ErrProtocolSkew
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
