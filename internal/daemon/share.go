package daemon

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/egladman/magus/internal/share"
)

// shareResponse is the JSON the share endpoint returns to the console: the link
// (with the token in its fragment) a phone loads, when it dies, and whether
// starting it revoked a previous active share.
type shareResponse struct {
	URL        string `json:"url"`
	ExpiresAt  string `json:"expires_at"` // RFC 3339 UTC
	Superseded bool   `json:"superseded"`
}

// shareError is the JSON error body the console toasts on a failed share.
type shareError struct {
	Error string `json:"error"`
}

// newShareHandler returns the POST /api/v1/share handler. It is mounted on the
// loopback listener behind RequireLoopbackPeer + the cli/connector bearer guard,
// so only the local, already-authenticated console can trigger a share. Each POST
// mints a fresh read-only token and opens a new LAN listener, superseding any
// active one. consoleDir is the built console served to the phone; when it is
// empty (no build found), the endpoint fails with a clear, actionable message
// rather than opening a listener that would 404 the app.
func (s *Daemon) newShareHandler(mgr *share.Manager, consoleDir string, guarded map[string]http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeShareError(w, http.StatusMethodNotAllowed, "share requires POST", log)
			return
		}
		if consoleDir == "" {
			writeShareError(w, http.StatusServiceUnavailable,
				"the built console was not found, so there is nothing to serve to your phone; build it with `magus run build console` and try again", log)
			return
		}
		sess, err := mgr.Start(consoleDir, guarded)
		if err != nil {
			// A missing LAN interface (the common case) is a client-actionable
			// condition, not a server fault: report it as 503 with the guidance
			// share.SelectLANIPv4 already put in the message.
			writeShareError(w, http.StatusServiceUnavailable, err.Error(), log)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(shareResponse{
			URL:        sess.URL,
			ExpiresAt:  sess.ExpiresAt.UTC().Format(time.RFC3339),
			Superseded: sess.Superseded,
		}); err != nil {
			log.Warn("[SHARE] encode response", slog.String("error", err.Error()))
		}
	})
}

// writeShareError writes a JSON {error} body with the given status and logs it.
func writeShareError(w http.ResponseWriter, status int, msg string, log *slog.Logger) {
	log.Warn("[SHARE] share request failed", slog.Int("status", status), slog.String("error", msg))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(shareError{Error: msg})
}

// resolveConsoleDir locates the built console the LAN share serves. It honors an
// explicit MAGUS_CONSOLE_DIR override (for a non-dogfood install), else falls
// back to <root>/console/gen (the repo's own build output). It returns ok=false
// when no directory containing an index.html is found, so the share endpoint can
// fail with a build hint rather than serving an empty app.
func resolveConsoleDir(root string) (string, bool) {
	var candidates []string
	if env := os.Getenv("MAGUS_CONSOLE_DIR"); env != "" {
		candidates = append(candidates, env)
	}
	if root != "" {
		candidates = append(candidates, filepath.Join(root, "console", "gen"))
	}
	for _, dir := range candidates {
		if fi, err := os.Stat(filepath.Join(dir, "index.html")); err == nil && !fi.IsDir() {
			return dir, true
		}
	}
	return "", false
}
