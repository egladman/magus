package mcp

// next.go carries the structural breadcrumbs onto MCP replies. internal/hint owns
// which entries a result earns and which of them a role may be served; this file
// owns splicing the field into a JSON payload, appending it to a text one, and
// recording what was served.
//
// It is the same field the CLI emits, filtered the same way. A reply shape that
// carried the breadcrumbs on one channel and not the other would make uptake per id
// a fact about which door the reader came through.

import (
	"bytes"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/types"
)

// nextServer filters a result's breadcrumbs for the acting role and journals what
// survived, beside the advisory markers in this checkout's cache dir.
//
// A zero value serves everything and records nothing, which is what a tool built
// without a workspace behind it should do.
type nextServer struct {
	cacheDir string
	rows     leaseLister
}

// leaseLister is the slice of the ledger store the role derivation needs.
type leaseLister interface {
	List() ([]types.Lease, error)
}

func newNextServer(cacheDir string, rows leaseLister) *nextServer {
	return &nextServer{cacheDir: cacheDir, rows: rows}
}

// serve filters next for the acting role and records what was handed over.
//
// The MCP door reports no session id of its own here, so the journal lands in the
// same anonymous bucket a CLI run writes to. Both are this checkout, which is the
// scope the guard reads it at.
func (s *nextServer) serve(next []hint.Next) []hint.Next {
	if s == nil {
		return hint.ForRole(hint.RoleUnbound, next)
	}
	served := hint.ForRole(s.role(), next)
	hint.AppendServedNext(s.cacheDir, "", served)
	return served
}

// role reads the acting lease's row: no lease is unbound, a read-only row or one
// owning no path is a reviewer, anything else a worker. Derived rather than stored,
// for the reason the CLI derives it: a second field saying what OwnedPaths already
// says is a field that can disagree with it.
func (s *nextServer) role() hint.Role {
	id := ledger.ActingLease(s.cacheDir)
	if id == "" {
		return hint.RoleUnbound
	}
	if s.rows == nil {
		return hint.RoleWorker
	}
	rows, err := s.rows.List()
	if err != nil {
		return hint.RoleWorker
	}
	for _, row := range rows {
		if row.ID != id {
			continue
		}
		if row.ReadOnly || len(row.OwnedPaths) == 0 {
			return hint.RoleReviewer
		}
		return hint.RoleWorker
	}
	return hint.RoleWorker
}

// dataWithNext is the reply payload for a record-shaped tool: v with one additive
// `next` key. The result is pre-encoded, which jsonResult emits verbatim.
//
// A payload that will not marshal here is returned as itself, so the tool's own
// marshal failure is reported where it always was rather than as a missing field.
func dataWithNext(v any, next []hint.Next) any {
	if len(next) == 0 {
		return v
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	return json.RawMessage(mergeNext(raw, next))
}

// mergeNext splices next into an already-marshaled JSON object as one additive key.
//
// Spliced rather than re-encoded through a wrapper type: the payloads are a dozen
// concrete types, and a wrapper per tool is a dozen places for the field to go
// missing. A payload that is not an object is returned untouched, since there is no
// key to add.
func mergeNext(raw []byte, next []hint.Next) []byte {
	if len(next) == 0 {
		return raw
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return raw
	}
	encoded, err := json.Marshal(next)
	if err != nil {
		return raw
	}
	body := trimmed[:len(trimmed)-1]
	sep := ","
	if bytes.Equal(bytes.TrimSpace(body), []byte("{")) {
		sep = ""
	}
	out := append([]byte{}, body...)
	out = append(out, sep+`"next":`...)
	out = append(out, encoded...)
	return append(out, '}')
}

// renderNext is the text-reply form, for a tool whose answer is prose rather than a
// record. Same two-line shape the CLI prints, so a reader meets one layout.
func renderNext(next []hint.Next) string {
	if len(next) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nnext:\n")
	for _, n := range next {
		b.WriteString("  " + n.Run + "\n")
		if n.Why != "" {
			b.WriteString("      " + n.Why + "\n")
		}
	}
	return b.String()
}
