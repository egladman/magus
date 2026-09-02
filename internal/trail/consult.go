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
// The subject is what makes this evidence, which is why it is kept where [Touch.Ran] drops every
// argument. A magus read verb is the one recorded command whose arguments ARE the record: "magus"
// says nothing, "explain internal/cache" says what was consulted. The text is safe to keep for two
// reasons that hold together: the blob it comes from is scrubbed by [WriteBlob] before it is
// stored, and the verbs in consultVerbs take a graph subject.
type Consultation struct {
	Verb    string `json:"verb"              yaml:"verb"`
	Subject string `json:"subject,omitempty" yaml:"subject,omitempty"`
	Count   int    `json:"count"             yaml:"count"`
}

// consultVerbs are the magus read verbs whose subject a reviewer can audit. A verb that RUNS work
// is absent: a build already has a journal, and it answers what happened rather than what was
// consulted. Doc lookups arrive through these too, as `query kind=doc` and `describe`, so magus
// has no separate docs verb to list here.
var consultVerbs = map[string]bool{
	"query":    true,
	"explain":  true,
	"path":     true,
	"refs":     true,
	"describe": true,
}

// consultValueFlags are the magus flags that take a SEPARATE value, so a `--root <path>` does not
// hand the path to the verb slot. A flag spelled with "=" carries its value already.
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

// Consulted returns what the agents that wrote paths consulted before writing them: the magus
// read verbs they ran under the same lease, aggregated by subject, most asked first. It reads at
// most limit recent events.
//
// The join is the LEASE rather than the session [Replay] groups by. A session is one host
// process; a lease is the declared work unit, and one work unit routinely spans an orchestrator
// and the sub-agents it hands work to. Joining on the session would report the orchestrator's
// questions and drop every question its workers asked about the same change.
//
// A change written without a lease reports nothing, which is the designed outcome rather than an
// error. So does a consultation made over MCP: that reaches a daemon serving many callers, which
// records no lease for the same reason it records no ancestry (see internal/handler/mcp).
//
// Best-effort throughout, like [Replay]: a missing trail, an unreadable blob, or an agent whose
// host wires no hook all contribute less.
func Consulted(root, base string, paths []string, limit int) []Consultation {
	want := make(map[string]bool, len(paths))
	for _, p := range paths {
		want[p] = true
	}
	events, err := ReadRecent(base, limit)
	if err != nil {
		return nil
	}

	type question struct{ lease, verb, subject string }
	leases := map[string]bool{}
	var asked []question

	for _, e := range events {
		// The lease test comes first so an unleased trail costs no blob reads at all. Nothing
		// downstream can use an event without one.
		if e.Kind != KindAgentCommand || e.RequestRef == "" || e.Lease == "" {
			continue
		}
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
	return out
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
