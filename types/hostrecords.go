package types

// Boundary mirrors of the records magus host methods return. Each is the typed
// value a Go SDK caller gets and the serializable view a magusfile can annotate
// (`> FileInfo`, `> HttpResponse`, …) for compile-checked field access. The host
// Impl returns the struct; its Record method is the {field: value} map the
// generated Buzz trampoline marshals (see hostbuzz.Recorder), so the Buzz boundary
// is unchanged. The Buzz `object` mirrors are generated from these structs by
// cmd/magus-types-gen (go:generate) and shipped in the magus/target module, so the
// Go struct stays the single source of truth and struct, Record, and mirror can't
// drift.

// FileInfo mirrors fs.stat's {size, mtime, mode, is_dir} record: size in bytes,
// mtime as Unix milliseconds, mode as the integer permission bits.
type FileInfo struct {
	Size  int64
	Mtime float64
	Mode  int64
	IsDir bool `buzz:"is_dir"`
}

// Record is the Buzz boundary map fs.stat returns. The generated trampoline calls
// it (see hostbuzz.Recorder) so a magusfile sees {size, mtime, mode, is_dir}.
func (fi FileInfo) Record() map[string]any {
	return map[string]any{
		"size":   fi.Size,
		"mtime":  fi.Mtime,
		"mode":   fi.Mode,
		"is_dir": fi.IsDir,
	}
}

// HTTPResponse mirrors http.get/post/request's {status, body, headers} record.
// headers maps each response header name to its first value.
type HTTPResponse struct {
	Status  int
	Body    string
	Headers map[string]string
}

// Record is the Buzz boundary map http.get/post/request returns: {status, body, headers}.
func (r HTTPResponse) Record() map[string]any {
	return map[string]any{"status": r.Status, "body": r.Body, "headers": r.Headers}
}

// SemverVersion mirrors semver.parse's {major, minor, patch, prerelease,
// metadata, original} record.
type SemverVersion struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string
	Metadata   string
	Original   string
}

// Record is the Buzz boundary map semver.parse returns.
func (v SemverVersion) Record() map[string]any {
	return map[string]any{
		"major":      v.Major,
		"minor":      v.Minor,
		"patch":      v.Patch,
		"prerelease": v.Prerelease,
		"metadata":   v.Metadata,
		"original":   v.Original,
	}
}

// URL mirrors encoding.parse_url's {scheme, host, port, path, query, fragment}
// record.
type URL struct {
	Scheme   string
	Host     string
	Port     string
	Path     string
	Query    string
	Fragment string
}

// Record is the Buzz boundary map encoding.parse_url returns.
func (u URL) Record() map[string]any {
	return map[string]any{
		"scheme":   u.Scheme,
		"host":     u.Host,
		"port":     u.Port,
		"path":     u.Path,
		"query":    u.Query,
		"fragment": u.Fragment,
	}
}
