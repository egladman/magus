package tier

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Tier is one case of the model-tier enum a workspace declares. Only a
// Set hands one out, so a Tier always names a declared case; the zero
// value means no tier was assigned.
type Tier struct {
	set  *Set
	rank int
}

// Name is the case's name as the enum declares it, or "" for the zero value.
func (t Tier) Name() string {
	if t.set == nil {
		return ""
	}
	return t.set.cases[t.rank]
}

// Rank is the case's position in the enum, cheapest first, or -1 for the zero value.
func (t Tier) Rank() int {
	if t.set == nil {
		return -1
	}
	return t.rank
}

// IsZero reports whether t is unassigned.
func (t Tier) IsZero() bool { return t.set == nil }

// Above reports whether t ranks above u. Ranks compare only within one declaration, so
// it errors with a [*CompareError] when the two come from different sets or either is
// the zero value.
func (t Tier) Above(u Tier) (bool, error) {
	if t.set == nil || u.set == nil || t.set != u.set {
		return false, &CompareError{T: t, U: u}
	}
	return t.rank > u.rank, nil
}

// CompareError is [Tier.Above]'s refusal of two tiers that do not compare: one is
// unassigned, or they come from different declarations.
type CompareError struct {
	T, U Tier
}

func (e *CompareError) Error() string {
	if e.T.set == nil || e.U.set == nil {
		return fmt.Sprintf("model tiers %q and %q cannot be compared: one is unassigned", e.T.Name(), e.U.Name())
	}
	return fmt.Sprintf("model tiers %q (%s) and %q (%s) come from different declarations", e.T.Name(), e.T.set.enum, e.U.Name(), e.U.set.enum)
}

// Within is nil when t ranks at or below ceiling, the bound a child's tier keeps to its
// parent's. It errors with a [*RanksAboveError] when t ranks above ceiling, and as
// [Tier.Above] does when the two cannot be compared.
func (t Tier) Within(ceiling Tier) error {
	above, err := t.Above(ceiling)
	if err != nil {
		return err
	}
	if above {
		return &RanksAboveError{Tier: t, Ceiling: ceiling}
	}
	return nil
}

// RanksAboveError is [Tier.Within]'s refusal of a tier ranked above its ceiling.
type RanksAboveError struct {
	Tier, Ceiling Tier
}

func (e *RanksAboveError) Error() string {
	return fmt.Sprintf("model tier %s (rank %d) ranks above %s (rank %d)", e.Tier.Name(), e.Tier.rank, e.Ceiling.Name(), e.Ceiling.rank)
}

// MarshalText writes the case name. There is no UnmarshalText: a name becomes a
// Tier only through [Set.Parse], against the declaration it belongs to.
func (t Tier) MarshalText() ([]byte, error) { return []byte(t.Name()), nil }

// Set is a validated model-tier declaration. It is immutable and safe for
// concurrent use.
type Set struct {
	enum, site string
	cases      []string
	byName     map[string]int
	def        int
	bounds     []Bounds
	skills     []SkillVariant
	hosts      map[string][]string
}

// Declaration is the raw declaration a magusfile's Buzz enum and its mapping
// functions produce. Bounds, Skills and each Hosts list carry one entry per case, by rank.
type Declaration struct {
	// Enum is the Buzz enum's name and Site the file:line that declared it.
	Enum, Site string
	// Cases are the enum's case names, cheapest first.
	Cases []string
	// Default is the rank a job gets when neither its job nor a matrix names one.
	Default int
	Bounds  []Bounds
	Skills  []SkillVariant
	// Hosts maps a harness id to its model alias for each case.
	Hosts map[string][]string
}

// Bounds caps the jobs at one model tier. A zero field is no cap.
type Bounds struct {
	// MaxLive is how many jobs at the tier may be live at once.
	MaxLive int
	// MaxContext is the context, in tokens, a leased worker at the tier may reach.
	MaxContext int64
}

