package main

import (
	"cmp"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// followWithin is how many main-thread calls after an advisory a spawn still counts as
// the agent acting on it.
const followWithin = 10

// costAdviceRules are the cost rules whose advice a spawn answers. spawn-names-model is
// absent: it asks for a model to be named, not for work to move.
var costAdviceRules = []string{
	"root-context-budget",
	"root-heavy-command",
	"root-result-cap",
	"root-repeat",
	"spawn-above-model-tier",
}

// costSection names one cost section. A section nobody could measure is listed under
// sessionCost.NotMeasured by its name instead of being left out or zeroed.
type costSection string

const (
	costRootContext costSection = "root_context"
	costDelegable   costSection = "delegable"
	costSpawns      costSection = "spawns"
	costAdvisories  costSection = "advisories"
	costShadow      costSection = "shadow"
	costDollars     costSection = "dollars"
)

// costSections are the sections in the order they print.
var costSections = []costSection{costRootContext, costDelegable, costSpawns, costAdvisories, costShadow, costDollars}

// label is how the text view names s.
func (s costSection) label() string { return strings.ReplaceAll(string(s), "_", " ") }

// tokens counts model tokens and chars counts tool-result characters. They print alike,
// and the two types keep one from being summed into or printed as the other.
type (
	tokens int64
	chars  int64
)

func (n tokens) String() string { return abbreviate(int64(n)) }

func (n chars) String() string { return abbreviate(int64(n)) }

// costSpawn is one subagent the session started.
type costSpawn struct {
	At    time.Time
	Model string
}

// sessionCost is the cost half of `magus session show`. Every section is a pointer or a
// slice that is absent when it could not be measured, and NotMeasured says why, so an
// unmeasured section never reads as a measured zero.
type sessionCost struct {
	RootContext *rootContext           `json:"root_context,omitempty"`
	Delegable   *delegableShare        `json:"delegable,omitempty"`
	Spawns      *spawnTally            `json:"spawns,omitempty"`
	Advisories  *advisoryOutcome       `json:"advisories,omitempty"`
	Shadow      []shadowTally          `json:"shadow,omitempty"`
	Dollars     *dollarEstimate        `json:"dollars,omitempty"`
	NotMeasured map[costSection]string `json:"not_measured,omitempty"`
}

// rootContext is the prompt size the main thread sent across its calls.
type rootContext struct {
	Peak    tokens   `json:"peak"`
	P50     tokens   `json:"p50"`
	Samples int      `json:"samples"`
	Models  []string `json:"models,omitempty"`
}

// delegableShare is the part of the main thread's tool-result characters that came from
// work a subagent could have done in its place. ByClass counts characters.
type delegableShare struct {
	Chars   chars         `json:"chars"`
	Total   chars         `json:"total"`
	ByClass []countedName `json:"by_class,omitempty"`
}

// Share is Chars as a fraction of Total, 0 when every observed result was empty.
func (d delegableShare) Share() float64 {
	if d.Total == 0 {
		return 0
	}
	return float64(d.Chars) / float64(d.Total)
}

// spawnTally counts the session's spawns by the model each declared, "" for none.
type spawnTally struct {
	Total   int           `json:"total"`
	ByModel []countedName `json:"by_model,omitempty"`
}

// advisoryOutcome counts the cost advisories the main thread was given and how many it
// answered with a spawn within Within calls.
type advisoryOutcome struct {
	Given    int `json:"given"`
	Followed int `json:"followed"`
	Within   int `json:"within"`
}

// Ignored is how many advisories no spawn answered.
func (a advisoryOutcome) Ignored() int { return a.Given - a.Followed }

// shadowTally is what one profile would have decided across the main thread's calls,
// whatever profile was in force, counted by decision.
type shadowTally struct {
	Profile   types.GuardProfile          `json:"profile"`
	Decisions map[types.GuardDecision]int `json:"decisions"`
}

// String renders t as its profile and a count of every decision: allow, advise and deny
// always, then any other decision recorded.
func (t shadowTally) String() string {
	declared := types.GuardDecision("").Values()
	counts := make([]string, 0, len(declared)+len(t.Decisions))
	for _, d := range declared {
		counts = append(counts, fmt.Sprintf("%d %s", t.Decisions[types.GuardDecision(d)], d))
	}
	for _, d := range slices.Sorted(maps.Keys(t.Decisions)) {
		if !slices.Contains(declared, string(d)) {
			counts = append(counts, fmt.Sprintf("%d %s", t.Decisions[d], d))
		}
	}
	return fmt.Sprintf("%s %s", t.Profile, strings.Join(counts, "/"))
}

// dollarEstimate is the cache-read price of the main thread's context alone: a floor,
// since it leaves out cache writes, uncached input, output and every subagent.
type dollarEstimate struct {
	Billing  types.AgentBilling `json:"billing"`
	Calls    int                `json:"calls"`
	FloorUSD float64            `json:"floor_usd"`
}

const noCallFacts = "the trail holds no per-call facts for this session"

// summarizeCost folds one session's trail into the cost sections. tr is nil when this
// checkout's trail holds nothing for the session, in which case no section is measured.
// tr is not modified.
func summarizeCost(tr *trail.SessionTrail, table model.Table) *sessionCost {
	c := &sessionCost{NotMeasured: map[costSection]string{}}
	if tr == nil {
		for _, s := range costSections {
			c.NotMeasured[s] = "this checkout's trail holds nothing for the session"
		}
		return c
	}
	spawns := sessionSpawns(tr)
	c.Spawns = spawnsByModel(spawns)

	// The root is the main thread; a call of unknown thread is never counted as the root's.
	calls := slices.Clone(tr.Calls)
	slices.SortStableFunc(calls, func(a, b trail.SessionCall) int { return a.At.Compare(b.At) })
	var main []trail.SessionCall
	unknown := 0
	for _, call := range calls {
		switch call.Thread {
		case types.AgentThreadMain:
			main = append(main, call)
		case types.AgentThreadUnknown:
			unknown++
		}
	}
	noMain := noCallFacts
	if len(calls) > 0 && len(main) == 0 {
		noMain = fmt.Sprintf("no call is known to be on the main thread (%d of unknown thread)", unknown)
	}

	c.RootContext = buildRootContext(main)
	switch {
	case c.RootContext != nil:
	case len(main) == 0:
		c.NotMeasured[costRootContext] = noMain
	default:
		c.NotMeasured[costRootContext] = "no main-thread call reported a context size"
	}

	c.Delegable = sumDelegable(main)
	switch {
	case c.Delegable != nil:
	case len(main) == 0:
		c.NotMeasured[costDelegable] = noMain
	default:
		c.NotMeasured[costDelegable] = "no main-thread call carries a result size"
	}

	if len(main) > 0 {
		c.Advisories = tallyAdvisories(main, spawns)
	} else {
		c.NotMeasured[costAdvisories] = noMain
	}

	c.Shadow = tallyShadow(main)
	if c.Shadow == nil {
		c.NotMeasured[costShadow] = "no call recorded what each profile would have decided"
	}

	est, why := estimateDollars(main, table)
	c.Dollars = est
	if est == nil {
		c.NotMeasured[costDollars] = why
	}
	if len(c.NotMeasured) == 0 {
		c.NotMeasured = nil
	}
	return c
}

func sessionSpawns(tr *trail.SessionTrail) []costSpawn {
	out := make([]costSpawn, 0, len(tr.Spawns))
	for _, sp := range tr.Spawns {
		out = append(out, costSpawn{At: sp.At, Model: sp.DeclaredModel})
	}
	return out
}

func spawnsByModel(spawns []costSpawn) *spawnTally {
	tally := map[string]int{}
	for _, sp := range spawns {
		tally[sp.Model]++
	}
	return &spawnTally{Total: len(spawns), ByModel: counted(tally)}
}

func buildRootContext(main []trail.SessionCall) *rootContext {
	var sizes []tokens
	models := map[string]bool{}
	for _, call := range main {
		if call.ContextTokens > 0 {
			sizes = append(sizes, tokens(call.ContextTokens))
			if call.ContextModel != "" {
				models[call.ContextModel] = true
			}
		}
	}
	if len(sizes) == 0 {
		return nil
	}
	slices.Sort(sizes)
	rc := &rootContext{Peak: sizes[len(sizes)-1], P50: sizes[(len(sizes)-1)/2], Samples: len(sizes)}
	for m := range models {
		rc.Models = append(rc.Models, m)
	}
	slices.Sort(rc.Models)
	return rc
}

// sumDelegable sums the result sizes the main thread's calls observed, nil when none
// observed one.
func sumDelegable(main []trail.SessionCall) *delegableShare {
	var total, delegable chars
	observed := false
	byClass := map[string]int{}
	for _, call := range main {
		if call.ResultChars == nil {
			continue
		}
		observed = true
		total += chars(*call.ResultChars)
		if call.DelegableClass != "" {
			delegable += chars(*call.ResultChars)
			byClass[string(call.DelegableClass)] += *call.ResultChars
		}
	}
	if !observed {
		return nil
	}
	return &delegableShare{Chars: delegable, Total: total, ByClass: counted(byClass)}
}

func tallyAdvisories(main []trail.SessionCall, spawns []costSpawn) *advisoryOutcome {
	out := &advisoryOutcome{Within: followWithin}
	for i, call := range main {
		if call.Decision != string(types.GuardAdvise) || !slices.Contains(costAdviceRules, call.Rule) {
			continue
		}
		out.Given++
		var deadline time.Time
		if j := i + followWithin; j < len(main) {
			deadline = main[j].At
		}
		if slices.ContainsFunc(spawns, func(sp costSpawn) bool {
			return !sp.At.Before(call.At) && (deadline.IsZero() || !sp.At.After(deadline))
		}) {
			out.Followed++
		}
	}
	return out
}

// tallyShadow tallies each profile's decisions across the main thread's calls. An unset
// decision is an allow, as a rule's zero verdict is.
func tallyShadow(main []trail.SessionCall) []shadowTally {
	byProfile := map[types.GuardProfile]map[types.GuardDecision]int{}
	for _, call := range main {
		for p, d := range call.Shadow {
			if byProfile[p] == nil {
				byProfile[p] = map[types.GuardDecision]int{}
			}
			byProfile[p][cmp.Or(d, types.GuardAllow)]++
		}
	}
	if len(byProfile) == 0 {
		return nil
	}
	out := make([]shadowTally, 0, len(byProfile))
	for p, decisions := range byProfile {
		out = append(out, shadowTally{Profile: p, Decisions: decisions})
	}
	slices.SortFunc(out, func(a, b shadowTally) int { return cmp.Compare(a.Profile.Rank(), b.Profile.Rank()) })
	return out
}

// estimateDollars prices the main thread's context at the cache-read rate, and returns the
// reason instead when it would be a guess: billing that is not metered on every call, or
// a model the table does not know. Consecutive calls reporting the same context are one
// model call that ran several tools, so they are priced once.
func estimateDollars(main []trail.SessionCall, table model.Table) (*dollarEstimate, string) {
	var samples []trail.SessionCall
	for _, call := range main {
		if call.ContextTokens > 0 {
			samples = append(samples, call)
		}
	}
	if len(samples) == 0 {
		return nil, "no main-thread call reported a context size"
	}
	for _, call := range samples {
		switch call.Billing {
		case types.AgentBillingMetered:
		case "":
			return nil, "billing is unknown: set agents.billing in magus.yaml"
		default:
			return nil, fmt.Sprintf("billing is %s, which has no per-token price", call.Billing)
		}
	}
	est := &dollarEstimate{Billing: types.AgentBillingMetered}
	var prev trail.SessionCall
	for i, call := range samples {
		pricing, ok := table.Lookup(call.ContextModel)
		if !ok || pricing.CacheRead == 0 {
			return nil, fmt.Sprintf("model %q is not in the model table: add it under agents.models in magus.yaml", call.ContextModel)
		}
		if i > 0 && call.ContextTokens == prev.ContextTokens && call.ContextModel == prev.ContextModel {
			continue
		}
		prev = call
		est.Calls++
		est.FloorUSD += float64(call.ContextTokens) / 1e6 * float64(pricing.CacheRead) * pricing.PriceMultiplier(call.ContextTokens)
	}
	return est, ""
}

func renderCost(w io.Writer, c *sessionCost) {
	fmt.Fprintln(w, "\nCost, with the root as the main thread:")
	line := func(s costSection, text string) {
		if why, no := c.NotMeasured[s]; no {
			fmt.Fprintf(w, "  %-16s not measured: %s\n", s.label(), why)
			return
		}
		fmt.Fprintf(w, "  %-16s %s\n", s.label(), text)
	}
	if rc := c.RootContext; rc != nil {
		line(costRootContext, fmt.Sprintf("peak %s, p50 %s over %d call(s)%s", rc.Peak, rc.P50, rc.Samples, modelSuffix(rc.Models)))
	} else {
		line(costRootContext, "")
	}
	if d := c.Delegable; d != nil {
		text := fmt.Sprintf("%.0f%% of %s result chars", d.Share()*100, d.Total)
		if len(d.ByClass) > 0 {
			parts := make([]string, 0, len(d.ByClass))
			for _, k := range d.ByClass {
				parts = append(parts, fmt.Sprintf("%s %s", k.Name, chars(k.Count)))
			}
			text += " (" + strings.Join(parts, ", ") + ")"
		}
		line(costDelegable, text)
	} else {
		line(costDelegable, "")
	}
	if sp := c.Spawns; sp != nil {
		text := "none"
		if sp.Total > 0 {
			parts := make([]string, 0, len(sp.ByModel))
			for _, m := range sp.ByModel {
				parts = append(parts, fmt.Sprintf("%s %d", cmp.Or(m.Name, "none declared"), m.Count))
			}
			text = fmt.Sprintf("%d: %s", sp.Total, strings.Join(parts, ", "))
		}
		line(costSpawns, text)
	} else {
		line(costSpawns, "")
	}
	if a := c.Advisories; a != nil {
		line(costAdvisories, fmt.Sprintf("%d given, %d followed by a spawn within %d calls, %d ignored", a.Given, a.Followed, a.Within, a.Ignored()))
	} else {
		line(costAdvisories, "")
	}
	if len(c.Shadow) > 0 {
		parts := make([]string, 0, len(c.Shadow))
		for _, t := range c.Shadow {
			parts = append(parts, t.String())
		}
		line(costShadow, strings.Join(parts, "; "))
	} else {
		line(costShadow, "")
	}
	if d := c.Dollars; d != nil {
		line(costDollars, fmt.Sprintf("$%.2f floor: cache reads of the root's %d model call(s), metered", d.FloorUSD, d.Calls))
	} else {
		line(costDollars, "")
	}
}

func modelSuffix(models []string) string {
	if len(models) == 0 {
		return ""
	}
	return ", " + strings.Join(models, ", ")
}

// abbreviate renders n as 1.2M or 612k, exact below a thousand.
func abbreviate(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%dk", n/1_000)
	}
	return fmt.Sprint(n)
}
