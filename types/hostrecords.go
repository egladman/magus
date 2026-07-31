package types

import "fmt"

// Boundary mirrors of the objects magus host methods return. Each is the typed
// value a Go SDK caller gets and the serializable view a magusfile can annotate
// (`> FileInfo`, `> HttpResponse`, ...) for compile-checked field access. The
// Impl returns the struct; its BuzzObject method is the {field: value} map the
// generated Buzz trampoline marshals (see host.BuzzObjecter). The Buzz `object`
// mirrors are generated from these structs by cmd/magus-utils types (go:generate)
// and shipped in the magus/target module, so the Go struct stays the single
// source of truth and struct, BuzzObject, and mirror can't drift.

// BuzzObject is the Buzz `object` a host method's return crosses the boundary
// as: the map a magusfile sees when it annotates a result (`> FileInfo`,
// `> HttpResponse`, ...). Named in Buzz's OWN vocabulary, not magus-internal
// jargon - Buzz's type system has ObjectType, and cmd/magus-utils types emits
// `export object Foo` for each mirror, so a Buzz author never has to translate
// what they typed into some other word this codebase prefers. Named rather
// than a bare map[string]any so every signature says which projection it is -
// this is NOT the JSON shape and deliberately differs from it (camelCase keys,
// `buzz:"-"` omissions, timestamps as RFC3339 text).
type BuzzObject map[string]any

// FileInfo mirrors fs.stat's {size, mtime, mode, is_dir} object: size in bytes,
// mtime as Unix milliseconds, mode as the integer permission bits.
type FileInfo struct {
	Size  int64
	Mtime float64
	Mode  int64
	IsDir bool `buzz:"is_dir"`
}

// BuzzObject is the Buzz boundary map fs.stat returns: {size, mtime, mode, is_dir}.
func (fi FileInfo) BuzzObject() BuzzObject {
	return map[string]any{
		"size":   fi.Size,
		"mtime":  fi.Mtime,
		"mode":   fi.Mode,
		"is_dir": fi.IsDir,
	}
}

// HTTPResponse mirrors http.get/post/request's {status, body, headers} object.
// headers maps each response header name to its first value.
type HTTPResponse struct {
	Status  int
	Body    string
	Headers map[string]string
}

// BuzzObject is the Buzz boundary map http.get/post/request returns: {status, body, headers}.
func (r HTTPResponse) BuzzObject() BuzzObject {
	return map[string]any{"status": r.Status, "body": r.Body, "headers": r.Headers}
}

// SemverVersion mirrors semver.parse's {major, minor, patch, prerelease,
// metadata, original} object.
type SemverVersion struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string
	Metadata   string
	Original   string
}

// BuzzObject is the Buzz boundary map semver.parse returns.
func (v SemverVersion) BuzzObject() BuzzObject {
	return map[string]any{
		"major":      v.Major,
		"minor":      v.Minor,
		"patch":      v.Patch,
		"prerelease": v.Prerelease,
		"metadata":   v.Metadata,
		"original":   v.Original,
	}
}

// String renders the canonical "vMAJOR.MINOR.PATCH[-PRERELEASE][+METADATA]"
// form, e.g. "v1.2.3-rc1+build5". This is deliberately NOT Original: Original
// is the raw text as the user wrote the tag/version string, so it round-trips
// things String() normalizes away (a leading zero like "v1.02.3", a missing
// "v", metadata the canonical form still carries). The leading "v" matches how
// this codebase already writes versions everywhere else - git tags ("v0.3.0"),
// the linker-stamped build version (-X main.version=v0.1.0), and selfupdate's
// target version handling.
func (v SemverVersion) String() string {
	s := fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Prerelease != "" {
		s += "-" + v.Prerelease
	}
	if v.Metadata != "" {
		s += "+" + v.Metadata
	}
	return s
}

// SemverNext mirrors semver.next's {major, minor, patch} object: the three
// candidate next versions after a parsed version (bump major, minor, or
// patch), each rendered "vX.Y.Z" to match SemverVersion.String()'s convention.
type SemverNext struct {
	Major string
	Minor string
	Patch string
}

// BuzzObject is the Buzz boundary map semver.next returns.
func (n SemverNext) BuzzObject() BuzzObject {
	return map[string]any{
		"major": n.Major,
		"minor": n.Minor,
		"patch": n.Patch,
	}
}

// URL mirrors encoding.parse_url's {scheme, host, port, path, query, fragment}
// object.
type URL struct {
	Scheme   string
	Host     string
	Port     string
	Path     string
	Query    string
	Fragment string
}

// BuzzObject is the Buzz boundary map encoding.parse_url returns.
func (u URL) BuzzObject() BuzzObject {
	return map[string]any{
		"scheme":   u.Scheme,
		"host":     u.Host,
		"port":     u.Port,
		"path":     u.Path,
		"query":    u.Query,
		"fragment": u.Fragment,
	}
}
