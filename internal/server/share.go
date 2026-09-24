package server

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"connectrpc.com/connect"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/handler"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/share"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// shareResponse is the JSON the share endpoint returns to the console: the link
// (with the token in its fragment) a device loads, when it dies, and whether
// starting it revoked a previous active share.
type shareResponse struct {
	URL        string `json:"url"`
	ExpiresAt  string `json:"expires_at"` // RFC 3339 UTC
	Superseded bool   `json:"superseded"`
}

// shareRequest is the optional JSON body: the lifetime the operator picked in the
// console before minting. Zero or absent means the default; a value outside
// [auth.MinShareTTL, auth.MaxShareTTL] is refused, not clamped.
type shareRequest struct {
	TTLSeconds int `json:"ttl_seconds"`
}

// newShareHandler returns the POST /api/v1/share handler. It is mounted on the
// loopback listener behind RequireLoopbackPeer and the console=write bearer guard,
// so only the local, already-authenticated console can trigger a share. Each POST
// mints a fresh share token within the caller's grant and opens a new LAN listener,
// superseding any active one. consoleDir is the built console served to the phone;
// when it is empty (no build found), the endpoint fails with a clear, actionable
// message rather than opening a listener that would 404 the app. Every mint is recorded to the
// trail under trailDir.
func (s *Server) newShareHandler(mgr *share.Manager, consoleDir string, guarded map[string]share.Route, trailDir string, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			handler.RefuseMethod(w, r, http.MethodPost)
			return
		}
		if consoleDir == "" {
			refuseShare(w, r, rpcerr.Error{
				Code:    connect.CodeFailedPrecondition,
				Reason:  types.ConsoleNotBuilt,
				Message: "the built console was not found, so there is nothing to share; build it with `magus run build console` and try again",
			}, log)
			return
		}
		// The body is optional, so an empty one means the default lifetime; a body that
		// does not parse is refused rather than read as the default.
		handler.LimitRequestBody(w, r)
		var req shareRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			refuseShare(w, r, rpcerr.Error{
				Code:    connect.CodeInvalidArgument,
				Reason:  types.ShareRequestMalformed,
				Message: "the share request body is not JSON of the form {\"ttl_seconds\": N}: " + err.Error(),
			}, log)
			return
		}
		minter := trail.CredentialFromContext(r.Context())
		link, err := mgr.Start(minter.Grant, consoleDir, guarded, time.Duration(req.TTLSeconds)*time.Second)
		switch {
		case errors.Is(err, auth.ErrExceedsGrant):
			refuseShare(w, r, rpcerr.Error{Code: connect.CodePermissionDenied, Reason: types.GrantInsufficient, Message: err.Error()}, log)
			return
		case errors.Is(err, auth.ErrShareLifetime):
			refuseShare(w, r, rpcerr.Error{Code: connect.CodeInvalidArgument, Reason: types.TokenLifetimeOutOfRange, Message: err.Error()}, log)
			return
		case err != nil:
			// A missing LAN interface (the common case) is a client-actionable
			// condition, not a server fault: report it as 503 with the guidance
			// share.SelectLANIPv4 already put in the message.
			refuseShare(w, r, rpcerr.Error{
				Code:    connect.CodeUnavailable,
				Reason:  types.ShareUnavailable,
				Message: err.Error(),
			}, log)
			return
		}
		trail.AppendMint(r.Context(), trailDir, "share.mint", trail.MintRecord{Minted: link.Credential, Expires: link.ExpiresAt, Minter: minter})
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(shareResponse{
			URL:        link.URL,
			ExpiresAt:  link.ExpiresAt.UTC().Format(time.RFC3339),
			Superseded: link.Superseded,
		}); err != nil {
			log.WarnContext(r.Context(), "[SHARE] encode response", slog.String("error", err.Error()))
		}
	})
}

// refuseShare logs a failed share and answers it in the AIP-193 shape the endpoint's guards
// already answer in, so the console reads one error shape from this route.
func refuseShare(w http.ResponseWriter, r *http.Request, e rpcerr.Error, log *slog.Logger) {
	log.WarnContext(r.Context(), "[SHARE] share request failed", slog.String("reason", string(e.Reason)), slog.String("error", e.Message))
	rpcerr.FormatJSON.Write(w, r, e)
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
