package types

import (
	"path/filepath"
)

// Path is magus's lexical filesystem reference: a value plus the directory it is
// measured from, so a path is self-describing rather than only meaningful to whoever
// produced it.
//
// Base is what makes this worth having over a bare string. Every path magus hands out
// is relative to SOMETHING - a VCS status path to the repository root, a glob result to
// the pattern's root, a footprint entry to the project directory - and a target's cwd is
// its project directory, not the workspace root. A []string forces every consumer to
// know, out of band, which of those it was handed, and to be right; the failure is silent
// and looks like a missing file. Carrying the base makes Resolve total: it needs no
// argument, so there is no argument to get wrong.
//
// Base is empty when Value is already absolute, or when the producer genuinely does not
// know - which is itself information a caller can test for, where a bare string offered
// no way to ask.
//
// IsDir describes the intended filesystem kind when it is known. A directory is
// still a filesystem entry, so FileInfo remains the observed stat result rather
// than a competing path abstraction.
type Path struct {
	Value string `json:"value"`
	Base  string `json:"base,omitempty"`
	IsDir bool   `json:"is_dir,omitempty" buzz:"isDir"`
}

// Resolve returns p as an absolute path, retaining its kind. It takes no base: a Path
// carries its own. An absolute Value is cleaned and returned unchanged; an empty Value
// remains empty so optional paths do not accidentally become their base directory. The
// result has an empty Base, since an absolute path is measured from nothing.
func (p Path) Resolve() Path {
	if p.Value == "" {
		return p
	}
	if filepath.IsAbs(p.Value) {
		p.Value = filepath.Clean(p.Value)
		p.Base = ""
		return p
	}
	p.Value = filepath.Join(p.Base, p.Value)
	p.Base = ""
	return p
}

// Rebase returns p measured from base instead of its current one. This is the deliberate
// way to reinterpret a path, as opposed to Resolve's no-argument form: a caller that
// genuinely means "treat this as relative to somewhere else" says so.
func (p Path) Rebase(base string) Path {
	p.Base = base
	return p
}

// RelativeTo returns p expressed from base, carrying base as its new Base so the result
// is still self-describing. Resolving first is what makes this correct for a path whose
// current Base differs from the requested one.
func (p Path) RelativeTo(base string) (Path, error) {
	if p.Value == "" {
		return p, nil
	}
	rel, err := filepath.Rel(base, p.Resolve().Value)
	if err != nil {
		return Path{}, err
	}
	p.Value = filepath.ToSlash(rel)
	p.Base = base
	return p, nil
}

// BuzzObject is the Buzz boundary map a Path crosses as: {value, base, isDir}.
//
// Base rides along deliberately. A Buzz caller that receives a path from vcs.status or a
// glob needs to know what it is measured from to open it, and the object mirror carries
// no methods - so the field is the only place that fact can live.
func (p Path) BuzzObject() BuzzObject {
	return BuzzObject{"value": p.Value, "base": p.Base, "isDir": p.IsDir}
}
