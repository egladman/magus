package mcp

// next.go carries the structural breadcrumbs onto MCP replies. internal/hint owns
// which entries a result earns and which of them a role may be served; this file
// owns splicing the field into a JSON payload, appending it to a text one, and
// recording what was served.

import (
	"bytes"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
)

// nextFilter grades a result's breadcrumbs for the acting role and journals what
// survived, in this checkout's cache dir.
//
// A zero value serves everything and records nothing, which is what a tool built
// without a workspace behind it should do.
type nextFilter struct {
	cacheDir string
	rows     *job.Store
}

// served filters next for the acting role and records what was handed over.
//
// The journal is per checkout, so the entries land beside the ones a CLI run in the
// same tree writes. That is the scope the guard reads it at.
func (f nextFilter) served(next []hint.Next) []hint.Next {
	role, lane := f.role()
	served := hint.OnPath(hint.ServableTo(role, lane, next))
	hint.AppendServedNext(f.cacheDir, served)
	return served
}

// role reads the acting job's row off this checkout's job store.
func (f nextFilter) role() (hint.Role, []string) {
	id := job.ActingLease(f.cacheDir)
	if id == "" {
		return hint.RoleUnbound, nil
	}
	if f.rows == nil {
		return hint.RoleWorker, nil
	}
	rows, err := f.rows.List()
	if err != nil {
		return hint.RoleWorker, nil
	}
	return hint.RoleFor(rows, id)
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
// missing. A payload that is not an object, or that already carries a top-level
// `next`, is returned untouched.
func mergeNext(raw []byte, next []hint.Next) []byte {
	if len(next) == 0 {
		return raw
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) < 2 || trimmed[0] != '{' || trimmed[len(trimmed)-1] != '}' {
		return raw
	}
	if carriesNextKey(trimmed) {
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

// carriesNextKey reports whether the object already declares a top-level `next`,
// which splicing a second one would duplicate.
func carriesNextKey(raw []byte) bool {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return true
	}
	_, ok := keys["next"]
	return ok
}

// renderNext is the text-reply form, for a tool whose answer is prose rather than a
// record. Every Why prints: the once-per-session suppression is a terminal's concern.
func renderNext(next []hint.Next) string {
	return hint.Render(next, func(entry hint.Next) string { return entry.Why })
}
