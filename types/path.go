package types

import (
	"path/filepath"
)

// Path is magus's lexical filesystem reference. Value may be relative or
// absolute; Resolve is the single conversion point to an absolute location.
// IsDir describes the intended filesystem kind when it is known. A directory is
// still a filesystem entry, so FileInfo remains the observed stat result rather
// than a competing path abstraction.
type Path struct {
	Value string
	IsDir bool `buzz:"isDir"`
}

// Resolve returns p against base, retaining its kind. An absolute p is cleaned
// and returned unchanged; an empty value remains empty so optional paths do not
// accidentally become the workspace root.
func (p Path) Resolve(base string) Path {
	if p.Value == "" {
		return p
	}
	if filepath.IsAbs(p.Value) {
		p.Value = filepath.Clean(p.Value)
		return p
	}
	p.Value = filepath.Join(base, p.Value)
	return p
}

// RelativeTo returns p expressed from base when a relative path exists.
func (p Path) RelativeTo(base string) (Path, error) {
	if p.Value == "" {
		return p, nil
	}
	rel, err := filepath.Rel(base, p.Resolve(base).Value)
	if err != nil {
		return Path{}, err
	}
	p.Value = filepath.ToSlash(rel)
	return p, nil
}
