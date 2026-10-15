// Package builtin holds the decision each compiled guard rule takes when the workspace
// declares nothing for it, and resolves what a workspace does declare against that set.
//
// It imports only leaves (stdlib and types) so the guard, the Buzz binding and the CLI can
// all read one table without importing the guard engine.
package builtin

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/egladman/magus/types"
)

// Decision is what a built-in rule does when it matches: say nothing, explain, or refuse.
type Decision string

// The decisions, least to most refusing.
const (
	Off    Decision = "off"
	Advise Decision = "advise"
	Deny   Decision = "deny"
)

// Setting is one rule's resolved configuration. Each parameter is a threshold the rule
// fires at, taken only by the rules params lists for it; 0 means unset, and the binary
// ships no number for it.
type Setting struct {
	Decision Decision `json:"decision"`
	// Lines is the size of a whole read worth flagging.
	Lines int `json:"lines"`
	// Tokens is a context size in tokens.
	Tokens int `json:"tokens"`
	// Calls is a count of calls in a row.
	Calls int `json:"calls"`
	// Chars is the size of one tool result in characters.
	Chars int `json:"chars"`
	// Markers are text a rule notices a write adding, each matched literally. Resolve
	// fills the rule's own list when none is declared.
	Markers []string `json:"markers"`
	// Profile is the level whose row gave this setting, empty for one the workspace
	// declared or a compiled default. A rule whose figures depend on the level and the
	// account's billing reads it when no parameter is set.
	Profile types.GuardProfile `json:"profile"`
}

const (
	compatMarker           = "compat-marker"
	modelTierContextCap    = "model-tier-context-cap"
	orchestratorGroundwork = "orchestrator-groundwork"
	readNavigation         = "read-navigation"
	rootContextBudget      = "root-context-budget"
	rootHeavyCommand       = "root-heavy-command"
	rootResultCap          = "root-result-cap"
	rootRepeat             = "root-repeat"
	spawnNamesModel        = "spawn-names-model"
	spawnAboveModelTier    = "spawn-above-model-tier"
	spawnAboveParentTier   = "spawn-above-parent-tier"
	stall                  = "stall"
)

// paramSpec is one numeric parameter: its name as magus\guard.builtins spells it, the
// lowest value that loads, and the Setting field that holds it. No parameter has a
// ceiling: only the workspace's model rows know the largest window, and they load later.
type paramSpec struct {
	name  string
	min   int
	field func(*Setting) *int
}

// paramSpecs are the numeric parameters, in the order validate reports them.
var paramSpecs = []paramSpec{
	{"lines", 1, func(s *Setting) *int { return &s.Lines }},
	{"tokens", 1, func(s *Setting) *int { return &s.Tokens }},
	{"calls", 2, func(s *Setting) *int { return &s.Calls }},
	{"chars", 1000, func(s *Setting) *int { return &s.Chars }},
}

// SetParam sets the numeric parameter name to n, reporting false for a name that is no
// numeric parameter. Which rules take it, and its floor, are Resolve's to judge.
func (s *Setting) SetParam(name string, n int) bool {
	for _, p := range paramSpecs {
		if p.name == name {
			*p.field(s) = n
			return true
		}
	}
	return false
}

// params maps each rule that takes a parameter to the parameters it takes: a paramSpecs
// name, or markers.
var params = map[string][]string{
	compatMarker:      {"markers"},
	readNavigation:    {"lines"},
	rootContextBudget: {"tokens"},
	rootHeavyCommand:  {"calls"},
	rootResultCap:     {"chars"},
	rootRepeat:        {"calls"},
	stall:             {"calls"},
}

