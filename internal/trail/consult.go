package trail

import (
	"sort"
	"strings"
	"unicode/utf8"

	json "github.com/egladman/magus/internal/json"
)

// Consultation is one question an agent asked magus while producing a change: a read verb, what
// it named, and how many times it was asked.
//
// The subject is what makes this evidence, which is why it is kept where [Touch.Ran] reduces a
// command to its program. That reduction answers a leak: a recorded command line is stored
// VERBATIM, because the hook process installs no secret resolver (only the MCP handler does, so
// the Redact calls on that path are pass-throughs) and a review payload once carried a live
// bearer token out of one.
//
// What keeps this surface off that path is consultVerbs, a closed set. Every verb in it takes a
// graph subject, so the text kept here is a node id, a path, or a query string. Widening that set
// is what would put arbitrary argument text back into a review, so read the membership rule below
// before adding to it.
type Consultation struct {
	Verb    string `json:"verb"              yaml:"verb"`
	Subject string `json:"subject,omitempty" yaml:"subject,omitempty"`
	Count   int    `json:"count"             yaml:"count"`
}

// consultVerbs is the closed set of magus verbs a consultation may name. Three conditions decide
// membership, and a verb joins only when it meets all three:
//
//  1. It READS. A verb that runs work, mutates, or reports machine state is out, so run, affected,
//     x, watch, buzz, init, config, vcs, agent, doctor, status and server never appear here.
//  2. Its answer describes the WORKSPACE, which is what a change can be shaped by.
//  3. Its first argument is a SUBJECT a reviewer can re-ask, which is also what bounds the text
//     this package keeps (see [Consultation]).
//
// graph fails 1: the token is shared with `graph build`, which writes. ls, memory and notes read
// the workspace but spell their subject in a second token, so admitting them means a two-token
// verb model rather than an entry here. magus has no docs verb; a doc lookup arrives as
// `query kind=doc` or `describe`, both listed above.
var consultVerbs = map[string]bool{
	"query":    true,
	"explain":  true,
	"path":     true,
	"refs":     true,
	"describe": true,
	"where":    true,
}

// consultValueFlags are the magus flags that take a SEPARATE value, so a `--root <path>` does not
// hand the path to the verb slot. A flag spelled with "=" carries its own value.
var consultValueFlags = map[string]bool{
	"--root":        true,
	"--config":      true,
	"--output":      true,
	"-o":            true,
	"--concurrency": true,
}

// maxSubjectLen bounds a rendered subject. A subject is a node id or a query string, and the
// surface it feeds gives each one a single line.
const maxSubjectLen = 72

// ConsultGap says WHY an evidence list came back empty.
//
// Four different facts produce the same empty list, and only one of them describes the change.
// A surface that renders one sentence for all four tells a reviewer in a fresh clone that nobody
// researched this, when the record lives in the tree the work was done in. This repository
// has paid for a silence that read as a clean bill of health (see [ObservedCounts]).
type ConsultGap string

const (
	// ConsultGapNone is the zero value: the list has entries.
	ConsultGapNone ConsultGap = ""
	// ConsultGapUnobserved means no agent hook has recorded anything in this trail. The trail
	// lives under the cache dir, so it is PER CHECKOUT: a reviewer reading someone else's branch
	// gets this, and so does a workspace whose agents wire no hook.
	ConsultGapUnobserved ConsultGap = "unobserved"
	// ConsultGapUnleased means observations exist and none carries a lease, so nothing can be
	// tied to a write.
	ConsultGapUnleased ConsultGap = "unleased"
	// ConsultGapUnmatched means leased observations exist and none of them wrote a file in this
	// changeset. The work happened in another checkout, or it has aged out of the retained trail.
	ConsultGapUnmatched ConsultGap = "unmatched"
	// ConsultGapNoQuestions means a lease that wrote these files IS recorded and ran no read
	// verb. This is the one gap that is a fact about the change rather than about the record.
	ConsultGapNoQuestions ConsultGap = "no_questions"
)

