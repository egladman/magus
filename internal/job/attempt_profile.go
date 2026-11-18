package job

import (
	"context"
	"errors"
	"math"
	"slices"

	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/journal"
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
func attemptWork(ctx context.Context, base, lease string, since int64) (*types.JobWork, []types.AgentFacts) {
	if base == "" {
		return nil, nil
	}
	denials, known, err := recorder.LeaseDenials(ctx, base, lease, since, math.MaxInt)
	if err != nil || !known {
		return nil, nil
	}
	var events []journal.Call
	for c, err := range recorder.Calls(ctx, base, workReadWindow) {
		if errors.Is(err, codec.ErrLineUndecodable) {
			continue
		}
		if err != nil {
			return nil, nil
		}
		events = append(events, c)
	}
	from := since * 1000
	if len(events) == workReadWindow && events[len(events)-1].Time.UnixMilli() > from {
		return nil, nil
	}
	slices.Reverse(events)

	work := types.JobWork{Denials: len(denials)}
	var sum, peak, setup int64
	var facts []types.AgentFacts
	everyReported := true
	for _, e := range events {
		if e.Kind != journal.CallKindAgentCommand || e.Lease != lease || e.Time.UnixMilli() < from {
			continue
		}
		f, reported := callFacts(base, e)
		facts = append(facts, f)
		tokens := f.ContextTokens
		everyReported = everyReported && reported
		sum += tokens
		peak = max(peak, tokens)
		work.Calls++
		if e.Name != writeTool {
			if work.Writes == 0 {
				work.SetupCalls++
				setup += tokens
			}
			continue
		}
		if work.Writes == 0 {
			ms := e.Time.UnixMilli() - from
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
func callFacts(base string, e journal.Call) (types.AgentFacts, bool) {
	if e.Request.Ref == "" {
		return types.AgentFacts{}, false
	}
	raw, err := recorder.ReadPayload(base, e.Request)
	if err != nil {
		return types.AgentFacts{}, false
	}
	var facts types.AgentFacts
	if codec.Unmarshal(raw, &facts) != nil || facts.ContextTokens <= 0 {
		return types.AgentFacts{}, false
	}
	return facts, true
}
