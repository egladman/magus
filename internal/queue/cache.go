package queue

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/json"
)

// cacheService is the GitHub Actions cache service's Twirp path under ACTIONS_RESULTS_URL.
const cacheService = "/twirp/github.actions.results.api.v1.CacheService/"

// cacheReads are the only methods a CacheReadProxy forwards. GetCacheEntryDownloadURL is
// the lookup and the download both: it answers whether an entry exists and, when it
// does, a pre-signed blob URL the reader fetches with no credential.
var cacheReads = []string{"GetCacheEntryDownloadURL"}

const (
	maxCacheRequest  = 64 << 10
	maxCacheResponse = 1 << 20
)

// CacheReadProxy serves the GitHub Actions cache service's reads to hooks on loopback,
// forwarding them upstream with the runner's token, which never leaves the process
// holding it. Every other method, a write included, is refused. Hooks authenticate with
// Token, a random stand-in valid only here.
type CacheReadProxy struct {
	URL   string // ends in "/", as ACTIONS_RESULTS_URL does
	Token string

	upstream string
	token    string
	client   *http.Client
	log      *HookLog
	srv      *http.Server
}

// StartCacheReadProxy listens on an ephemeral loopback port and serves until Close.
// upstream and token are the runner's ACTIONS_RESULTS_URL and ACTIONS_RUNTIME_TOKEN.
// Each refused request is a line on log; a nil log discards.
func StartCacheReadProxy(upstream, token string, log *HookLog) (*CacheReadProxy, error) {
	if upstream == "" || token == "" {
		return nil, errors.New("the cache read proxy needs the cache service URL and its token")
	}
	stand := make([]byte, 32)
	if _, err := rand.Read(stand); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("the cache read proxy: %w", err)
	}
	p := &CacheReadProxy{
		URL:      "http://" + ln.Addr().String() + "/",
		Token:    hex.EncodeToString(stand),
		upstream: strings.TrimSuffix(upstream, "/"),
		token:    token,
		// A lookup is one POST; following a redirect would carry it somewhere unnamed.
		client: &http.Client{
			Timeout:       time.Minute,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		log: log,
	}
	p.srv = &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = p.srv.Serve(ln) }()
	return p, nil
}

// Env is what a hook's environment takes to read through the proxy: its URL and the
// stand-in token, under the names the runner gives the real ones.
func (p *CacheReadProxy) Env() []string {
	return []string{"ACTIONS_RESULTS_URL=" + p.URL, "ACTIONS_RUNTIME_TOKEN=" + p.Token}
}

// Close stops serving; a request in flight is cut off.
func (p *CacheReadProxy) Close() error { return p.srv.Close() }

// ServeHTTP answers one hook request, as a Twirp error when it is refused.
func (p *CacheReadProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Loopback is open to every process on the machine; the stand-in is handed only to
	// hooks, so the port alone reads nothing.
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+p.Token)) != 1 {
		p.refuse(w, r, http.StatusUnauthorized, "unauthenticated", "not the token this proxy handed its hooks")
		return
	}
	method, ok := strings.CutPrefix(r.URL.Path, cacheService)
	allowed := slices.Index(cacheReads, method)
	if !ok || allowed < 0 || r.URL.RawQuery != "" {
		p.refuse(w, r, http.StatusForbidden, "permission_denied",
			"the queue's cache proxy forwards only "+strings.Join(cacheReads, ", ")+", and validation never writes the cache")
		return
	}
	if r.Method != http.MethodPost {
		p.refuse(w, r, http.StatusMethodNotAllowed, "bad_route", "Twirp takes POST")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCacheRequest))
	if err != nil {
		p.refuse(w, r, http.StatusRequestEntityTooLarge, "invalid_argument", err.Error())
		return
	}
	// Built from the allowlist rather than the request, so no byte of the path reaches upstream.
	up, err := http.NewRequestWithContext(r.Context(), http.MethodPost, p.upstream+cacheService+cacheReads[allowed], bytes.NewReader(body))
	if err != nil {
		twirpError(w, http.StatusBadGateway, "internal", err.Error())
		return
	}
	up.Header.Set("Authorization", "Bearer "+p.token)
	for _, h := range []string{"Content-Type", "Accept"} {
		if v := r.Header.Get(h); v != "" {
			up.Header.Set(h, v)
		}
	}
	res, err := p.client.Do(up)
	if err != nil {
		// A client error names the URL, never a header, so the token stays here.
		twirpError(w, http.StatusBadGateway, "unavailable", err.Error())
		return
	}
	defer func() { _ = res.Body.Close() }()
	if ct := res.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(res.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(res.Body, maxCacheResponse))
}

func (p *CacheReadProxy) refuse(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	out := p.log.Prefixed("[remote cache read] ")
	_, _ = fmt.Fprintf(out, "refused %s %s: %s\n", r.Method, r.URL.Path, msg)
	_ = out.Close()
	twirpError(w, status, code, msg)
}

func twirpError(w http.ResponseWriter, status int, code, msg string) {
	b, _ := json.Marshal(map[string]string{"code": code, "msg": msg})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
