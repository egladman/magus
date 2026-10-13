package main

import (
	"cmp"
	"fmt"
	"io"
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
	"spawn-principal-for-economy",
}

// The cost sections, in the order they print. A section nobody could measure is listed
// under sessionCost.NotMeasured by these keys instead of being left out or zeroed.
const (
	costRootContext = "root_context"
	costDelegable   = "delegable"
	costSpawns      = "spawns"
	costAdvisories  = "advisories"
	costShadow      = "shadow"
	costPriceStep   = "price_step"
	costDollars     = "dollars"
)

var costSectionLabels = []struct{ key, label string }{
	{costRootContext, "root context"},
	{costDelegable, "delegable"},
	{costSpawns, "spawns"},
	{costAdvisories, "advisories"},
	{costShadow, "shadow"},
	{costPriceStep, "past price step"},
	{costDollars, "dollars"},
}

// costCall is one guard observation of a call, with the cost facts recorded beside it.
// The root is the main thread; a call of unknown thread is never counted as the root's.
type costCall struct {
	At             time.Time
	Thread         types.AgentThread
	Decision       string
	Rule           string
	ContextTokens  int64
	ContextModel   string
	Billing        types.AgentBilling
	DelegableClass string
	ResultChars    int
	Shadow         map[types.GuardProfile]types.GuardDecision
}

// costSpawn is one subagent the session started. ContextTokens is its last reported
// context, 0 when none was.
type costSpawn struct {
	At            time.Time
	Model         string
	ContextTokens int64
}

// sessionCost is the cost half of `magus session show`. Every section is a pointer or a
// slice that is absent when it could not be measured, and NotMeasured says why, so an
// unmeasured section never reads as a measured zero.
type sessionCost struct {
	RootContext   *rootContext      `json:"root_context,omitempty"`
	Delegable     *delegableShare   `json:"delegable,omitempty"`
	Spawns        *spawnTally       `json:"spawns,omitempty"`
	Advisories    *advisoryOutcome  `json:"advisories,omitempty"`
	Shadow        []shadowTally     `json:"shadow,omitempty"`
	PastPriceStep *int              `json:"past_price_step,omitempty"`
	Dollars       *dollarEstimate   `json:"dollars,omitempty"`
	NotMeasured   map[string]string `json:"not_measured,omitempty"`
}

// rootContext is the prompt size the main thread sent across its calls.
type rootContext struct {
	Peak    int64    `json:"peak"`
	P50     int64    `json:"p50"`
	Samples int      `json:"samples"`
	Models  []string `json:"models,omitempty"`
}

// delegableShare is the part of the main thread's tool-result characters that came from
// work a subagent could have done in its place.
type delegableShare struct {
	Chars   int64         `json:"chars"`
	Total   int64         `json:"total"`
	Share   float64       `json:"share"`
	ByClass []countedName `json:"by_class,omitempty"`
}

type spawnTally struct {
	Total   int           `json:"total"`
	ByModel []countedName `json:"by_model,omitempty"`
}

// advisoryOutcome counts the cost advisories the main thread was given and how many it
// answered with a spawn within Within calls.
type advisoryOutcome struct {
	Given    int `json:"given"`
	Followed int `json:"followed"`
	Ignored  int `json:"ignored"`
	Within   int `json:"within"`
}

