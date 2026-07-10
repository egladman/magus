package console

import (
	"net/url"
	"strings"

	wire "github.com/egladman/magus/internal/handler/viewer"
	"github.com/egladman/magus/internal/journal"
)

// LogViewerURL assembles the log-viewer deep link: BOTH the ref identity and the encoded
// output ride the URL fragment (after #), which the browser NEVER transmits to a server - so
// nothing about the run, not even its ref id, ever leaves the machine. The payload is a
// magus.viewer.v1 Journal (protobuf, gzip+base64url) of the ref's events; the browser decodes
// it and renders pretty from structure (the generated JS client, bundled in).
func LogViewerURL(base, ref string, events []journal.Event, inv journal.Invocation) (string, error) {
	j := journal.InvocationFromEvents(ref, events)
	// A single ref's display events are output+result only (no `started`), so
	// InvocationFromEvents yields no command lineage; graft the resolved run's Command so the
	// viewer's lineage header shows which command (and trigger) produced this output.
	if inv.ID != "" {
		j.Command = inv.Command
	}
	encoded, err := wire.EncodeJournalFragment(j, events)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(base, "/") + "/#ref=" + url.QueryEscape(ref) + "&data=" + encoded, nil
}
