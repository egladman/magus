package trail

import (
	"bufio"
	"cmp"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// FeedbackWindow selects one session's observations: the events inside [Since, Until]
// whose Session matches. An empty Session selects the session with the newest agent
// observation in the window, which at the end of a session is the one asking.
type FeedbackWindow struct {
	Session string
	Since   time.Time
	Until   time.Time
}

// FeedbackBases is every trail directory a session in this repository may have recorded
// into: each checkout's cache dir whose events file changed at or after since.
//
// A hook records into the cache dir of the checkout it ran from, which for a session that
// spawns workers into worktrees is the session's own checkout, not the worker's. Reading
// only the caller's checkout therefore misses most sessions. The mtime filter keeps the
// cost to the trails that could hold an event in the window; a repository with hundreds of
// worktrees stats each events file once.
//
// cacheRel is the cache dir relative to a checkout root (".magus" by default); an absolute
// one names a cache every checkout shares, and is the only base.
func FeedbackBases(checkouts []string, cacheRel string, since time.Time) []string {
	if filepath.IsAbs(cacheRel) {
		return []string{cacheRel}
	}
	var out []string
	for _, root := range checkouts {
		base := filepath.Join(root, cacheRel)
		fi, err := os.Stat(eventsPath(base))
		if err != nil || fi.ModTime().Before(since) {
			continue
		}
		if !slices.Contains(out, base) {
			out = append(out, base)
		}
	}
	slices.Sort(out)
	return out
}

// ReadFeedback folds the trails at bases into one session's guard record. A trail that
// cannot be read contributes nothing; an event whose blobs are gone is skipped, since the
// trail rotates blobs out with the events that cite them.
//
// Checkouts on the result names the bases that held at least one event of the session.
func ReadFeedback(bases []string, w FeedbackWindow) (types.FeedbackTrail, error) {
	since, until := w.Since.UnixMilli(), w.Until.UnixMilli()
	out := types.FeedbackTrail{Start: since, End: until, Session: w.Session}
	type sourced struct {
		base string
		e    Event
	}
	var events []sourced
	var newest int64
	for _, base := range bases {
		read, err := eventsInWindow(base, since, until)
		if err != nil {
			return out, err
		}
		for _, e := range read {
			if e.Kind != KindAgentCommand && e.Kind != KindAgentSpawn {
				continue
			}
			events = append(events, sourced{base, e})
			if w.Session == "" && e.Session != "" && e.Ts >= newest {
				newest, out.Session = e.Ts, e.Session
			}
		}
	}
	if out.Session == "" {
		return out, nil
	}
	slices.SortStableFunc(events, func(a, b sourced) int { return cmp.Compare(a.e.Ts, b.e.Ts) })

	for _, s := range events {
		e := s.e
		if e.Session != out.Session {
			continue
		}
		if out.Host == "" {
			out.Host = e.Host
		}
		if !slices.Contains(out.Checkouts, filepath.Dir(s.base)) {
			out.Checkouts = append(out.Checkouts, filepath.Dir(s.base))
		}
		switch e.Kind {
		case KindAgentCommand:
			if obs, ok := feedbackObservation(s.base, e); ok {
				out.Observations = append(out.Observations, obs)
			}
		case KindAgentSpawn:
			if e.Action == ActionAgentContinue {
				continue
			}
			sp := types.FeedbackSpawn{At: e.Ts, Agent: e.Agent, Child: e.Action, Lease: e.Lease}
			if raw, err := ReadBlob(s.base, e.RequestRef); err == nil {
				var req agentSpawnRequest
				if json.Unmarshal(raw, &req) == nil {
					sp.Model = req.DeclaredModel
				}
			}
			out.Spawns = append(out.Spawns, sp)
		}
	}
	slices.Sort(out.Checkouts)
	return out, nil
}

func feedbackObservation(base string, e Event) (types.FeedbackObservation, bool) {
	var req agentCommandRequest
	var resp agentCommandResponse
	raw, err := ReadBlob(base, e.RequestRef)
	if err != nil || json.Unmarshal(raw, &req) != nil {
		return types.FeedbackObservation{}, false
	}
	// A read carries no response blob: no rule judges it.
	if e.ResponseRef != "" {
		if raw, err := ReadBlob(base, e.ResponseRef); err == nil {
			_ = json.Unmarshal(raw, &resp)
		}
	}
	obs := types.FeedbackObservation{
		At: e.Ts, Agent: e.Agent, Lease: e.Lease, Tool: req.Tool,
		Command: req.Command, Path: req.Path,
		Decision: resp.Decision, Rule: resp.Rule, PreauthorizedBy: resp.PreauthorizedBy,
		Nexts: servedNexts(resp.Reason + "\n" + resp.Context),
	}
	if req.Command != "" {
		obs.Shape = CommandShape(req.Command)
		obs.Shapes = CommandShapes(req.Command)
	}
	return obs, true
}

// eventsInWindow decodes the events of the trail at base whose Ts falls in [since, until],
// oldest first. A missing trail is no events.
func eventsInWindow(base string, since, until int64) ([]Event, error) {
	f, err := os.Open(eventsPath(base))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Event
		if json.Unmarshal(line, &e) != nil || e.Ts < since || e.Ts > until {
			continue
		}
		out = append(out, e)
	}
	return out, sc.Err()
}

// servedNexts reads the commands a verdict's text served under its `next:` block: each
// line indented two spaces after a line reading "next:", until the first line that is
// neither one of those nor a deeper-indented explanation.
//
// The block is the guard's rendering of Verdict.Next, the one place the trail keeps it.
func servedNexts(text string) []string {
	var out []string
	in := false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.TrimSpace(line) == "next:":
			in = true
		case !in:
		case strings.HasPrefix(line, "    "):
			// an explanation under the command above it
		case strings.HasPrefix(line, "  ") && strings.TrimSpace(line) != "":
			out = append(out, strings.TrimSpace(line))
		default:
			in = false
		}
	}
	return out
}
