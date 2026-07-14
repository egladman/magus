package types

import "path/filepath"

// ProjectLabel is the human-facing display name for a project, used in logs and
// generated docs so a project at the workspace root never renders as the ambiguous
// "" or ".". A non-root path is shown as-is; the root project falls back to the
// workspace directory's base name (e.g. "magus"), then to "(workspace root)" when
// even that is unavailable. dir is the project's absolute directory ("" if unknown).
//
// This is the single normalization point for the "never print '.'" rule; callers in
// the run/cache logging path, describe, and MAGUS.md rendering all route through it.
func ProjectLabel(path, dir string) string {
	if path != "" && path != "." {
		return path
	}
	if dir != "" {
		if base := filepath.Base(dir); base != "" && base != "." && base != string(filepath.Separator) {
			return base
		}
	}
	return "(workspace root)"
}

// ProjectRef identifies a project for end-user output, carrying BOTH its stable machine
// identifier and its human name so no display surface has to re-derive one from the
// other (the "never print '.'" fix, applied once instead of per call site). Path is the
// workspace-relative identifier used for addressing, RPC, and cache keys ("." for the
// workspace root); Name is the readable label (ProjectLabel: the repo/dir base name for
// the root, e.g. "magus", the path otherwise). They coincide for a nested project and
// diverge for the root, so surfaces carry both rather than pick one and lose information.
type ProjectRef struct {
	Path string `json:"path" yaml:"path"`
	Name string `json:"name" yaml:"name"`
}

// NewProjectRef builds a display ref from a project's workspace-relative path and its
// absolute directory (dir feeds the root project's name via ProjectLabel).
func NewProjectRef(path, dir string) ProjectRef {
	return ProjectRef{Path: path, Name: ProjectLabel(path, dir)}
}

// Display returns "Name" when path and name coincide, else "Name (path)" - the explicit
// both-values form for a single-line human render where the path adds disambiguation.
func (r ProjectRef) Display() string {
	if r.Name == r.Path || r.Path == "" {
		return r.Name
	}
	return r.Name + " (" + r.Path + ")"
}

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
