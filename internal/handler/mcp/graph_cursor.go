//go:build mcp

package mcp

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
)

// Pagination for magus_query is stateless: the cursor is an opaque token the
// client echoes back, carrying only the next offset plus two guards - a hash of
// the query and the graph's fingerprint at issue time. On the next page both are
// re-checked, so a cursor reused against a different query, or against a graph that
// changed underneath it (a warm-graph rebuild between pages), fails loudly instead
// of returning an incoherent slice. No server-side session, nothing to expire.

// queryCursor is the decoded pagination token. Short JSON keys keep the encoded
// string compact.
type queryCursor struct {
	Offset    int    `json:"o"`
	QueryHash string `json:"q"`
	GraphFP   string `json:"g"`
}

// encodeCursor renders a cursor as a compact, URL-safe opaque token.
func encodeCursor(c queryCursor) string {
	b, _ := json.Marshal(c) // a fixed struct of scalars never fails to marshal
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor parses a token produced by encodeCursor; a malformed token is an
// error so a hand-edited or truncated cursor is rejected rather than misread.
func decodeCursor(s string) (queryCursor, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return queryCursor{}, err
	}
	var c queryCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return queryCursor{}, err
	}
	return c, nil
}

// queryHash identifies a query string for cursor validation: a cursor is only
// valid against the same (trimmed) query it was issued for.
func queryHash(query string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(query)))
	return hex.EncodeToString(sum[:8])
}
