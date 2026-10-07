package auth

import (
	"crypto/subtle"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/types"
)

// Verify authenticates presented on the server's LOOPBACK listener and returns the credential
// it is, grant included. It routes by kind, so each store is consulted only for its own
// kind: mgo_ against the operator file, mgs_ against the token store, and mgl_ and mgx_
// refused outright, because a share token authenticates only on its own listener and an
// exchange code is never a bearer. It does no authorization; the caller compares the Grant
// with its route's Need.
//
// It answers from an in-memory view of both stores, so a request costs three stat calls and
// no file read, however many come. The view is reloaded when the operator file, tokens.d, or
// the state directory holding them changes on disk, and at least every revalidateEvery
// regardless, which catches an edit that rewrites a token file in place. So a rotate, mint or
// revoke takes effect without a restart, and a load error fails closed. A store that fails to
// load (a retired connectors.d, say) refuses stored tokens only: the operator token never
// opens it.
func Verify(presented string) (types.Credential, bool) {
	kind, ok := credentialKind(presented)
	if !ok {
		return types.Credential{}, false
	}
	switch kind {
	case types.KindOperator:
		v := currentView()
		if v.operator == "" {
			return types.Credential{}, false
		}
		if subtle.ConstantTimeCompare([]byte(digest(presented)), []byte(digest(v.operator))) != 1 {
			return types.Credential{}, false
		}
		return operatorCredential(v.operator), true
	case types.KindStored:
		v := currentView()
		if !v.storeOK {
			return types.Credential{}, false
		}
		t, ok := lookup(v.tokens, presented, types.KindStored, time.Now())
		if !ok {
			return types.Credential{}, false
		}
		return t.Credential(), true
	}
	return types.Credential{}, false
}

// revalidateEvery bounds how long a view is trusted while nothing it stats has changed.
var revalidateEvery = time.Second

// view is what Verify reads: the operator token ("" when the file did not load) and the
// stored tokens, as of the stat signature sig.
type view struct {
	state    string
	sig      string
	loaded   time.Time
	operator string
	tokens   []Token
	storeOK  bool
}

var (
	viewMu sync.Mutex
	cached view
	// viewLoads counts the views Verify loaded from disk.
	viewLoads int
)

// currentView returns the cached view, reloading it when the state directory moved (a test
// or a changed XDG_STATE_HOME), its signature changed, or it is older than revalidateEvery.
func currentView() view {
	state, err := StateDir()
	if err != nil {
		return view{}
	}
	sig := statSignature(state)
	viewMu.Lock()
	defer viewMu.Unlock()
	if cached.state == state && cached.sig == sig && time.Since(cached.loaded) < revalidateEvery {
		return cached
	}
	viewLoads++
	// The signature is taken before the load, so a change landing mid-load leaves a view
	// whose signature no longer matches, and the next call loads again.
	v := view{state: state, sig: sig, loaded: time.Now()}
	if tok, err := LoadOperator(); err == nil {
		v.operator = tok
	}
	if store, err := LoadStore(filepath.Join(state, "tokens.d")); err == nil {
		st := store.state()
		st.mu.Lock()
		v.tokens = slices.Clone(st.tokens)
		st.mu.Unlock()
		v.storeOK = true
	}
	cached = v
	return v
}

// statSignature stats the state directory (a retired connectors.d appearing changes it), the
// operator file, and tokens.d (a mint links a file in and a revoke unlinks one, both of which
// change it). It opens nothing.
func statSignature(state string) string {
	var b strings.Builder
	for _, p := range []string{state, filepath.Join(state, "mcp_token"), filepath.Join(state, "tokens.d")} {
		info, err := os.Stat(p)
		if err != nil {
			b.WriteString("|-")
			continue
		}
		fmt.Fprintf(&b, "|%d:%d:%o", info.Size(), info.ModTime().UnixNano(), info.Mode())
	}
	return b.String()
}