// NewSet validates decl and builds the set. Every problem is reported at
// once, one [*DeclarationError] each, joined: no cases, a duplicate or empty case, a default out of range, a bounds, skills
// or host list whose length differs from the cases, a negative bound, an invalid skill
// variant, an empty host id or alias, or one host giving two cases the same alias.
// Adjacent cases may share one: a host with fewer models repeats an id.
func NewSet(decl Declaration) (*Set, error) {
	problems := slices.Concat(decl.caseProblems(), decl.listProblems(), decl.hostProblems())
	if len(problems) > 0 {
		errs := make([]error, len(problems))
		for i, p := range problems {
			errs[i] = &DeclarationError{Enum: decl.Enum, Site: decl.Site, Problem: p}
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
	return &Set{
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

// DeclarationError is one problem [NewSet] found in a declaration.
type DeclarationError struct {
	Enum, Site, Problem string
}

func (e *DeclarationError) Error() string {
	return fmt.Sprintf("model tier declaration: enum %s at %s: %s", e.Enum, e.Site, e.Problem)
}

// caseProblems checks the enum's name, its cases and the default rank.
func (decl Declaration) caseProblems() []string {
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
func (decl Declaration) listProblems() []string {
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
func (decl Declaration) hostProblems() []string {
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
func (s *Set) All() []Tier {
	out := make([]Tier, len(s.cases))
	for i := range s.cases {
		out[i] = Tier{set: s, rank: i}
	}
	return out
}

// Parse returns the tier named name. An unknown name is an [*UnknownTierError]. An old
// name [Renamed] respells reads as its new one, when the set declares the new name and
// not the old; a caller that must say so checks Renamed itself.
func (s *Set) Parse(name string) (Tier, error) {
	if i, ok := s.byName[name]; ok {
		return Tier{set: s, rank: i}, nil
	}
	if renamed, ok := Renamed(name); ok {
		if i, ok := s.byName[renamed]; ok {
			return Tier{set: s, rank: i}, nil
		}
	}
	return Tier{}, &UnknownTierError{Name: name, Enum: s.enum, Site: s.site, Cases: slices.Clone(s.cases)}
}

// UnknownTierError is [Set.Parse]'s refusal of a name the declaration does not declare.
type UnknownTierError struct {
	Name, Enum, Site string
	// Cases are the declared case names, cheapest first.
	Cases []string
}

func (e *UnknownTierError) Error() string {
	return fmt.Sprintf("%q is not a model tier: enum %s at %s declares %s", e.Name, e.Enum, e.Site, strings.Join(e.Cases, ", "))
}

// renamedModelTiers maps each old shipped tier name to its new one.
// compat(until: the next release): a job written before the rename
// still names economy, standard or principal.
var renamedModelTiers = map[string]string{"economy": "routine", "standard": "bounded", "principal": "design"}

// Renamed is the new spelling of an old shipped tier name, and false for any other
// name. It says nothing itself: a caller reading an old name tells its own reader.
// compat: see renamedModelTiers.
func Renamed(name string) (string, bool) {
	renamed, ok := renamedModelTiers[name]
	return renamed, ok
}

// At returns the tier at rank, and false when rank is outside the declaration.
func (s *Set) At(rank int) (Tier, bool) {
	if rank < 0 || rank >= len(s.cases) {
		return Tier{}, false
	}
	return Tier{set: s, rank: rank}, true
}

// Default is the declared default tier.
func (s *Set) Default() Tier { return Tier{set: s, rank: s.def} }

// Bounds returns t's caps, or no caps for a tier from another set.
func (s *Set) Bounds(t Tier) Bounds {
	if t.set != s {
		return Bounds{}
	}
	return s.bounds[t.rank]
}

// Skills returns the skill variant t's workers load, or "" for a tier from another set.
func (s *Set) Skills(t Tier) SkillVariant {
	if t.set != s {
		return ""
	}
	return s.skills[t.rank]
}

// Alias returns the model host spawns for t. It errors with a [*ForeignTierError] for a
// tier from another set and an [*UnmappedHostError] for a host the declaration does not
// map.
func (s *Set) Alias(host string, t Tier) (string, error) {
	if t.set != s {
		return "", &ForeignTierError{Tier: t.Name(), Enum: s.enum}
	}
	aliases, ok := s.hosts[host]
	if !ok {
		return "", &UnmappedHostError{Host: host, Enum: s.enum, Site: s.site, Hosts: s.Hosts()}
	}
	return aliases[t.rank], nil
}

// ForeignTierError is a tier handed to a [Set] that did not declare it.
type ForeignTierError struct {
	Tier, Enum string
}

func (e *ForeignTierError) Error() string {
	return fmt.Sprintf("model tier %q is not a case of enum %s", e.Tier, e.Enum)
}

// UnmappedHostError is [Set.Alias]'s refusal of a harness the declaration maps no model
// for. Hosts are the harnesses it does map, sorted.
type UnmappedHostError struct {
	Host, Enum, Site string
	Hosts            []string
}

func (e *UnmappedHostError) Error() string {
	hosts := "none"
	if len(e.Hosts) > 0 {
		hosts = strings.Join(e.Hosts, ", ")
	}
	return fmt.Sprintf("enum %s at %s maps no model for harness %s: its hosts are %s", e.Enum, e.Site, e.Host, hosts)
}

// Resolve returns the tier host's alias names, and false when host is unmapped or no
// case maps to alias. An alias tiers share resolves to the cheapest.
func (s *Set) Resolve(host, alias string) (Tier, bool) {
	i := slices.Index(s.hosts[host], alias)
	if i < 0 {
		return Tier{}, false
	}
	return Tier{set: s, rank: i}, true
}

// SharedAliases names each host that maps t and another tier to one
// alias, with the tiers that share it, sorted by host. It is empty
// when no host repeats t's alias, or t comes from another set.
func (s *Set) SharedAliases(t Tier) []string {
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
func (s *Set) Enum() string { return s.enum }

// Site is the file:line, or module name, of the declaration.
func (s *Set) Site() string { return s.site }

// Hosts lists the ids of the harnesses the declaration maps to models, in order.
func (s *Set) Hosts() []string {
	return slices.Sorted(maps.Keys(s.hosts))
}