// shadowTally is what one profile would have decided across the main thread's calls,
// whatever profile was in force.
type shadowTally struct {
	Profile types.GuardProfile `json:"profile"`
	Allow   int                `json:"allow"`
	Advise  int                `json:"advise"`
	Deny    int                `json:"deny"`
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
func summarizeCost(tr *trail.SessionTrail, calls []costCall, table model.Table) *sessionCost {
	c := &sessionCost{NotMeasured: map[string]string{}}
	if tr == nil {
		for _, s := range costSectionLabels {
			c.NotMeasured[s.key] = "this checkout's trail holds nothing for the session"
		}
		return c
	}
	spawns := spawnsOf(tr)
	c.Spawns = spawnsByModel(spawns)

	slices.SortStableFunc(calls, func(a, b costCall) int { return a.At.Compare(b.At) })
	var main []costCall
	unknown := 0
	for _, call := range calls {
		switch call.Thread {
		case types.AgentThreadMain:
			main = append(main, call)
		case types.AgentThreadUnknown, "":
			unknown++
		}
	}
	noMain := noCallFacts
	if len(calls) > 0 && len(main) == 0 {
		noMain = fmt.Sprintf("no call is known to be on the main thread (%d of unknown thread)", unknown)
	}

	c.RootContext = rootContextOf(main)
	switch {
	case c.RootContext != nil:
	case len(main) == 0:
		c.NotMeasured[costRootContext] = noMain
	default:
		c.NotMeasured[costRootContext] = "no main-thread call reported a context size"
	}

	c.Delegable = delegableOf(main)
	switch {
	case c.Delegable != nil:
	case len(main) == 0:
		c.NotMeasured[costDelegable] = noMain
	default:
		c.NotMeasured[costDelegable] = "no main-thread call carries a result size"
	}

	if len(main) > 0 {
		c.Advisories = advisoriesOf(main, spawns)
	} else {
		c.NotMeasured[costAdvisories] = noMain
	}

	c.Shadow = shadowOf(main)
	if c.Shadow == nil {
		c.NotMeasured[costShadow] = "no call recorded what each profile would have decided"
	}

	c.PastPriceStep = pastPriceStep(spawns, table)
	if c.PastPriceStep == nil {
		c.NotMeasured[costPriceStep] = "no spawn recorded its final context size"
	}

	est, why := dollarsOf(main, table)
	c.Dollars = est
	if est == nil {
		c.NotMeasured[costDollars] = why
	}
	if len(c.NotMeasured) == 0 {
		c.NotMeasured = nil
	}
	return c
}

func spawnsOf(tr *trail.SessionTrail) []costSpawn {
	out := make([]costSpawn, 0, len(tr.Spawns))
	for _, sp := range tr.Spawns {
		out = append(out, costSpawn{At: sp.At, Model: sp.DeclaredModel})
	}
	return out
}

func spawnsByModel(spawns []costSpawn) *spawnTally {
	tally := map[string]int{}
	for _, sp := range spawns {
		tally[cmp.Or(sp.Model, "none declared")]++
	}
	return &spawnTally{Total: len(spawns), ByModel: counted(tally)}
}

func rootContextOf(main []costCall) *rootContext {
	var sizes []int64
	models := map[string]bool{}
	for _, call := range main {
		if call.ContextTokens > 0 {
			sizes = append(sizes, call.ContextTokens)
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

func delegableOf(main []costCall) *delegableShare {
	var total, delegable int64
	byClass := map[string]int{}
	for _, call := range main {
		total += int64(call.ResultChars)
		if call.DelegableClass != "" {
			delegable += int64(call.ResultChars)
			byClass[call.DelegableClass] += call.ResultChars
		}
	}
	if total == 0 {
		return nil
	}
	return &delegableShare{Chars: delegable, Total: total, Share: float64(delegable) / float64(total), ByClass: counted(byClass)}
}

func advisoriesOf(main []costCall, spawns []costSpawn) *advisoryOutcome {
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
	out.Ignored = out.Given - out.Followed
	return out
}

func shadowOf(main []costCall) []shadowTally {
	byProfile := map[types.GuardProfile]*shadowTally{}
	for _, call := range main {
		for p, d := range call.Shadow {
			t := byProfile[p]
			if t == nil {
				t = &shadowTally{Profile: p}
				byProfile[p] = t
			}
			switch d {
			case types.GuardDeny:
				t.Deny++
			case types.GuardAdvise:
				t.Advise++
			default:
				t.Allow++
			}
		}
	}
	if len(byProfile) == 0 {
		return nil
	}
	out := make([]shadowTally, 0, len(byProfile))
	for _, t := range byProfile {
		out = append(out, *t)
	}
	slices.SortFunc(out, func(a, b shadowTally) int { return cmp.Compare(a.Profile.Rank(), b.Profile.Rank()) })
	return out
}

// pastPriceStep counts children whose final context crossed their model's price step,
// nil when no child reported a context size to compare.
func pastPriceStep(spawns []costSpawn, table model.Table) *int {
	reported, past := false, 0
	for _, sp := range spawns {
		if sp.ContextTokens == 0 {
			continue
		}
		reported = true
		if facts, ok := table.Lookup(sp.Model); ok && facts.PriceStep > 0 && sp.ContextTokens > facts.PriceStep {
			past++
		}
	}
	if !reported {
		return nil
	}
	return &past
}

// dollarsOf prices the main thread's context at the cache-read rate, and returns the
// reason instead when it would be a guess: billing that is not metered on every call, or
// a model the table does not know. Consecutive calls reporting the same context are one
// model call that ran several tools, so they are priced once.
func dollarsOf(main []costCall, table model.Table) (*dollarEstimate, string) {
	var samples []costCall
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
	var prev costCall
	for i, call := range samples {
		facts, ok := table.Lookup(call.ContextModel)
		if !ok || facts.CacheRead == 0 {
			return nil, fmt.Sprintf("model %q is not in the model table: add it under agents.models in magus.yaml", call.ContextModel)
		}
		if i > 0 && call.ContextTokens == prev.ContextTokens && call.ContextModel == prev.ContextModel {
			continue
		}
		prev = call
		est.Calls++
		est.FloorUSD += float64(call.ContextTokens) / 1e6 * facts.CacheRead * facts.Factor(call.ContextTokens)
	}
	return est, ""
}

func renderCost(w io.Writer, c *sessionCost) {
	fmt.Fprintln(w, "\nCost, with the root as the main thread:")
	line := func(key, text string) {
		label := key
		for _, s := range costSectionLabels {
			if s.key == key {
				label = s.label
			}
		}
		if why, no := c.NotMeasured[key]; no {
			fmt.Fprintf(w, "  %-16s not measured: %s\n", label, why)
			return
		}
		fmt.Fprintf(w, "  %-16s %s\n", label, text)
	}
	if rc := c.RootContext; rc != nil {
		line(costRootContext, fmt.Sprintf("peak %s, p50 %s over %d call(s)%s",
			tokenCount(rc.Peak), tokenCount(rc.P50), rc.Samples, modelSuffix(rc.Models)))
	} else {
		line(costRootContext, "")
	}
	if d := c.Delegable; d != nil {
		text := fmt.Sprintf("%.0f%% of %s result chars", d.Share*100, tokenCount(d.Total))
		if len(d.ByClass) > 0 {
			parts := make([]string, 0, len(d.ByClass))
			for _, k := range d.ByClass {
				parts = append(parts, fmt.Sprintf("%s %s", k.Name, tokenCount(int64(k.Count))))
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
				parts = append(parts, fmt.Sprintf("%s %d", m.Name, m.Count))
			}
			text = fmt.Sprintf("%d: %s", sp.Total, strings.Join(parts, ", "))
		}
		line(costSpawns, text)
	} else {
		line(costSpawns, "")
	}
	if a := c.Advisories; a != nil {
		line(costAdvisories, fmt.Sprintf("%d given, %d followed by a spawn within %d calls, %d ignored", a.Given, a.Followed, a.Within, a.Ignored))
	} else {
		line(costAdvisories, "")
	}
	if len(c.Shadow) > 0 {
		parts := make([]string, 0, len(c.Shadow))
		for _, t := range c.Shadow {
			parts = append(parts, fmt.Sprintf("%s %d allow/%d advise/%d deny", t.Profile, t.Allow, t.Advise, t.Deny))
		}
		line(costShadow, strings.Join(parts, "; "))
	} else {
		line(costShadow, "")
	}
	if c.PastPriceStep != nil {
		line(costPriceStep, fmt.Sprintf("%d child(ren)", *c.PastPriceStep))
	} else {
		line(costPriceStep, "")
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

// tokenCount renders n as 1.2M or 612k, exact below a thousand.
func tokenCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%dk", n/1_000)
	}
	return fmt.Sprint(n)
}