// Consulted returns what the agents that wrote paths consulted before writing them: the magus
// read verbs they ran under the same lease, aggregated by subject, most asked first. It reads at
// most limit recent events.
//
// The join is the LEASE rather than the session [Replay] groups by. A session is one host
// process; a lease is the declared work unit, and one work unit spans an orchestrator and the
// sub-agents it hands work to. Joining on the session would report the orchestrator's questions
// and drop every question its workers asked about the same change.
//
// Every lease that wrote into paths contributes, and their questions MERGE into one list keyed by
// subject. A work unit that fanned out to sub-agents has a lease per worker, and the reviewer is
// asking what backs the changeset, so splitting the list by worker would answer a question nobody
// put. A subject two workers both asked about counts twice.
//
// An empty list is reported with a [ConsultGap] saying which silence it is. A consultation made
// over MCP is never recorded: it reaches a daemon serving many callers, which stamps no lease for
// the same reason it records no ancestry (see internal/handler/mcp).
//
// Best-effort throughout, like [Replay]: a missing trail, an unreadable blob, or an agent whose
// host wires no hook all contribute less.
func Consulted(root, base string, paths []string, limit int) ([]Consultation, ConsultGap) {
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		want[p] = true
	}
	events, err := ReadRecent(base, limit)
	if err != nil {
		return nil, ConsultGapUnobserved
	}

	type question struct{ lease, verb, subject string }
	leases := map[string]bool{}
	var asked []question
	var observed, leased int

	for _, e := range events {
		if e.Kind != KindAgentCommand || e.RequestRef == "" {
			continue
		}
		observed++
		// Counted before the lease test so the blob read stays off an unleased trail while the
		// gap can still tell "nothing was observed" from "nothing was leased".
		if e.Lease == "" {
			continue
		}
		leased++
		raw, rerr := ReadBlob(base, e.RequestRef)
		if rerr != nil {
			continue
		}
		var req agentCommandRequest
		if json.Unmarshal(raw, &req) != nil {
			continue
		}
		switch req.Tool {
		case toolWrite:
			if want[relativize(root, req.Path)] {
				leases[e.Lease] = true
			}
		case toolShell:
			if verb, subject, ok := consultationOf(req.Command); ok {
				asked = append(asked, question{e.Lease, verb, subject})
			}
		}
	}

	// Filtered after the walk rather than during it: a lease is established by a write, and the
	// consultations that explain it were recorded BEFORE that write by construction.
	counts := map[Consultation]int{}
	for _, q := range asked {
		if !leases[q.lease] {
			continue
		}
		counts[Consultation{Verb: q.verb, Subject: q.subject}]++
	}
	out := make([]Consultation, 0, len(counts))
	for c, n := range counts {
		c.Count = n
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].Verb != out[j].Verb {
			return out[i].Verb < out[j].Verb
		}
		return out[i].Subject < out[j].Subject
	})
	if len(out) > 0 {
		return out, ConsultGapNone
	}
	switch {
	case observed == 0:
		return nil, ConsultGapUnobserved
	case leased == 0:
		return nil, ConsultGapUnleased
	case len(leases) == 0:
		return nil, ConsultGapUnmatched
	default:
		return nil, ConsultGapNoQuestions
	}
}

// consultationOf reads a recorded command line as a magus consultation, reporting false when it is
// not one. Leading VAR=value assignments are skipped for the reason [commandProgram] skips them.
func consultationOf(cmd string) (verb, subject string, ok bool) {
	fields := strings.Fields(cmd)
	i := 0
	for i < len(fields) && isEnvAssignment(fields[i]) {
		i++
	}
	if i == len(fields) || !isMagusProgram(fields[i]) {
		return "", "", false
	}
	// A value-taking flag consumes the token after it, which can run past the end of a
	// truncated line, so the verb test below has to bound-check rather than compare.
	for i++; i < len(fields) && strings.HasPrefix(fields[i], "-"); i++ {
		if consultValueFlags[fields[i]] {
			i++
		}
	}
	if i >= len(fields) || !consultVerbs[fields[i]] {
		return "", "", false
	}
	verb = fields[i]

	var parts []string
	for _, f := range fields[i+1:] {
		if strings.HasPrefix(f, "-") {
			continue
		}
		parts = append(parts, f)
	}
	// The recorded line keeps the shell quoting a multi-term query was typed with, and the quotes
	// are not part of what was asked.
	subject = strings.Trim(strings.Join(parts, " "), `"'`)
	return verb, clampRunes(subject, maxSubjectLen), true
}

// isMagusProgram reports whether a command token invokes magus, under every spelling this
// repository teaches: a bare `magus`, a `./magus` built for the tree, an absolute path.
func isMagusProgram(tok string) bool {
	if i := strings.LastIndexAny(tok, `/\`); i >= 0 {
		tok = tok[i+1:]
	}
	return tok == "magus" || tok == "magus.exe"
}

// clampRunes bounds a string, cutting on a rune boundary so what is stored stays valid UTF-8.
func clampRunes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
