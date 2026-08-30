package registry

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Size caps on every read. Parsing is the attack surface, so nothing unbounded
// reaches a decoder.
const (
	maxSig      = 1 << 10  // 1 KB
	maxRegistry = 16 << 20 // 16 MB; the eol half grows with the upstream product list
)

// fetchTimeout bounds one refresh including retries.
const fetchTimeout = 60 * time.Second

// Refresh fetches, verifies, and caches one source, returning what is now local.
//
// The order is signature first, then the document. That does NOT let magus verify
// before downloading - a detached Ed25519 signature is computed over the artifact,
// so the artifact has to be in hand - and claiming otherwise would be dishonest.
// What it buys is concrete: a missing signature fails after 1 KB instead of after
// the whole file, which is exactly the shape of the bug that made `magus self
// update` exit 1 for every user, and the document host is not contacted until the
// expected signature is already pinned locally.
//
// The security property is the one below it: verify before parse, verify before
// persist, bounded reads. Nothing here writes to the cache path until the bytes
// have been checked against a key pinned on this machine.
func Refresh(ctx context.Context, src Source, client *http.Client) (Cached, error) {
	if offline() {
		return Cached{Source: src}, fmt.Errorf("%w (wanted %s)", ErrOffline, src.URL)
	}
	if err := requireHTTPS(src.URL); err != nil {
		return Cached{Source: src}, fmt.Errorf("registry: %s: %w", src.Name, err)
	}
	// Resolve the keys BEFORE sending anything. A response this build could not check
	// is one it must not ask for: contacting the host first would leak that this
	// machine runs magus and wants the registry, in exchange for bytes destined for
	// the bin. It also turns "no key is pinned" into a clear refusal rather than a
	// 404 that reads like the server is broken.
	if _, err := src.Keys(); err != nil {
		return Cached{Source: src}, err
	}
	if client == nil {
		client = defaultClient()
	}

	sig, err := fetchLimited(ctx, client, src.URL+".sig", maxSig)
	if err != nil {
		return Cached{Source: src}, fmt.Errorf("registry: %s: signature unreachable (%s.sig): %w", src.Name, src.URL, err)
	}
	data, err := fetchLimited(ctx, client, src.URL, maxRegistry)
	if err != nil {
		return Cached{Source: src}, fmt.Errorf("registry: %s: unreachable (%s): %w", src.Name, src.URL, err)
	}

	reg, err := verify(src, data, sig)
	if err != nil {
		return Cached{Source: src}, err
	}

	dir, err := cacheDir()
	if err != nil {
		return Cached{Source: src}, err
	}
	path, err := store(dir, src.Name, data)
	if err != nil {
		return Cached{Source: src}, err
	}

	age := time.Since(reg.GeneratedAt)
	state := StateFresh
	if age > src.StaleWindow() {
		// Fetched and still stale: the publisher has stopped updating. Saying so is
		// the point of measuring against generated_at rather than against now.
		state = StateStale
	}
	return Cached{Source: src, Registry: reg, State: state, Age: age, Path: path}, nil
}

// defaultClient matches the transport floor the installer demands of curl
// (--proto '=https' --proto-redir '=https' --tlsv1.2), so every path that can
// bring bytes into magus agrees about what it will speak.
func defaultClient() *http.Client {
	return &http.Client{
		Timeout:   fetchTimeout,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if err := requireHTTPS(req.URL.String()); err != nil {
				return fmt.Errorf("refusing redirect: %w", err)
			}
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			return nil
		},
	}
}

// fetchLimited GETs url and reads at most max bytes, refusing a longer body rather
// than truncating it: a silently truncated document would fail signature
// verification anyway, but with an error that blamed the signature.
func fetchLimited(ctx context.Context, client *http.Client, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("larger than the %d byte cap", max)
	}
	return data, nil
}
