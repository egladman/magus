package model

import (
	"fmt"

	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/types"
)

// contextFloor is the cache-read price of a thread's reported context: a floor, since it
// leaves out cache writes, uncached input, output and every subagent. Calls counts the
// model calls priced and Tokens the context they read.
type contextFloor struct {
	Calls  int
	Tokens int64
	USD    float64
}

// PriceContext prices against t the context calls reported at the cache-read rate, skipping a call
// that reported none, and returns the reason instead when the price would be a guess: no
// call to price, billing that is not metered on every call, or a model the table does not
// know. Consecutive calls reporting the same context are one model call that ran several
// tools, so they are priced once.
func PriceContext(t tier.Table, calls []types.AgentFacts) (contextFloor, string) {
	var samples []types.AgentFacts
	for _, call := range calls {
		if call.ContextTokens > 0 {
			samples = append(samples, call)
		}
	}
	if len(samples) == 0 {
		return contextFloor{}, "no call reported a context size"
	}
	for _, call := range samples {
		switch call.Billing {
		case types.AgentBillingMetered:
		case "":
			return contextFloor{}, "billing is unknown: set agents.billing in magus.yaml"
		default:
			return contextFloor{}, fmt.Sprintf("billing is %s, which has no per-token price", call.Billing)
		}
	}
	var floor contextFloor
	var prev types.AgentFacts
	for i, call := range samples {
		pricing, ok := t.Lookup(call.ContextModel)
		if !ok || pricing.CacheRead == 0 {
			return contextFloor{}, fmt.Sprintf("model %q is not in the model table: add it under agents.models in magus.yaml", call.ContextModel)
		}
		if i > 0 && call.ContextTokens == prev.ContextTokens && call.ContextModel == prev.ContextModel {
			continue
		}
		prev = call
		floor.Calls++
		floor.Tokens += call.ContextTokens
		floor.USD += float64(call.ContextTokens) / 1e6 * float64(pricing.CacheRead) * pricing.PriceMultiplier(call.ContextTokens)
	}
	return floor, ""
}
