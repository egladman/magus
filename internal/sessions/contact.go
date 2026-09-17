package sessions

import (
	"slices"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/repoid"
)

// Reading the loaded-transcript half of the store back for ONE path.
//
// The knowledge graph's @session overlay answers the same question in aggregate, but it
// is a lazy shard keyed to the symbol layer, so a single-node lookup that went through it
// would pay for every code symbol in the workspace to learn about one file. This reads the
// store the overlay is built from, which is the cheaper half of a fact magus already
// decodes on every graph build.

// PathContact is what the loaded agent sessions did to one file: how many distinct
// sessions reached it, how many reads and writes they made, how many the host refused,
// and when the newest of them happened.
//
// It is the per-file half of the same fact the knowledge graph's @session overlay carries
// as agent_sessions / agent_reads / agent_writes on a file node.
//
// Sessions is a COUNT OF DISTINCT SESSIONS rather than of events, because the question it
// answers is "is somebody else working here", and one session that saved a file nine
// times is one answer, not nine.
type PathContact struct {
	Sessions int `json:"sessions" yaml:"sessions"`
	Reads    int `json:"reads"    yaml:"reads"`
	Writes   int `json:"writes"   yaml:"writes"`
	// Denials counts events the HOST refused, across reads and writes alike. Kept as one
	// number because the two are refused for the same reasons and a caller that renders it
	// as "writes refused" is claiming more than the store knows.
	Denials int `json:"denials" yaml:"denials"`
	// Last is when the newest contact happened, or the zero value when no event carried a
	// time. Undated is a real answer here: a host is not required to record one, and
	// omitzero is what keeps that absence from rendering as the epoch.
	Last time.Time `json:"last,omitzero" yaml:"last,omitzero"`
}

// Touched reports whether any loaded session reached this path.
func (c PathContact) Touched() bool { return c.Sessions > 0 }

// ReadPathContact summarizes what the loaded sessions did to one checkout-relative path,
// or the zero value when none of them reached it.
//
// The verb is READ because this reads the store, pairing with ReadAll beside it. It was
// ContactFor, which two separate readers stopped at to ask what a contact was and whose
// it was: the name carried neither the action nor the object, and "for" stood in for
// both.
//
// Best-effort like every other reader of this store: no store, an unreadable one, or a
// record this build cannot decode all report nothing rather than failing the caller. A
// workspace that has never loaded a transcript is indistinguishable from one whose files
// no session touched, which is correct: both have nothing to say.
func ReadPathContact(dir, path string) PathContact {
	var out PathContact
	if dir == "" || path == "" {
		return out
	}
	fold, err := ReadAll(dir)
	if err != nil {
		return out
	}
	var seen []string
	for _, rec := range fold.Records {
		if rec.Kind != KindAgentEvent {
			continue
		}
		var ev AgentEvent
		if json.Unmarshal(rec.Payload, &ev) != nil {
			continue
		}
		// compat: see knowledge.go's loadKnowledgeAgentContacts. Loads store the
		// checkout-relative path now; events loaded before they did still need reducing,
		// and the overlay and this reader must agree on what a path IS or one would find
		// contact the other cannot.
		//
		// The cheap comparison runs first because CheckoutRelative walks ancestors with
		// Lstat for an absolute path, and this loop runs once per record in the store:
		// paying that walk for every event to serve one `magus explain` would put a
		// filesystem traversal per historical event on an interactive command.
		if ev.Text != path && repoid.CheckoutRelative(ev.Text) != path {
			continue
		}
		switch ev.Kind {
		case EventFileRead:
			out.Reads++
		case EventFileWrite:
			out.Writes++
		default:
			// Every other kind is path-less by construction: a shell command's text is
			// never stored, and a skill load or a spawn is not about a file. One that
			// matched this path anyway is not evidence of contact WITH the file.
			continue
		}
		if ev.Denied {
			out.Denials++
		}
		if !slices.Contains(seen, rec.Session) {
			seen = append(seen, rec.Session)
		}
		// A zero AtMs is "the host recorded no time", not 1970. Taken literally it is
		// After every real Last, so one undated event would date the whole contact to the
		// epoch and render as twenty thousand days ago, which reads as a confident answer
		// rather than the missing one it is. PathContact.Last keeps its zero value, which
		// the caller already renders as unrecorded.
		if at := time.UnixMilli(ev.AtMs); ev.AtMs > 0 && at.After(out.Last) {
			out.Last = at
		}
	}
	out.Sessions = len(seen)
	return out
}