// neverDeny names the rules that only ever explain, so a deny declared for one is a
// misdeclaration rather than a refusal nobody would see.
var neverDeny = map[string]string{
	compatMarker:        "whether code stays for an old caller is the root's call, and refusing the write would not ask it",
	rootContextBudget:   "size only tunes advice; refusing a session's groundwork is orchestrator-groundwork's, by its role",
	rootHeavyCommand:    "it nudges a session that has handed out no work; refusing an orchestrator's groundwork is orchestrator-groundwork's",
	rootRepeat:          "a repeat is advice to a session that has handed out no work; an orchestrator's repeat is groundwork, which orchestrator-groundwork refuses",
	rootResultCap:       "it reads a result the call already returned",
	spawnAboveModelTier: "work can outgrow the tier its row was forked at, and only the root can tell when it has",
	stall:               "it routes a block to the root and refuses nothing itself",
}

// profiles maps each level to the settings it gives the rules it names. A level leaves
// every other rule at its default, and a rule magus\guard.builtins names overrides the
// level's setting for it. The figures each level fires at belong to the rules, which read
// them by the level ResolveProfile stamps on the setting.
var profiles = map[types.GuardProfile]map[string]Setting{
	types.GuardProfileOff: {},
	types.GuardProfileLight: {
		rootContextBudget: {Decision: Advise},
		rootResultCap:     {Decision: Advise},
		stall:             {Decision: Advise},
	},
	types.GuardProfileStandard: {
		rootContextBudget:   {Decision: Advise},
		rootHeavyCommand:    {Decision: Advise},
		rootResultCap:       {Decision: Advise},
		rootRepeat:          {Decision: Advise},
		spawnNamesModel:     {Decision: Advise},
		spawnAboveModelTier: {Decision: Advise},
		stall:               {Decision: Advise},
	},
	types.GuardProfileStrict: {
		rootContextBudget:   {Decision: Advise},
		rootHeavyCommand:    {Decision: Advise},
		rootResultCap:       {Decision: Advise},
		rootRepeat:          {Decision: Advise},
		spawnNamesModel:     {Decision: Deny},
		spawnAboveModelTier: {Decision: Advise},
		stall:               {Decision: Advise},
	},
}

