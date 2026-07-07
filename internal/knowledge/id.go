package knowledge

import (
	"strings"

	"github.com/egladman/magus/types"
)

// Node ID scheme: "<kind>:<qualified-name>", stable across builds and human
// readable so external consumers and agent memory can key on it. The project
// path is embedded in target/op-adjacent IDs so an edge crossing projects names
// exactly the shard to load next (the routing key, per the plan). No invented
// vocabulary - kinds and separators only.

func projectID(path string) string { return types.KindProject + ":" + path }

func targetID(projectPath, name string) string {
	return types.KindTarget + ":" + projectPath + ":" + name
}

func spellID(name string) string { return types.KindSpell + ":" + name }

func opID(spell, op string) string { return types.KindOp + ":" + spell + ":" + op }

func moduleID(name string) string { return types.KindModule + ":" + name }

func methodID(module, method string) string {
	return types.KindMethod + ":" + module + "." + method
}

func diagnosticID(code string) string { return types.KindDiagnostic + ":" + code }

func charmID(name string) string { return types.KindCharm + ":" + name }

// sanitize normalizes free-form repo text (labels, docs, provenance) before it
// enters the graph, per the plan's ingest-sanitization requirement: strip
// control characters (which would corrupt MAGUS.md, MCP responses, and agent
// contexts) and cap length to keep node cards and exports bounded. Newlines and
// tabs collapse to spaces; other control runes are dropped.
func sanitize(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t' || r == '\r':
			return ' '
		case r < 0x20 || r == 0x7f:
			return -1
		default:
			return r
		}
	}, s)
	s = strings.TrimSpace(s)
	if limit > 0 && len(s) > limit {
		s = strings.TrimSpace(s[:limit])
	}
	return s
}

// Sanitization caps. Labels are short identifiers; docs are one-line summaries.
const (
	maxLabelLen = 256
	maxDocLen   = 512
	maxSrcLen   = 512
)
