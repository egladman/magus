package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
)

var errModelTierDeclaration = errors.New("model tier declaration")

// ModelTier is one case of the model-tier enum a workspace declares. Only a
// ModelTierSet hands one out, so a ModelTier always names a declared case; the zero
// value means no tier was assigned.
type ModelTier struct {
	set  *ModelTierSet
	rank int
}

// Name is the case's name as the enum declares it, or "" for the zero value.
func (t ModelTier) Name() string {
	if t.set == nil {
		return ""
	}
	return t.set.cases[t.rank]
}

// Rank is the case's position in the enum, cheapest first, or -1 for the zero value.
func (t ModelTier) Rank() int {
	if t.set == nil {
		return -1
	}
	return t.rank
}

// IsZero reports whether t is unassigned.
func (t ModelTier) IsZero() bool { return t.set == nil }

// Above reports whether t ranks above u. Ranks compare only within one declaration, so
// it errors when the two come from different sets or either is the zero value.
func (t ModelTier) Above(u ModelTier) (bool, error) {
	if t.set == nil || u.set == nil {
		return false, fmt.Errorf("model tiers %q and %q cannot be compared: one is unassigned", t.Name(), u.Name())
	}
	if t.set != u.set {
		return false, fmt.Errorf("model tiers %q (%s) and %q (%s) come from different declarations", t.Name(), t.set.enum, u.Name(), u.set.enum)
	}
	return t.rank > u.rank, nil
}

// Within is nil when t ranks at or below ceiling, the bound a child's tier keeps to its
// parent's. It errors naming both tiers when t ranks above ceiling, and as [ModelTier.Above]
// does when the two cannot be compared.
func (t ModelTier) Within(ceiling ModelTier) error {
	above, err := t.Above(ceiling)
	if err != nil {
		return err
	}
	if above {
		return fmt.Errorf("model tier %s (rank %d) ranks above %s (rank %d)", t.Name(), t.rank, ceiling.Name(), ceiling.rank)
	}
	return nil
}

// MarshalText writes the case name. There is no UnmarshalText: a name becomes a
// ModelTier only through [ModelTierSet.Parse], against the declaration it belongs to.
func (t ModelTier) MarshalText() ([]byte, error) { return []byte(t.Name()), nil }

// ModelTierSet is a validated model-tier declaration. It is immutable and safe for
// concurrent use.
type ModelTierSet struct {
	enum, site string
	cases      []string
	byName     map[string]int
	def        int
	bounds     []ModelTierBounds
	skills     []SkillVariant
	hosts      map[string][]string
}

// ModelTierState is the model-tier declaration a workspace holds: the root
// magusfile's, none when it turned tiers off, or else the shipped default.
// The zero value holds no declaration and no default. Safe for concurrent use.
type ModelTierState struct {
	mu  sync.Mutex
	set *ModelTierSet
	// declared tells a magusfile's nil (tiers off) apart from no
	// declaration, which leaves the tiers to build.
	declared bool
	build    func() (*ModelTierSet, error)
}

// Declare records a magusfile's declaration, replacing one an earlier
// evaluation of the same magusfile recorded. nil records that it turned
// model tiers off, so the default does not apply.
func (s *ModelTierState) Declare(set *ModelTierSet) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.set, s.declared = set, true
}