// defaults must name exactly the rules internal/guard catalogs. A rule that refuses
// something the caller can recover from by running a different command advises, so
// a workspace that wants it refused says so; a rule guarding something no later
// command undoes, or a boundary a lease or person drew, denies.
var defaults = map[string]Decision{
	// Recoverable: the served command answers what the refused one asked.
	"architecture-unbriefed": Advise,
	"brief-command":          Advise,
	"brief-plain-commands":   Advise,
	"busy-wait":              Advise,
	"buzz-unbriefed":         Advise,
	"chained-run":            Advise,
	"exit-status-echo":       Advise,
	"filter-without-input":   Advise,
	"grep-reader":            Advise,
	"interpreter-rewrite":    Advise,
	"magus-timeout":          Advise,
	"monitor-duplicate":      Advise,
	"monitor-pile":           Advise,
	"output-pipe":            Advise,
	"output-redirect":        Advise,
	"process-poll":           Advise,
	"raw-tool":               Advise,
	readNavigation:           Advise,
	"read-paging":            Advise,
	"scripted-rewrite":       Advise,
	"search-translation":     Advise,
	"sed-in-place":           Advise,
	"sibling-checkout":       Advise,
	"sleep-poll":             Advise,
	"spawn-unbriefed":        Advise,
	"spawn-waiter":           Advise,
	"stage-all":              Advise,
	"symbol-search":          Advise,
	"throwaway-copy":         Advise,
	"unjudged-tool":          Advise,
	"unknown-env":            Advise,

	// Irreversible, or a boundary someone else drew.
	"agent-sign-off":        Deny,
	"background-left":       Deny,
	"background-unbounded":  Deny,
	"backtick-substitution": Deny,
	"cache-dir-write":       Deny,
	"claimed-declaration":   Deny,
	"credential-verb":       Deny,
	"focus-read":            Deny,
	"heredoc-write":         Deny,
	"hook-wiring-write":     Deny,
	"hooks-off":             Deny,
	"human-only":            Deny,
	"inline-alias":          Deny,
	"lease-gate":            Deny,
	"lease-harness":         Deny,
	"lease-rebind":          Deny,
	"lease-undeclared":      Deny,
	"lease-vcs":             Deny,
	"lease-write":           Deny,
	"conflict-rewrite":      Deny,
	"merge-side-checkout":   Deny,
	"notes-author":          Deny,
	"policy-unloaded":       Deny,
	"push-ungated":          Deny,
	"shared-stash":          Deny,
	"spawn-duplicate":       Deny,
	spawnAboveParentTier:    Deny,
	"stale-binary":          Deny,
	"token-state":           Deny,
	"vcs-off-switch":        Deny,
	"whole-tree":            Deny,
	"worker-background-run": Deny,
	"worker-check-only":     Deny,
	"worktree-remove":       Deny,

	// Advisories never refused anything, so they keep advising; a workspace may still
	// raise one to deny or turn it off. lease-state, lease-invalid and lease-terminal are
	// advisories, not the lease-* denies above.
	"capture-filter":      Advise,
	"check-timeout":       Advise,
	compatMarker:          Advise,
	"checkpoint-state":    Advise,
	"dependency-install":  Advise,
	"dependency-update":   Advise,
	"echo-on-success":     Advise,
	"focus":               Advise,
	"gate-repeat":         Advise,
	"generated-write":     Advise,
	"graph-pipe":          Advise,
	"graph-stale":         Advise,
	"hook-wiring":         Advise,
	"installed-skill":     Advise,
	"instruction-write":   Advise,
	"lease-invalid":       Advise,
	"lease-state":         Advise,
	"lease-terminal":      Advise,
	"leased-path":         Advise,
	modelTierContextCap:   Advise,
	"new-file":            Advise,
	"new-source-dir":      Advise,
	"precedent-search":    Advise,
	"push-gate":           Advise,
	"read-symbol":         Advise,
	"regen-source":        Advise,
	"revert-classify":     Advise,
	"scope-drift":         Advise,
	"shared-checkout":     Advise,
	"skill-source":        Advise,
	"source-read":         Advise,
	"spawn-nested-waiter": Advise,
	"split-run":           Advise,
	"stage-classify":      Advise,
	"timed-magus":         Advise,
	"unleased-write":      Advise,

	// A session's role, not its size or level, decides whether its groundwork is refused,
	// so no level names this rule and the off level leaves it on. It judges only a session
	// magus recorded handing out work: proof refuses, an inference advises.
	orchestratorGroundwork: Deny,

	// The cost rules and the stall rule are off until a level turns them on, and the
	// level a workspace gets when it declares none is light.
	rootContextBudget:   Off,
	rootHeavyCommand:    Off,
	rootResultCap:       Off,
	rootRepeat:          Off,
	spawnNamesModel:     Off,
	spawnAboveModelTier: Off,
	stall:               Off,

	"guard-unfinished":         Advise,
	"workspace-command-failed": Advise,
	"workspace-spawn-failed":   Advise,
	"workspace-write-failed":   Advise,
}

// defaultMarkers are what a rule taking markers notices when the workspace names none: the
// labels code kept for older data or callers carries.
var defaultMarkers = map[string][]string{
	compatMarker: {"compat(until:", "Deprecated:"},
}

// fixed maps each rule a workspace may not set, with any value, to why. Its entries are
// notices that report on the workspace's own rules, so a setting would let a workspace
// silence the report or promote it to a refusal. Every name is also in defaults.
var fixed = map[string]string{
	"guard-unfinished":         "a guard check",
	"workspace-command-failed": "a command rule",
	"workspace-spawn-failed":   "a spawn rule",
	"workspace-write-failed":   "a write rule",
}

// removal is why a rule the guard no longer has under its name is gone, and the name it
// now has when it was renamed rather than dropped.
type removal struct {
	replacement string
	why         string
}

// removed maps each rule the guard no longer has under that name to its removal. A
// workspace that still names one gets that reason rather than a guess at the nearest live
// name.
//
// compat(until: no workspace's guard.builtins still names a removed rule): the entry
// exists only to name the removal in the load error.
var removed = map[string]removal{
	"stdin-closed": {
		why: "the guard no longer rewrites a shell command to close its stdin, since the hosts that need it already do",
	},
	"spawn-principal-for-economy": {
		replacement: spawnAboveModelTier,
		why:         "it compares any two model tiers by rank, not only principal against economy",
	},
}

