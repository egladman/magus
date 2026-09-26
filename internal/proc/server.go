package proc

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/proc/endpoint"
	"github.com/egladman/magus/internal/proc/environ"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/types"
)

// maxArgs caps runRequest.Args to prevent OOM from untrusted callers.
const maxArgs = 256

// handshakeTimeout bounds how long an accepted connection may take to deliver its request
// headers. Without it a client that connects and never writes holds a connection open
// forever, blocking Server.Close, and letting any local caller DoS the socket.
const handshakeTimeout = 30 * time.Second

type contextKey int

const (
	rootCtxKey contextKey = iota
	cwdCtxKey
	jobCtxKey
	leaseCtxKey
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

// WithLease returns ctx carrying the client's lease, readable via
// LeaseFromContext. An id failing [types.ValidJobID] (including the empty
// string a client that predates the field sends) stores nothing, so a reader sees "".
//
// The validation is here rather than at the call site because the value crosses a socket
// any local process may dial: a lease id is exempt from the trail's redaction, so
// storing an unvalidated one would let the wire carry a credential onto an event line.
// Dropping it matches what trail.LeaseFromEnv does with a malformed environment value.
func WithLease(ctx context.Context, lease string) context.Context {
	if !types.ValidJobID(lease) {
		return ctx
	}
	return context.WithValue(ctx, leaseCtxKey, lease)
}

// LeaseFromContext returns the lease the adopted client was launched under, or
// "" when it claimed none or claimed one that failed validation.
//
// A caller that also reads the environment channel must prefer this: it is the lease
// of the process that ASKED for the run, while the server's own environment describes
// whoever happened to start the server.
func LeaseFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(leaseCtxKey).(string); ok {
		return v
	}
	return ""
}

type sandboxFloorKey struct{}

// WithSandboxFloor marks ctx as belonging to a run that must stay sandboxed at mode or
// stronger. On a client it makes [Forward] say so; on the server it is what the client
// said, and the handler declines the run (ErrNotAdoptable) rather than execute it with
// less than the client had.
func WithSandboxFloor(ctx context.Context, mode types.SandboxMode) context.Context {
	return context.WithValue(ctx, sandboxFloorKey{}, mode.Resolved())
}

// SandboxFloorFromContext returns the mode [WithSandboxFloor] set, or off when unset.
func SandboxFloorFromContext(ctx context.Context) types.SandboxMode {
	if v, ok := ctx.Value(sandboxFloorKey{}).(types.SandboxMode); ok {
		return v
	}
	return types.SandboxModeOff
}

// withJob marks ctx as a background job invocation (submitJob), distinct from an adopted run.
// The server's handler reads it via IsJob to route jobs through the full command set while a
// plain adopted run stays limited to run/affected.
func withJob(ctx context.Context) context.Context {
	return context.WithValue(ctx, jobCtxKey, true)
}

// IsJob reports whether ctx belongs to a background job (submitted via SubmitJob) rather than
// an adopted run. The server's dispatch handler branches on it.
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
	WorkspaceLister func() []Workspace                             // optional; used by the server's Status RPC
	// OnJobDone, if set, is called after every BACKGROUND job (submitJob) completes, never for
	// an adopted foreground run, with the job's args, wall-clock duration, and outcome. The
	// ctx still carries Root/Cwd. The server uses it to record a KIND_JOB activity event; proc
	// stays decoupled from the trail and cache layout.
	OnJobDone func(ctx context.Context, args []string, dur time.Duration, err error)
	// ConfigReloader, if set, drops the workspaces the server is holding open so the next
	// command against each reopens it and re-reads its config. It reports how many were
	// dropped and how many were left alone as busy. Only the server sets it; a per-process
	// proc server holds one workspace for one invocation and has nothing to reload.
	ConfigReloader func() (dropped, busy int)
	// Server, if set, is read on every Status RPC and reported as StatusReply.Server.
	// Only `magus server` sets it.
	Server func() *types.StatusServer
	// CallerOwnsSignals leaves SIGINT, SIGTERM and SIGHUP to the caller. Without it Start
	// installs a handler that shuts the server down on any of them and re-raises the
	// signal, which is right for a proc server living inside one run and wrong for a
	// process that reloads on SIGHUP.
	CallerOwnsSignals bool
}