// SetDefault records how to build the declaration a workspace gets when its
// magusfile declares none. Nothing builds it until [ModelTierState.Tiers].
func (s *ModelTierState) SetDefault(build func() (*ModelTierSet, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.build = build
}

// Tiers returns the declaration, nil when tiers are off or nothing set a
// default. A default that builds is kept. One that fails returns its error,
// and the next call builds again.
func (s *ModelTierState) Tiers() (*ModelTierSet, error) {
	s.mu.Lock()
	if s.declared || s.build == nil {
		defer s.mu.Unlock()
		return s.set, nil
	}
	build := s.build
	s.mu.Unlock()

	// Outside the lock: the build loads a Buzz module, which must not wait
	// on this state. Racing callers build the same set; the first kept wins.
	set, err := build()
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.declared {
		s.set, s.declared = set, true
	}
	return s.set, nil
}

type modelTierStateKey struct{}

// WithModelTierState returns ctx carrying s, the state a magusfile's
// magus\modelTiers declares into.
func WithModelTierState(ctx context.Context, s *ModelTierState) context.Context {
	return context.WithValue(ctx, modelTierStateKey{}, s)
}

// ModelTierStateFromContext returns the state on ctx, or nil.
func ModelTierStateFromContext(ctx context.Context) *ModelTierState {
	s, _ := ctx.Value(modelTierStateKey{}).(*ModelTierState)
	return s
}

// ModelTierDeclaration is the raw declaration a magusfile's Buzz enum and its mapping
// functions produce. Bounds, Skills and each Hosts list carry one entry per case, by rank.
type ModelTierDeclaration struct {
	// Enum is the Buzz enum's name and Site the file:line that declared it.
	Enum, Site string
	// Cases are the enum's case names, cheapest first.
	Cases []string
	// Default is the rank a job gets when neither its row nor a matrix names one.
	Default int
	Bounds  []ModelTierBounds
	Skills  []SkillVariant
	// Hosts maps a harness id to its model alias for each case.
	Hosts map[string][]string
}

// ModelTierBounds caps the jobs at one model tier. A zero field is no cap.
type ModelTierBounds struct {
	// MaxLive is how many jobs at the tier may be live at once.
	MaxLive int
	// MaxContext is the context, in tokens, a leased worker at the tier may reach.
	MaxContext int64
}

// NewModelTierSet validates decl and builds the set. Every problem is reported at
// once: no cases, a duplicate or empty case, a default out of range, a bounds, skills
// or host list whose length differs from the cases, a negative bound, an invalid skill
// variant, an empty host id or alias, or one host giving two cases the same alias.
// Adjacent cases may share one: a host with fewer models repeats an id.
func NewModelTierSet(decl ModelTierDeclaration) (*ModelTierSet, error) {
	problems := slices.Concat(decl.caseProblems(), decl.listProblems(), decl.hostProblems())
	if len(problems) > 0 {
		errs := make([]error, len(problems))
		for i, p := range problems {
			errs[i] = fmt.Errorf("%w: enum %s at %s: %s", errModelTierDeclaration, decl.Enum, decl.Site, p)
		}
		return nil, errors.Join(errs...)
	}
	byName := make(map[string]int, len(decl.Cases))
	for i, c := range decl.Cases {
		byName[c] = i
	}
	hosts := make(map[string][]string, len(decl.Hosts))
	for k, v := range decl.Hosts {
		hosts[k] = slices.Clone(v)
	}
	return &ModelTierSet{
		enum:   decl.Enum,
		site:   decl.Site,
		cases:  slices.Clone(decl.Cases),
		byName: byName,
		def:    decl.Default,
		bounds: slices.Clone(decl.Bounds),
		skills: slices.Clone(decl.Skills),
		hosts:  hosts,
	}, nil
}

// caseProblems checks the enum's name, its cases and the default rank.
func (decl ModelTierDeclaration) caseProblems() []string {
	var out []string
	n := len(decl.Cases)
	if decl.Enum == "" {
		out = append(out, "the enum has no name")
	}
	if n == 0 {
		out = append(out, "the enum declares no cases")
	}
	seen := make(map[string]bool, n)
	for i, c := range decl.Cases {
		switch {
		case c == "":
			out = append(out, fmt.Sprintf("case %d has no name", i))
		case seen[c]:
			out = append(out, fmt.Sprintf("case %q is declared twice", c))
		}
		seen[c] = true
	}
	if n > 0 && (decl.Default < 0 || decl.Default >= n) {
		out = append(out, fmt.Sprintf("default rank %d is outside 0..%d", decl.Default, n-1))
	}
	return out
}

// listProblems checks bounds and skills against the cases.
func (decl ModelTierDeclaration) listProblems() []string {
	var out []string
	n := len(decl.Cases)
	if len(decl.Bounds) != n {
		out = append(out, fmt.Sprintf("bounds covers %d cases and the enum declares %d", len(decl.Bounds), n))
	}
	for i, b := range decl.Bounds {
		if b.MaxLive < 0 || b.MaxContext < 0 {
			out = append(out, fmt.Sprintf("bounds for rank %d are negative", i))
		}
	}
	if len(decl.Skills) != n {
		out = append(out, fmt.Sprintf("skills covers %d cases and the enum declares %d", len(decl.Skills), n))
	}
	for i, s := range decl.Skills {
		if s == "" || !s.Valid() {
			out = append(out, fmt.Sprintf("skills for rank %d is %s, and the variants are %s", i, s, strings.Join(s.Values(), ", ")))
		}
	}
	return out
}

// hostProblems checks each host's aliases, sorted so the report is stable.
func (decl ModelTierDeclaration) hostProblems() []string {
	var out []string
	n := len(decl.Cases)
	for _, host := range slices.Sorted(maps.Keys(decl.Hosts)) {
		aliases := decl.Hosts[host]
		if host == "" {
			out = append(out, "a host mapping has no harness id")
			continue
		}
		if len(aliases) != n {
			out = append(out, fmt.Sprintf("host %s maps %d cases and the enum declares %d", host, len(aliases), n))
			continue
		}
		seen := map[string]int{}
		for i, a := range aliases {
			first, dup := seen[a]
			switch {
			case a == "":
				out = append(out, fmt.Sprintf("host %s maps rank %d to an empty alias", host, i))
			case dup && aliases[i-1] != a:
				out = append(out, fmt.Sprintf("host %s maps ranks %d and %d to %q, and only adjacent tiers may share an id, so a spawn of it names no one run of tiers",
					host, first, i, a))
			case !dup:
				seen[a] = i
			}
		}
	}
	return out
}

// All lists every tier, cheapest first.
func (s *ModelTierSet) All() []ModelTier {
	out := make([]ModelTier, len(s.cases))
	for i := range s.cases {
		out[i] = ModelTier{set: s, rank: i}
	}
	return out
}

// Parse returns the tier named name. An unknown name errors, naming the declared cases.
// An old name [RenamedModelTier] respells reads as its new one,
// when the set declares the new name and not the old.
func (s *ModelTierSet) Parse(name string) (ModelTier, error) {
	if i, ok := s.byName[name]; ok {
		return ModelTier{set: s, rank: i}, nil
	}
	if renamed, ok := RenamedModelTier(name); ok {
		if i, ok := s.byName[renamed]; ok {
			return ModelTier{set: s, rank: i}, nil
		}
	}
	return ModelTier{}, fmt.Errorf("%q is not a model tier: enum %s at %s declares %s", name, s.enum, s.site, strings.Join(s.cases, ", "))
}

// renamedModelTiers maps each old shipped tier name to its new one.
// compat(until: the next release): a row written before the rename
// still names economy, standard or principal.
var renamedModelTiers = map[string]string{"economy": "routine", "standard": "bounded", "principal": "design"}

var renamedModelTierSaid sync.Map

// RenamedModelTier is the new spelling of an old shipped tier name,
// and false for any other name. The first read of each old name
// in a process warns once. compat: see renamedModelTiers.
func RenamedModelTier(name string) (string, bool) {
	renamed, ok := renamedModelTiers[name]
	if !ok {
		return "", false
	}
	if _, said := renamedModelTierSaid.LoadOrStore(name, true); !said {
		slog.WarnContext(context.Background(), fmt.Sprintf("model tier %s is now spelled %s: magus reads %s as %s until the next release, so write %s",
			name, renamed, name, renamed, renamed))
	}
	return renamed, true
}

// At returns the tier at rank, and false when rank is outside the declaration.
func (s *ModelTierSet) At(rank int) (ModelTier, bool) {
	if rank < 0 || rank >= len(s.cases) {
		return ModelTier{}, false
	}
	return ModelTier{set: s, rank: rank}, true
}

// Default is the declared default tier.
func (s *ModelTierSet) Default() ModelTier { return ModelTier{set: s, rank: s.def} }

// Bounds returns t's caps, or no caps for a tier from another set.
func (s *ModelTierSet) Bounds(t ModelTier) ModelTierBounds {
	if t.set != s {
		return ModelTierBounds{}
	}
	return s.bounds[t.rank]
}

// Skills returns the skill variant t's workers load, or "" for a tier from another set.
func (s *ModelTierSet) Skills(t ModelTier) SkillVariant {
	if t.set != s {
		return ""
	}
	return s.skills[t.rank]
}

// Alias returns the model host spawns for t. It errors for a tier from another set and
// for a host the declaration does not map, naming the hosts it does.
func (s *ModelTierSet) Alias(host string, t ModelTier) (string, error) {
	if t.set != s {
		return "", fmt.Errorf("model tier %q is not a case of enum %s", t.Name(), s.enum)
	}
	aliases, ok := s.hosts[host]
	if !ok {
		return "", fmt.Errorf("enum %s at %s maps no model for harness %s: its hosts are %s", s.enum, s.site, host, s.hostIDs())
	}
	return aliases[t.rank], nil
}

// Resolve returns the tier host's alias names, and false when host is unmapped or no
// case maps to alias. An alias tiers share resolves to the cheapest.
func (s *ModelTierSet) Resolve(host, alias string) (ModelTier, bool) {
	i := slices.Index(s.hosts[host], alias)
	if i < 0 {
		return ModelTier{}, false
	}
	return ModelTier{set: s, rank: i}, true
}

// SharedAliases names each host that maps t and another tier to one
// alias, with the tiers that share it, sorted by host. It is empty
// when no host repeats t's alias, or t comes from another set.
func (s *ModelTierSet) SharedAliases(t ModelTier) []string {
	if t.set != s {
		return nil
	}
	var out []string
	for _, host := range s.Hosts() {
		aliases := s.hosts[host]
		var tiers []string
		for i, a := range aliases {
			if a == aliases[t.rank] {
				tiers = append(tiers, s.cases[i])
			}
		}
		if len(tiers) > 1 {
			out = append(out, fmt.Sprintf("%s runs %s on one id, %s", host, joinAnd(tiers), aliases[t.rank]))
		}
	}
	return out
}

func joinAnd(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return fmt.Sprintf("%s and %s", strings.Join(words[:len(words)-1], ", "), words[len(words)-1])
}

// Enum is the name of the Buzz enum that declared the set.
func (s *ModelTierSet) Enum() string { return s.enum }

// Site is the file:line, or module name, of the declaration.
func (s *ModelTierSet) Site() string { return s.site }

// Hosts lists the ids of the harnesses the declaration maps to models, in order.
func (s *ModelTierSet) Hosts() []string {
	return slices.Sorted(maps.Keys(s.hosts))
}

func (s *ModelTierSet) hostIDs() string {
	if len(s.hosts) == 0 {
		return "none"
	}
	return strings.Join(s.Hosts(), ", ")
}

// SkillVariant is which body of a magus skill a reader loads. Both come from one
// curated source, so they share a content digest and go stale together.
type SkillVariant string

const (
	// SkillVariantShort sheds enumeration and keeps judgment: a capable reader can
	// re-derive the mechanical steps, not which failures are silent or load-bearing.
	SkillVariantShort SkillVariant = "short"
	// SkillVariantFull spells out every mechanical step and why each is the right move.
	SkillVariantFull SkillVariant = "full"
)

// ModelTierSource says which declaration chose a job's model tier.
type ModelTierSource string

const (
	// ModelTierSourceRow is a tier or model the job row named.
	ModelTierSourceRow ModelTierSource = "row"
	// ModelTierSourceMatrix is the workspace's registered matrix function's answer.
	ModelTierSourceMatrix ModelTierSource = "matrix"
	// ModelTierSourceUserMatrix is the person's own matrix module's answer.
	ModelTierSourceUserMatrix ModelTierSource = "user-matrix"
	// ModelTierSourceDefault is the declaration's default.
	ModelTierSourceDefault ModelTierSource = "default"
)

// JobModelTier is the tier recorded on a job row when it was assigned: the case name and
// rank, the skill variant its worker loads, which declaration chose it and why, the
// file:line of that declaration, and the model alias the host spawns.
type JobModelTier struct {
	Name   string          `json:"name"             yaml:"name"`
	Rank   int             `json:"rank"             yaml:"rank"`
	Skills SkillVariant    `json:"skills"           yaml:"skills"`
	Source ModelTierSource `json:"source"           yaml:"source"`
	Reason string          `json:"reason,omitempty" yaml:"reason,omitempty"`
	Site   string          `json:"site,omitempty"   yaml:"site,omitempty"`
	Model  string          `json:"model,omitempty"  yaml:"model,omitempty"`
	// Shared is [ModelTierSet.SharedAliases] for the tier when it was assigned.
	Shared []string `json:"shared,omitempty" yaml:"shared,omitempty"`
}

// String renders the assignment as `fork` and `describe job` print it: the name and rank,
// the source that chose it and, where the source gave them, its reason and site.
// Each Shared line follows.
func (t JobModelTier) String() string {
	line := fmt.Sprintf("%s (rank %d), %s", t.Name, t.Rank, t.Source)
	if t.Reason != "" {
		line += fmt.Sprintf(": %q", t.Reason)
	}
	if t.Site != "" {
		line += " at " + t.Site
	}
	for _, s := range t.Shared {
		line += "; " + s
	}
	return line
}
