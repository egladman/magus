package trail

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	trailrecord "github.com/egladman/magus/types/trail"
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
// trail rotates blobs out with the events that cite them. A request blob that is there and
// does not decode is the error.
//
// Checkouts on the result names the bases that held at least one event of the session.
func ReadFeedback(bases []string, w FeedbackWindow) (trailrecord.Trail, error) {
	since, until := w.Since.UnixMilli(), w.Until.UnixMilli()
	out := trailrecord.Trail{Start: since, End: until, Session: w.Session}
	type sourced struct {
		base string
		e    journal.Call
	}
	var events []sourced
	var newest int64
	for _, base := range bases {
		read, err := eventsInWindow(base, since, until)
		if err != nil {
			return out, err
		}
		for _, e := range read {
			if e.Kind != journal.CallKindAgentCommand && e.Kind != journal.CallKindAgentSpawn {
				continue
			}
			events = append(events, sourced{base, e})
			if w.Session == "" && e.Session != "" && e.Time.UnixMilli() >= newest {
				newest, out.Session = e.Time.UnixMilli(), e.Session
			}
		}
	}
	if out.Session == "" {
		return out, nil
	}
	slices.SortStableFunc(events, func(a, b sourced) int { return a.e.Time.Compare(b.e.Time) })

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
		case journal.CallKindAgentCommand:
			obs, ok, err := feedbackObservation(s.base, e)
			if err != nil {
				return out, err
			}
			if ok {
				out.Observations = append(out.Observations, obs)
			}
		case journal.CallKindAgentSpawn:
			if e.Name == ActionAgentContinue {
				continue
			}
			sp := trailrecord.Spawn{At: e.Time.UnixMilli(), Agent: e.Agent, Child: e.Name, Lease: e.Lease}
			if raw, err := e.Request.Read(CallsDir(s.base)); err == nil {
				var req agentSpawnRequest
				if codec.Unmarshal(raw, &req) == nil {
					sp.Model = req.DeclaredModel
				}
			}
			out.Spawns = append(out.Spawns, sp)
		}
	}
	slices.Sort(out.Checkouts)
	return out, nil
}

// feedbackObservation reads one agent-command event's blobs. It reports false for an event
// that recorded no request blob or whose blob is gone. A request blob that cannot be read or decoded is its error: a
// [*blobUndecodableError], or a [*retiredDecisionError] for one holding a retired guard
// decision.
func feedbackObservation(base string, e journal.Call) (trailrecord.Observation, bool, error) {
	var req agentCommandRequest
	var resp agentCommandResponse
	var retired *retiredDecisionError
	if e.Request.Ref == "" {
		return trailrecord.Observation{}, false, nil
	}
	raw, err := e.Request.Read(CallsDir(base))
	if errors.Is(err, fs.ErrNotExist) {
		return trailrecord.Observation{}, false, nil
	}
	if err != nil {
		return trailrecord.Observation{}, false, err
	}
	if err := decodeAgentBlob(base, e.Request.Ref, raw, &req); err != nil {
		return trailrecord.Observation{}, false, err
	}
	// A read carries no response blob: no rule judges it.
	if e.Response.Ref != "" {
		if raw, err := e.Response.Read(CallsDir(base)); err == nil {
			if err := decodeAgentBlob(base, e.Response.Ref, raw, &resp); errors.As(err, &retired) {
				return trailrecord.Observation{}, false, err
			}
		}
	}
	obs := trailrecord.Observation{
		At: e.Time.UnixMilli(), Agent: e.Agent, Lease: e.Lease, Tool: req.Tool,
		Command: req.Command, Path: req.Path,
		Decision: string(resp.Decision), Rule: resp.Rule, PreauthorizedBy: resp.PreauthorizedBy,
		Nexts: servedNexts(fmt.Sprintf("%s\n%s", resp.Reason, resp.Context)),
	}
	if req.Command != "" {
		obs.Shape = CommandShape(req.Command)
		obs.Shapes = CommandShapes(req.Command)
	}
	return obs, true, nil
}

// eventsInWindow decodes the events of the trail at base whose Ts falls in [since, until],
// oldest first. A missing trail is no events.
func eventsInWindow(base string, since, until int64) ([]journal.Call, error) {
	f, err := os.Open(eventsPath(base))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	var out []journal.Call
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e journal.Call
		if codec.Unmarshal(line, &e) != nil || e.Time.UnixMilli() < since || e.Time.UnixMilli() > until {
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
