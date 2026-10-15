package job

import (
	"math"
	"slices"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// workReadWindow bounds the trail events one work profile reads. A window that fills
// without reaching back to the attempt's start leaves the profile unknown.
const workReadWindow = 20_000

// writeTool is the trail's action for a file write, the class the guard records an agent's
// edit, write or patch tool under.
const writeTool = "file.write"

// attemptWork folds the guard's observations of lease's holder since since (unix seconds)
// out of the trail at base, with the cost facts the host reported beside each call, in
// order. Nil is unknown: no trail to read, one that no longer reaches back to since, or no
// call recorded under lease, which is a holder the guard never saw rather than an idle
// one. It reads the trail [trail.LeaseDenials] reads, whose test of how far the retained
// trail reaches is the one this profile is known by.
func attemptWork(base, lease string, since int64) (*types.JobWork, []types.AgentFacts) {
	if base == "" {
		return nil, nil
	}
	denials, known, err := trail.LeaseDenials(base, lease, since, math.MaxInt)
	if err != nil || !known {
		return nil, nil
	}
	events, err := trail.ReadRecent(base, workReadWindow)
	if err != nil {
		return nil, nil
	}
	from := since * 1000
	if len(events) == workReadWindow && events[len(events)-1].Ts > from {
		return nil, nil
	}
	slices.Reverse(events)

	work := types.JobWork{Denials: len(denials)}
	var sum, peak, setup int64
	var facts []types.AgentFacts
	everyReported := true
	for _, e := range events {
		if e.Kind != trail.KindAgentCommand || e.Lease != lease || e.Ts < from {
			continue
		}
		f, reported := callFacts(base, e)
		facts = append(facts, f)
		tokens := f.ContextTokens
		everyReported = everyReported && reported
		sum += tokens
		peak = max(peak, tokens)
		work.Calls++
		if e.Action != writeTool {
			if work.Writes == 0 {
				work.SetupCalls++
				setup += tokens
			}
			continue
		}
		if work.Writes == 0 {
			ms := e.Ts - from
			work.SetupMs = &ms
			if reported {
				work.SetupTokens = &tokens
			}
			if everyReported {
				work.SetupContext = &setup
			}
		}
		work.Writes++
	}
	if work.Calls == 0 {
		return nil, nil
	}
	if everyReported {
		work.ContextTokens, work.PeakTokens = &sum, &peak
	}
	return &work, facts
}

// callFacts is the cost facts the host reported beside one call, from the call's request
// record; false when it reported no context size or the record rotated away.
func callFacts(base string, e trail.Event) (types.AgentFacts, bool) {
	if e.RequestRef == "" {
		return types.AgentFacts{}, false
	}
	raw, err := trail.ReadBlob(base, e.RequestRef)
	if err != nil {
		return types.AgentFacts{}, false
	}
	var facts types.AgentFacts
	if json.Unmarshal(raw, &facts) != nil || facts.ContextTokens <= 0 {
		return types.AgentFacts{}, false
	}
	return facts, true
}
