package types

// Binding is the per-spell registration state attached to a project.
// One Binding is created per WithSpell call.
type Binding struct {
	Name          string // spell identifier
	ClaimWeight   int    // higher wins on glob collision; ties fall back to last-wins
	AddedClaims   []string
	RemovedClaims []string
}

// Project is the record magus maintains for every directory with a marker file.
type Project struct {
	Path           string // repo-relative directory, forward slashes (e.g. "api", ".")
	Dir            string // absolute filesystem path
	Spell          string // primary spell name; use Spells for fan-out dispatch
	Spells         []string
	Bindings       []*Binding // parallel to Spells, in registration order
	Sources        []string   // doublestar globs relative to Dir for the cache key
	Outputs        []string   // doublestar globs snapshotted into and replayed from cache
	DependsOn      []string
	Exclusive      bool
	WatchIgnores   []IgnorePattern
	TargetPolicies map[string]Target // per-target execution policy; values carry only the policy fields of Target
	ResolvedSpells []*Spell          // set at the end of magus.Open; immutable thereafter
}

// AttachSpell associates spell with p without applying registration overrides.
func (p *Project) AttachSpell(spell *Spell) {
	if p.Spell == "" {
		p.Spell = spell.Name()
	}
	p.Spells = append(p.Spells, spell.Name())
	p.Bindings = append(p.Bindings, &Binding{Name: spell.Name()})
	p.Sources = append(p.Sources, spell.Sources()...)
	p.Outputs = append(p.Outputs, spell.Outputs()...)
}
