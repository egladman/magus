package console

import (
	"strings"

	"github.com/egladman/magus/internal/journal"
	queryv1 "github.com/egladman/magus/proto/gen/go/magus/query/v1"
	viewerv1 "github.com/egladman/magus/proto/gen/go/magus/viewer/v1"
)

// ParseEventQuery parses a viewer filter DSL string into a typed EventQuery: whitespace
// separated "field:value" clauses plus free text, e.g. `project:web target:build kind:output
// -"cache miss"`. Repeated values on one REPEATED field OR; different fields AND; matching is
// case-insensitive. Field filters are include-only (they mirror the filter-menu checkboxes);
// negation ("-word") is supported only on free text. `status` is single-valued (a result has
// one status), so a second status: clause replaces the first. The time window is NOT parsed
// from the DSL - the filter menu sets EventQuery.Time programmatically (date pickers), and
// ApplyEventQuery honors it. This is the viewer's OWN grammar over the log's own fields; it
// deliberately does not reuse the knowledge-graph query parser (whose fields differ).
func ParseEventQuery(s string) *viewerv1.EventQuery {
	q := &viewerv1.EventQuery{}
	// The repeated field keys and where each value appends - the single source of truth for the
	// viewer's field set. `status` is the one scalar field; anything else is free text.
	lists := map[string]*[]string{
		"project": &q.Projects,
		"target":  &q.Targets,
		"kind":    &q.Kinds,
		"stream":  &q.Streams,
		"level":   &q.Levels,
	}
	for _, tok := range tokenizeQuery(s) {
		if tok == "" {
			continue
		}
		if field, value, hasColon := strings.Cut(tok, ":"); hasColon {
			switch field = strings.ToLower(field); {
			case lists[field] != nil:
				*lists[field] = append(*lists[field], value)
				continue
			case field == "status":
				q.Status = value
				continue
			}
			// An unknown field key (e.g. the graph's "id:foo") is not a viewer field; fall
			// through and match the whole token as free text.
		}
		negate := strings.HasPrefix(tok, "-")
		text := strings.TrimPrefix(tok, "-")
		if text != "" {
			q.Text = append(q.Text, &queryv1.StringMatch{Value: text, Negate: negate})
		}
	}
	return q
}

// tokenizeQuery splits on whitespace, keeping "double quoted" spans as one token.
func tokenizeQuery(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case (r == ' ' || r == '\t') && !inQuote:
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// ApplyEventQuery returns the subset of events matching q, preserving order. A nil or empty q
// returns events unchanged. Set filters AND together; repeated values within a field OR;
// string comparison is case-insensitive; text matches are substring (a negated text match
// excludes events that contain it). The input slice is not mutated.
func ApplyEventQuery(events []journal.Event, q *viewerv1.EventQuery) []journal.Event {
	if q == nil {
		return events
	}
	out := make([]journal.Event, 0, len(events))
	for _, e := range events {
		if matchEvent(e, q) {
			out = append(out, e)
		}
	}
	return out
}

func matchEvent(e journal.Event, q *viewerv1.EventQuery) bool {
	if len(q.Projects) > 0 && !containsFold(q.Projects, e.Project) {
		return false
	}
	if len(q.Targets) > 0 && !containsFold(q.Targets, e.Target) {
		return false
	}
	if len(q.Kinds) > 0 && !containsFold(q.Kinds, e.Kind) {
		return false
	}
	if len(q.Streams) > 0 && !containsFold(q.Streams, e.Stream) {
		return false
	}
	if len(q.Levels) > 0 && !containsFold(q.Levels, e.Level) {
		return false
	}
	if q.Status != "" && !strings.EqualFold(q.Status, e.Status) {
		return false
	}
	text := strings.ToLower(e.Text)
	for _, m := range q.Text {
		if strings.Contains(text, strings.ToLower(m.Value)) == m.Negate {
			return false
		}
	}
	if q.Time != nil {
		if q.Time.Since != nil && e.Ts < q.Time.Since.AsTime().UnixMilli() {
			return false
		}
		if q.Time.Until != nil && e.Ts > q.Time.Until.AsTime().UnixMilli() {
			return false
		}
	}
	return true
}

// containsFold reports whether v equals any entry of list, case-insensitively.
func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(x, v) {
			return true
		}
	}
	return false
}