// Fixed reports whether name is a rule a workspace may not set.
func Fixed(name string) bool {
	_, ok := fixed[name]
	return ok
}

// Verdicts returns the verdicts rule name can give under any setting a workspace may
// declare, least refusing first: advise alone for a rule that never refuses or that a
// workspace may not set, advise and deny for any other.
func Verdicts(name string) []types.GuardDecision {
	if _, never := neverDeny[name]; never || Fixed(name) {
		return []types.GuardDecision{types.GuardAdvise}
	}
	return []types.GuardDecision{types.GuardAdvise, types.GuardDeny}
}

// Defaults returns every compiled rule's name mapped to the decision it takes when the
// workspace declares nothing for it. The map is the caller's to modify.
func Defaults() map[string]Decision {
	return maps.Clone(defaults)
}

// Resolve returns a setting for every compiled rule: the declared one where the workspace
// declared it, the default otherwise. Every misdeclaration is reported at once, each as an
// MGS1045 error: a name no rule has (naming the nearest one), a rule that is fixed, a
// decision other than off, advise or deny, a parameter on a rule that takes no such
// parameter, or a parameter out of its range (see validate). declared takes no profile;
// expand one with ResolveProfile first.
func Resolve(declared map[string]Setting) (map[string]Setting, error) {
	out := make(map[string]Setting, len(defaults))
	for name, d := range defaults {
		out[name] = Setting{Decision: d, Markers: slices.Clone(defaultMarkers[name])}
	}
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(declared)) {
		s := declared[name]
		if err := validate(name, s); err != nil {
			errs = append(errs, err)
			continue
		}
		if len(s.Markers) == 0 {
			s.Markers = slices.Clone(defaultMarkers[name])
		}
		out[name] = s
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

func validate(name string, s Setting) error {
	if _, ok := defaults[name]; !ok {
		if r, gone := removed[name]; gone {
			if r.replacement != "" {
				return types.DiagnosticErrorf(types.GuardRuleMisdeclared,
					"built-in rule %q was renamed to %q; declare it under that name", name, r.replacement).WithWhy(r.why)
			}
			return types.DiagnosticErrorf(types.GuardRuleMisdeclared,
				"built-in rule %q was removed; delete it from the declaration", name).WithWhy(r.why)
		}
		if near := nearest(name); near != "" {
			return types.DiagnosticErrorf(types.GuardRuleMisdeclared,
				"unknown built-in rule %q; did you mean %q?", name, near)
		}
		return types.DiagnosticErrorf(types.GuardRuleMisdeclared,
			"unknown built-in rule %q; `magus describe rules` lists every name", name)
	}
	if what, ok := fixed[name]; ok {
		return types.DiagnosticErrorf(types.GuardRuleMisdeclared,
			"built-in rule %q cannot be set; remove it from the declaration", name).
			WithWhy("it is how a workspace learns that " + what + " of its own judged nothing, so it can be neither silenced nor promoted")
	}
	if rank(s.Decision) < 0 {
		return types.DiagnosticErrorf(types.GuardRuleMisdeclared,
			"built-in rule %q: decision %q is not off, advise or deny", name, s.Decision)
	}
	if why, ok := neverDeny[name]; ok && s.Decision == Deny {
		return types.DiagnosticErrorf(types.GuardRuleMisdeclared,
			"built-in rule %q never refuses; give off or advise", name).WithWhy(why)
	}
	for _, p := range paramSpecs {
		value := *p.field(&s)
		if value == 0 {
			continue
		}
		if !slices.Contains(params[name], p.name) {
			return takesNo(name, p.name)
		}
		if value < p.min {
			return types.DiagnosticErrorf(types.GuardRuleMisdeclared,
				"built-in rule %q: %s is %d; give %d or more, or leave it out", name, p.name, value, p.min)
		}
	}
	if len(s.Markers) > 0 && !slices.Contains(params[name], "markers") {
		return takesNo(name, "markers")
	}
	if slices.ContainsFunc(s.Markers, func(m string) bool { return strings.TrimSpace(m) == "" }) {
		return types.DiagnosticErrorf(types.GuardRuleMisdeclared,
			"built-in rule %q: a marker is blank; give the text to notice, or leave markers out", name)
	}
	return nil
}

// takesNo is the misdeclaration of param on a rule that does not take it, naming every
// rule that does.
func takesNo(name, param string) error {
	var takers []string
	for rule, ps := range params {
		if slices.Contains(ps, param) {
			takers = append(takers, rule)
		}
	}
	slices.Sort(takers)
	if len(takers) == 1 {
		return types.DiagnosticErrorf(types.GuardRuleMisdeclared,
			"built-in rule %q takes no %s parameter; only %s does", name, param, takers[0])
	}
	return types.DiagnosticErrorf(types.GuardRuleMisdeclared,
		"built-in rule %q takes no %s parameter; only %s do", name, param, strings.Join(takers, ", "))
}

// Stricter returns whichever of a and b refuses more, ranking off < advise < deny. At the
// same decision each parameter takes the lower threshold of the two, since a lower one
// fires sooner. An unset Lines beats any set one, since unset judges every whole read
// rather than only the long ones; any other unset parameter yields to a set one, since
// unset defers to the rule's own figure. Of two levels the higher-ranked one stands, since
// its figures are the lower.
func Stricter(a, b Setting) Setting {
	ra, rb := rank(a.Decision), rank(b.Decision)
	switch {
	case ra > rb:
		return a
	case rb > ra:
		return b
	}
	lower := func(x, y int) int {
		if x == 0 || (y != 0 && y < x) {
			return y
		}
		return x
	}
	out := a
	if a.Lines != 0 && (b.Lines == 0 || b.Lines < a.Lines) {
		out.Lines = b.Lines
	}
	out.Tokens = lower(a.Tokens, b.Tokens)
	out.Calls = lower(a.Calls, b.Calls)
	out.Chars = lower(a.Chars, b.Chars)
	out.Markers = slices.Clone(a.Markers)
	for _, m := range b.Markers {
		if !slices.Contains(out.Markers, m) {
			out.Markers = append(out.Markers, m)
		}
	}
	if b.Profile != "" && (a.Profile == "" || b.Profile.Rank() > a.Profile.Rank()) {
		out.Profile = b.Profile
	}
	return out
}

// ResolveProfile expands profile p into the declaration Resolve takes: the settings p
// gives its rules, each stamped with p's level, with declared applied over them rule by
// rule. Unset p is light. An unknown p is an MGS1045 error. Neither map is modified.
func ResolveProfile(p types.GuardProfile, declared map[string]Setting) (map[string]Setting, error) {
	level := p.Effective()
	row, ok := profiles[level]
	if !ok {
		return nil, types.DiagnosticErrorf(types.GuardRuleMisdeclared,
			"unknown guard profile %q; give one of %s", string(p), strings.Join(p.Values(), ", "))
	}
	out := make(map[string]Setting, len(row)+len(declared))
	for name, s := range row {
		s.Profile = level
		out[name] = s
	}
	maps.Copy(out, declared)
	return out, nil
}

// rank orders decisions by how much they refuse; an unknown one ranks below off.
func rank(d Decision) int {
	return slices.Index([]Decision{Off, Advise, Deny}, d)
}

// nearest is the rule name within a typo's reach of typed, or "". The tolerance matches
// internal/hint.Nearest, which this leaf cannot import.
func nearest(typed string) string {
	limit := 2
	if utf8.RuneCountInString(typed) >= 8 {
		limit = 3
	}
	folded := strings.ToLower(typed)
	best, bestDist := "", limit+1
	for _, name := range slices.Sorted(maps.Keys(defaults)) {
		if d := distance(folded, name); d < bestDist {
			best, bestDist = name, d
		}
	}
	return best
}

// distance is the Levenshtein distance between a and b, counted in runes.
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