// Server serves HTTP on a Unix-domain socket: the proc routes under /proc/v1/ that child
// processes forward work through, and whatever [Server.Mount] adds beside them.
//
// Where the platform reports a connection's peer uid, every request must come from a process
// running as this one's user, and is admitted as [types.CredentialSocketPeer] and held to its
// route's Need. Elsewhere the socket's private directory is the only boundary, as it always
// was for these routes, and Mount refuses: nothing that needs a credential rides a socket that
// cannot name its caller.
type Server struct {
	ep   endpoint.Endpoint
	svc  *service
	http *http.Server
	// token is the secret every /proc/ request must carry; see [TokenEnv].
	token string
	// peerChecked is whether requests pass the peer-uid guard; see Server.
	peerChecked bool
	// extra is what Mount installed for the paths outside /proc/, nil for none.
	extra atomic.Pointer[http.Handler]
	// mu guards listener. Start assigns it and Close reads it, and those run on different
	// goroutines: an RPC shutdown dispatches Close from a handler while Serve is live.
	mu       sync.Mutex
	listener net.Listener
	cancel   context.CancelFunc
	once     sync.Once
	done     chan struct{} // closed when Close begins, to stop the signal watcher goroutine
	closed   chan struct{} // closed when Close has drained every in-flight request

	callerOwnsSignals bool
}

// Addr returns the canonical unix:// URL that children dial. Valid after New.
func (s *Server) Addr() string { return s.ep.String() }

// Token returns the secret a client presents on every /proc/ request. A process hands it
// to the nested magus it spawns through [TokenEnv], and to nothing else.
func (s *Server) Token() string { return s.token }

// Done returns a channel closed when the server has been Closed, whether by an RPC
// shutdown request or a signal. A blocking server loop selects on it so an RPC-driven
// `magus server stop` unblocks the process the same way a signal does: without it the
// shutdown handler tears down the listener but the process keeps running, since the
// listener's context is a sibling of the process context, not its parent.
func (s *Server) Done() <-chan struct{} { return s.done }

// Mount serves h for every path on the socket outside /proc/, each request carrying the
// socket peer's credential, until the returned unmount runs. A later Mount replaces an
// earlier one; an unmount whose handler was already replaced does nothing. Where the
// platform cannot name a connection's peer it installs nothing and returns
// [httpx.ErrPeerCredentialsUnsupported].
func (s *Server) Mount(h http.Handler) (unmount func(), err error) {
	if !s.peerChecked {
		return nil, httpx.ErrPeerCredentialsUnsupported
	}
	box := &h
	s.extra.Store(box)
	return func() { s.extra.CompareAndSwap(box, nil) }, nil
}

// Close stops accepting, cancels the server's work, waits for every in-flight request, and
// removes the socket file. Safe to call multiple times and concurrently; every caller
// returns once the drain is done.
func (s *Server) Close() {
	s.once.Do(func() {
		close(s.done) // unblocks watchSignals before we cancel/close
		s.cancel()
		s.mu.Lock()
		ln := s.listener
		s.listener = nil
		s.mu.Unlock()
		if ln != nil {
			_ = ln.Close()
		}
		// Shutdown waits for handlers, and each one runs on the context cancelled above.
		_ = s.http.Shutdown(context.Background())
		_ = os.Remove(s.ep.Addr)
		_ = os.Remove(tokenPath(s.ep.Addr))
		close(s.closed)
	})
	<-s.closed
}

// New constructs an unstarted Server; returns ErrAlreadyAdopted when MAGUS_PROC_SOCKET is set.
// Call Start to bind the socket.
func New(opts Options) (*Server, error) {
	// Name the culprit in the error. This guard refuses to host a second proc server when
	// MAGUS_PROC_SOCKET is set (a nested process must forward to the parent's pool, not open
	// its own socket). Surfacing the value turns an opaque "already adopted" (which reads as a
	// mystery to anyone whose environment merely inherited the var) into an actionable one.
	if sock := os.Getenv(SocketEnv); sock != "" {
		return nil, fmt.Errorf("%w (%s=%s)", ErrAlreadyAdopted, SocketEnv, sock)
	}

	var ep endpoint.Endpoint
	if opts.Address != "" {
		var err error
		ep, err = endpoint.Parse(opts.Address)
		if err != nil {
			return nil, fmt.Errorf("proc: invalid address: %w", err)
		}
	} else {
		rnd := make([]byte, 4)
		if _, err := rand.Read(rnd); err != nil {
			return nil, fmt.Errorf("proc: random bytes: %w", err)
		}
		sockName := fmt.Sprintf("magus-%d-%s.sock", os.Getpid(), hex.EncodeToString(rnd))
		ep = endpoint.Endpoint{Scheme: "unix", Addr: filepath.Join(SockDir(), sockName)}
	}

	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("proc: random bytes: %w", err)
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
		configReloader:  opts.ConfigReloader,
		parentCtx:       serverCtx,
		lim:             lim,
		version:         opts.Version,
		gateVersion:     adoptionIdentity(opts.Version),
		workspaceLister: opts.WorkspaceLister,
		serverInfo:      opts.Server,
		onJobDone:       opts.OnJobDone,
	}
	srv := &Server{
		ep:          ep,
		svc:         svc,
		token:       hex.EncodeToString(secret),
		peerChecked: httpx.PeerCredentialsSupported(),
		cancel:      cancel,
		done:        make(chan struct{}),
		closed:      make(chan struct{}),

		callerOwnsSignals: opts.CallerOwnsSignals,
	}
	handler, err := srv.routes()
	if err != nil {
		cancel()
		return nil, err
	}
	// No ReadTimeout: its deadline outlives the body and would cancel a request mid-stream,
	// and a forwarded run holds its request, an MCP or Connect stream its response, for as
	// long as the work takes.
	//
	// Every request's context derives from the server's, so Close, which cancels it before it
	// drains, ends an open stream instead of waiting on a client that will never hang up.
	srv.http = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: handshakeTimeout,
		BaseContext:       func(net.Listener) context.Context { return serverCtx },
	}
	if srv.peerChecked {
		srv.http.ConnContext = httpx.PeerConnContext
	}
	svc.shutdownFn = srv.Close
	return srv, nil
}

