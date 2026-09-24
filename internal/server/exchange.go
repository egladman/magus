package server

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/handler"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/rpcerr"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

type exchangeRequest struct {
	Code string `json:"code"`
}

type exchangeResponse struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"` // RFC 3339 UTC
}

// newExchangeHandler returns the POST /api/v1/token/exchange handler, which trades a console
// link's one-time code for the stored token it stands for. The code is the credential, so no
// bearer guard sits in front of it; a code that is wrong, expired or used is 401. Each redeem is
// recorded to the trail under trailDir.
func newExchangeHandler(trailDir string, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			handler.RefuseMethod(w, r, http.MethodPost)
			return
		}
		handler.LimitRequestBody(w, r)
		var req exchangeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Code == "" {
			rpcerr.FormatJSON.Write(w, r, rpcerr.Error{
				Code:    connect.CodeInvalidArgument,
				Reason:  types.TokenRequestInvalid,
				Message: "the exchange body is not JSON of the form {\"code\": \"mgx_...\"}",
			})
			return
		}
		secret, tok, err := redeem(req.Code)
		switch {
		case errors.Is(err, auth.ErrTokenNotFound):
			rpcerr.FormatJSON.Write(w, r, rpcerr.Error{Code: connect.CodeUnauthenticated, Reason: types.BearerRejected, Message: err.Error()})
			return
		case err != nil:
			log.WarnContext(r.Context(), "[TOKEN] exchange failed", slog.String("error", err.Error()))
			rpcerr.FormatJSON.Write(w, r, rpcerr.Error{Code: connect.CodeInternal, Reason: types.TokenRequestInvalid, Message: err.Error()})
			return
		}
		code := types.Credential{Class: types.ClassExchange, ID: auth.TokenID(req.Code), Name: tok.Name, Grant: tok.Grant}
		trail.AppendMint(r.Context(), trailDir, "link.redeem", trail.MintRecord{Minted: tok.Credential(), Expires: tok.Expires, Minter: code})
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(exchangeResponse{Token: secret, ExpiresAt: tok.Expires.UTC().Format(time.RFC3339)}); err != nil {
			log.WarnContext(r.Context(), "[TOKEN] encode exchange response", slog.String("error", err.Error()))
		}
	})
}

func redeem(code string) (string, auth.Token, error) {
	dir, err := auth.StoreDir()
	if err != nil {
		return "", auth.Token{}, err
	}
	store, err := auth.LoadStore(dir)
	if err != nil {
		return "", auth.Token{}, err
	}
	return store.Redeem(code)
}
