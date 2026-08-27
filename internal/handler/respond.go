package handler

import (
	"net/http"

	json "github.com/egladman/magus/internal/json"
)

// AllowGet answers a CORS preflight (204) and rejects non-GET methods (405), returning false
// when the caller should stop.
//
// Here rather than beside any one route: every read handler in every handler package applies the
// same gate, and the copy that lived next to the insight route was the one six unrelated files
// reached across for.
func AllowGet(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return false
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

// WriteJSON marshals v and writes it as an uncached JSON body, matching the read handlers'
// no-store posture: these reads reflect live daemon state.
func WriteJSON(w http.ResponseWriter, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		http.Error(w, "marshal error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}