// routes is the socket's whole handler: the proc routes, held to routeNeeds, a 404 for any
// other /proc/ path, and the mounted handler for the rest, all behind the peer guard where
// the platform has one.
func (s *Server) routes() (http.Handler, error) {
	proc := http.NewServeMux()
	proc.HandleFunc("POST "+pathRun, s.svc.handleRun)
	proc.HandleFunc("POST "+pathJobs, s.svc.handleJob)
	proc.HandleFunc("GET "+pathStatus, s.svc.handleStatus)
	proc.HandleFunc("POST "+pathShutdown, s.svc.handleShutdown)
	proc.HandleFunc("POST "+pathReload, s.svc.handleReload)
	proc.HandleFunc("/proc/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, errorReply{Message: fmt.Sprintf("proc: no route %s %s", r.Method, r.URL.Path)})
	})

	mux := http.NewServeMux()
	if s.peerChecked {
		guarded, err := httpx.GrantGuard(rpcerr.FormatJSON, routeNeeds, proc)
		if err != nil {
			return nil, fmt.Errorf("proc: %w", err)
		}
		mux.Handle("/proc/", s.requireToken(guarded))
	} else {
		mux.Handle("/proc/", s.requireToken(proc))
	}
	mux.HandleFunc("/", s.serveExtra)
	if !s.peerChecked {
		return mux, nil
	}
	return httpx.PeerGuard(rpcerr.FormatJSON, os.Getuid(), types.CredentialSocketPeer, mux), nil
}

// requireToken refuses a /proc/ request that does not carry this server's token.
//
// The peer check admits every process of this user, and landlock does not govern
// connect(2) on a pathname socket, so a confined child that finds the socket could post a
// run its parent then executes outside the child's sandbox. The token is what that child
// lacks: it rides only to a nested magus (see [TokenEnv]) and in a 0600 file in the
// private socket directory, which the sandbox does not grant.
func (s *Server) requireToken(next http.Handler) http.Handler {
	want := []byte(s.token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get(tokenHeader)), want) != 1 {
			writeJSON(w, http.StatusUnauthorized, errorReply{Message: ErrTokenRefused.Error()})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) serveExtra(w http.ResponseWriter, r *http.Request) {
	if h := s.extra.Load(); h != nil {
		(*h).ServeHTTP(w, r)
		return
	}
	writeJSON(w, http.StatusNotFound, errorReply{Message: fmt.Sprintf("proc: nothing is served at %s on this socket", r.URL.Path)})
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
				_ = os.Remove(tokenPath(s.ep.Addr))
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
	if err := writeToken(s.ep.Addr, s.token); err != nil {
		_ = ln.Close()
		s.cancel()
		return fmt.Errorf("proc: %w", err)
	}
	s.mu.Lock()
	s.listener = ln
	s.mu.Unlock()

	go func() { _ = s.http.Serve(ln) }()
	if !s.callerOwnsSignals {
		watchSignals(s, s.cancel)
	}
	return nil
}

// tokenPath is where the token for the socket at sock is kept: beside it, in the same
// private directory, so a client that found the socket by discovery can present it.
func tokenPath(sock string) string { return sock + ".token" }

