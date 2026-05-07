package types

// Binding is the per-spell registration state attached to a project.
// One Binding is created per WithSpell call.
type Binding struct {
	Name          string // spell identifier
	ClaimWeight   int    // higher wins on glob collision; ties fall back to last-wins
	AddedClaims   []string
	RemovedClaims []string
}

// TargetPolicy declares behavioural hooks for a named target.
type TargetPolicy struct {
	// CheckClean fails the run if the working tree is dirty after the target runs (write=false).
	CheckClean bool
	// TrackFlake routes the target through flake detection and auto-retry.
	TrackFlake bool
	// NoCache opts the target out of the cache: magus always runs it and never
	// replays or snapshots it. Set for long-running targets (e.g. a blocking
	// fs.watch loop) where a cached "success" would make a re-run a no-op.
	NoCache bool
	// Isolated serializes the target against the whole RunAll batch: while an
	// isolated target runs, no other scheduled target runs concurrently. Set for
	// targets whose correctness depends on an undisturbed working tree (e.g. a
	// drift gate that inspects `git status`).
	Isolated bool
}

// IsZero reports whether p carries no policy.
func (p TargetPolicy) IsZero() bool { return p == TargetPolicy{} }

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
	TargetPolicies map[string]TargetPolicy
	ResolvedSpells []*Spell // set at the end of magus.Open; immutable thereafter
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