// writeToken publishes token for the socket at sock, readable by this user alone. A file
// left by a server that crashed is replaced; O_EXCL after the remove refuses to follow
// anything planted at the path in between.
func writeToken(sock, token string) error {
	path := tokenPath(sock)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("replace token %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("write token: %w", err)
	}
	_, werr := f.WriteString(token)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("write token %s: %w", path, werr)
	}
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

func (s *service) handleRun(w http.ResponseWriter, r *http.Request) {
	var req runRequest
	if err := decodeBody(w, r, &req); err != nil {
		refuse(w, http.StatusBadRequest, err)
		return
	}
	var reply runReply
	if err := s.run(req, &reply); err != nil {
		refuse(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, reply)
}

func (s *service) handleJob(w http.ResponseWriter, r *http.Request) {
	var req jobRequest
	if err := decodeBody(w, r, &req); err != nil {
		refuse(w, http.StatusBadRequest, err)
		return
	}
	var reply jobReply
	if err := s.submitJob(req, &reply); err != nil {
		refuse(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusAccepted, reply)
}

func (s *service) handleStatus(w http.ResponseWriter, _ *http.Request) {
	var reply StatusReply
	s.status(&reply)
	writeJSON(w, http.StatusOK, reply)
}

func (s *service) handleShutdown(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusAccepted, struct{}{})
	if s.shutdownFn != nil {
		// Close drains in-flight requests, this one included, so it cannot run inline.
		go s.shutdownFn()
	}
}

func (s *service) handleReload(w http.ResponseWriter, _ *http.Request) {
	var reply configReloadReply
	if s.configReloader != nil {
		reply.Dropped, reply.Busy = s.configReloader()
	}
	writeJSON(w, http.StatusOK, reply)
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
	version         string // human-facing display version; surfaced as StatusReply.Version
	gateVersion     string // adoption identity for the version gate (see adoptionIdentity); "" disables the gate
	workspaceLister func() []Workspace
	serverInfo      func() *types.StatusServer
	configReloader  func() (dropped, busy int)
	onJobDone       func(ctx context.Context, args []string, dur time.Duration, err error)
	inflight        sync.Map // cycleKey → struct{}, for cycle detection
	calls           sync.Map // uint64 id → *activeCall, for Status reporting
	nextID          atomic.Uint64
	shutdownFn      func() // called by the shutdown route; set by New to srv.Close
}

// versionAdmits reports whether a request carrying reqVersion may be adopted by this
// server. It is the single gate shared by run and submitJob: both the client's reqVersion
// and the server's gateVersion are adoption identities (see adoptionIdentity), so a match
// means the two builds are provably the same code. An empty identity on EITHER side
// disables the check: the "" escape hatch for test injection and pre-versioning clients.
func (s *service) versionAdmits(reqVersion string) bool {
	return s.gateVersion == "" || reqVersion == "" || reqVersion == s.gateVersion
}

// admitWork refuses a request to run magus that this server must not execute: one over the
// argument limit, or one from a different build.
func (s *service) admitWork(request string, args []string, version string) error {
	if len(args) > maxArgs {
		return fmt.Errorf("proc: %s.Args exceeds limit (%d > %d)", request, len(args), maxArgs)
	}
	if !s.versionAdmits(version) {
		return ErrVersionMismatch
	}
	return nil
}

// trackCall adds a pool entry for work this server is running, so status and the Dashboard
// see it. The caller runs untrack when the work ends.
func (s *service) trackCall(args []string, workspace, inv string) (call *activeCall, untrack func()) {
	id := s.nextID.Add(1)
	call = &activeCall{
		Call:  Call{Args: args, Workspace: workspace, StartedAt: time.Now(), Inv: inv},
		SubOp: &SubOp{},
	}
	s.calls.Store(id, call)
	return call, func() { s.calls.Delete(id) }
}

func (s *service) run(req runRequest, reply *runReply) error {
	if err := s.admitWork("runRequest", req.Args, req.Version); err != nil {
		return err
	}

	// The server's context, not the request's: a client that disconnects mid-run does not
	// cancel the work it started.
	ctx, cancel := context.WithCancel(s.parentCtx)
	defer cancel()

	ctx = WithRoot(ctx, req.Root)
	ctx = WithCwd(ctx, req.Cwd)
	ctx = WithLease(ctx, req.Lease)
	// A run of its own, so its env\set reaches no other run this server holds.
	ctx = environ.With(ctx)
	if req.Sandbox.Enabled() {
		ctx = WithSandboxFloor(ctx, req.Sandbox)
	}
	// Adopt the client's ancestry (BeginInvocation appends the id minted below), so a run
	// this server executes for a nested client recognizes the lock it holds for that
	// client's parent as its own ancestor's rather than waiting on itself forever.
	ctx = types.WithInvocationAncestors(ctx, req.Ancestors)

	key := cycleKey(req.Root, req.Cwd, req.Args)
	if _, loaded := s.inflight.LoadOrStore(key, struct{}{}); loaded {
		reply.ExitCode = 1
		reply.Err = ErrCycleDetected.Error()
		return nil
	}
	defer s.inflight.Delete(key)

	// Mint the invocation id here, before dispatch, and thread it onto ctx so the adopted
	// run's BeginInvocation reuses it (rather than minting its own). That lets this pool
	// entry carry its inv: the key a dashboard uses to deep-link into the run's live log.
	inv := journal.NewInvocationID()
	ctx = journal.WithInvocationID(ctx, inv)

	call, untrack := s.trackCall(req.Args, req.Root, inv)
	defer untrack()
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
	// reacquire before returning (mirrors RunChildSync's Yield).
	if err := s.lim.Yield(ctx, func() error { return s.handler(ctx, req.Args) }); err != nil {
		if errors.Is(err, ErrNotAdoptable) { // propagate so client falls back to local execution
			return err
		}
		var exitErr types.ExitError
		if errors.As(err, &exitErr) {
			// os.exit(code) from a magusfile: honor the code and say nothing. The message
			// is incidental (types.ExitError says so), the magusfile printed whatever it
			// wanted before exiting, and the local path prints nothing here either, so
			// sending text the client would render as an error is a difference between
			// adopted and local runs, not information.
			reply.ExitCode = exitErr.Code
			return nil
		}
		reply.ExitCode = 1
		// A failure that names its own status keeps it. Collapsing everything to 1 made
		// the documented split (1 the work failed, 2 the invocation was wrong) depend on
		// whether a server happened to be running.
		if code, ok := ExitCode(err); ok {
			reply.ExitCode = code
		}
		// Withheld when the handler already explained the failure: the client PRINTS this
		// text, so sending a sentinel's placeholder would report "silent exit" as the
		// reason a run failed.
		if !AlreadyReported(err) {
			reply.Err = err.Error()
		}
		return nil
	}
	reply.ExitCode = 0
	return nil
}

// submitJob accepts a fire-and-forget background job: it registers the invocation (so it
// shows in the Dashboard like an adopted run), spawns the handler on the server's
// long-lived context, and returns immediately, unlike run, which blocks until done. A
// duplicate job already in flight (same workspace + args) is coalesced: no second run
// starts, so a rapid series of checkouts collapses to one refresh. The job's own
// success/failure is observed via the Dashboard/logs, not the reply.
func (s *service) submitJob(req jobRequest, reply *jobReply) error {
	if err := s.admitWork("jobRequest", req.Args, req.Version); err != nil {
		return err
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
	// server resolves it from Cwd), fall back to Cwd so the label is never blank.
	workspace := req.Root
	if workspace == "" {
		workspace = req.Cwd
	}
	inv := journal.NewInvocationID()
	call, untrack := s.trackCall(req.Args, workspace, inv)
	reply.Inv = inv

	// Run on the server's context, not the request's: the job must outlive the round-trip
	// that submitted it.
	go func() {
		defer s.inflight.Delete(key)
		defer untrack()

		ctx, cancel := context.WithCancel(s.parentCtx)
		defer cancel()
		ctx = WithRoot(ctx, req.Root)
		ctx = WithCwd(ctx, req.Cwd)
		ctx = journal.WithInvocationID(ctx, inv)
		ctx = WithSubOp(ctx, call.SubOp)
		ctx = environ.With(ctx)
		ctx = withJob(ctx) // route through the full job command set, not the run/affected adoption allowlist
		// A job descends from nobody. parentCtx carries whatever ancestry the SERVER's
		// process environment had (which is a real value when the server was started from
		// inside a magus target), and inheriting it would attribute this job's locks to a
		// stranger, and tell every process it forks that it descends from one.
		ctx = types.WithInvocationAncestors(ctx, nil)

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

func (s *service) status(reply *StatusReply) {
	reply.ParentPID = os.Getpid()
	reply.Version = s.version
	snap := s.lim.Snapshot()
	reply.Capacity, reply.Running, reply.Queued = snap.Capacity, snap.Running, snap.Queued
	if s.serverInfo != nil {
		reply.Server = s.serverInfo()
	}
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
}

func cycleKey(root, cwd string, args []string) string {
	scope := root // when empty (old clients), cwd disambiguates workspaces
	if scope == "" {
		scope = cwd
	}
	return scope + "\x00" + cwd + "\x00" + strings.Join(args, "\x00")
}
