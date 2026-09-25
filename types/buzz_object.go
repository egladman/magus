package types

import (
	"fmt"
)

// Boundary mirrors of the objects magus host methods return. Each is the typed
// value a Go SDK caller gets and the serializable view a magusfile can annotate
// (`> FileInfo`, `> HttpResponse`, ...) for compile-checked field access. The
// Impl returns the struct; the encoder generated for it in
// internal/interp/bindings/gen turns it into the map the VM reads. The Buzz `object`
// mirrors are generated from these structs by cmd/magus-utils types (go:generate)
// and shipped with the host module that returns each one (os, fs, http, encoding,
// semver, vcs; see internal/spell/host_types.go), so the Go struct stays the
// single source of truth and struct, encoder, and mirror can't drift.

// BuzzObject is the Buzz `object` a host method's return crosses the boundary
// as: the map a magusfile sees when it annotates a result (`> FileInfo`,
// `> HttpResponse`, ...). Named in Buzz's OWN vocabulary, not magus-internal
// jargon: Buzz's type system has ObjectType, and cmd/magus-utils types emits
// `export object Foo` for each mirror, so a Buzz author never has to translate
// what they typed into some other word this codebase prefers. Named rather
// than a bare map[string]any so every signature says which projection it is:
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

// UncompressResult mirrors archive.uncompress's {files, bytes} object: the paths written
// (sorted) and their total uncompressed size.
//
// Files are Paths based at the destination directory, which is where uncompress actually
// wrote them, so a caller can open one without first remembering which of the two
// directories it passed in the entries were measured from.
type UncompressResult struct {
	Files []Path
	Bytes int
}

// CompressResult mirrors archive.compress's {files, bytes_in, bytes_out} object.
//
// Deliberately NOT the same type as UncompressResult. The two look alike and are not:
// compressing reports what went in AND what came out, because the ratio is the thing you
// asked for, while uncompressing has only one size to report. Sharing a type would mean a
// bytes field that means different things depending on which call produced it.
type CompressResult struct {
	// Files are based at the SOURCE directory (the files that went in), where
	// UncompressResult's are based at the destination. Each is based where it exists.
	Files    []Path
	BytesIn  int `buzz:"bytes_in"`
	BytesOut int `buzz:"bytes_out"`
}

// ArchiveEntry mirrors one element of archive.list's result: an entry's name as
// the archive itself records it, its uncompressed size, and whether it is a
// directory.
//
// Name is a plain str, not a Path, and that is deliberate: it is a name INSIDE an
// archive, which resolves against nothing on disk until something extracts it. A
// Path would invite fs.read_file on a file that is not there. UncompressResult
// hands back Paths precisely because by then the entries do exist.
//
// Size is the UNCOMPRESSED size, which is what a caller checking "will this fit"
// needs; a zip's compressed size is an implementation detail of the container and
// tar has no equivalent field at all, so reporting one would be inconsistent
// across the formats this module treats alike.
type ArchiveEntry struct {
	Name  string
	Size  int
	IsDir bool `buzz:"is_dir"`
}

// HTTPResponse mirrors http.get/post/request's {status, body, headers} object.
// headers maps each response header name to its first value.
type HTTPResponse struct {
	Status  int
	Body    string
	Headers map[string]string
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

// String renders the canonical "vMAJOR.MINOR.PATCH[-PRERELEASE][+METADATA]"
// form, e.g. "v1.2.3-rc1+build5". This is deliberately NOT Original: Original
// is the raw text as the user wrote the tag/version string, so it round-trips
// things String() normalizes away (a leading zero like "v1.02.3", a missing
// "v", metadata the canonical form still carries). The leading "v" matches how
// this codebase already writes versions everywhere else: git tags ("v0.3.0"),
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

// PipeRecord mirrors one record a magus stage writes to the next in a pipe: the -o jsonl
// record, with the fields a script filters on lifted out of its body. A field the record
// does not carry is empty; Body is the whole record, for everything else.
type PipeRecord struct {
	Schema int
	// Type is the record's event type: run.scope, run.target.result, run.step, ...
	Type    string
	Project string
	Target  string
	Status  string
	Error   string
	// Ref is the output ref of a run.target.result, which reads its captured output.
	Ref string
	// Projects is a run.scope's selection: the projects a run downstream inherits.
	Projects []string
	// Body is the record as its JSON line, without the newline.
	Body string
}

// TargetArtifact is one file a target actually produced: a declared output glob expanded
// against the working tree. Glob is carried alongside Path because the declaration is
// what makes the file a build artifact rather than an incidental file, and a reader
// chasing an unexpected artifact needs to know which ctx.writesFiles(...) claimed it.
// It crosses into Buzz as pipe.outputs's Artifact.
type TargetArtifact struct {
	Path string // workspace-relative
	Glob string // the declaration it matched
	// ProjectPath is the project whose target DECLARED the glob, not necessarily the
	// project the file sits in, since a target may declare an output into another
	// project's tree. Recorded here because this is the only place that knows it: a
	// consumer re-deriving attribution from Path has to guess, and the guess fails
	// outright for a file no project's tree claims.
	ProjectPath string `buzz:"project"`
}

// ArtifactVersion mirrors one row of pipe.history: a version of an artifact the cache
// stored, newest first. Blob is the content hash the store keys it by, the same sha256
// crypto.sha256_file gives for the file on disk, so the two compare.
type ArtifactVersion struct {
	Blob  string
	Short string
	Size  int64
	// Target is the target whose run produced this version.
	Target string
	// Created is when that run's cache entry was written, RFC 3339 in UTC.
	Created string
	// Entry is that cache entry's key.
	Entry string
}

// FlagParse mirrors flags.parse's {values, positionals, unknown} object.
//
// Three fields rather than two because the caller, not the module, decides what an
// argument it did not declare means. A parser that folds unknown arguments into the
// positionals leaves the caller unable to tell "you passed me a file" from "you passed me
// a flag I have never heard of", and one that drops them silently judges the call with
// settings nobody chose. Both answers belong to the script.
type FlagParse struct {
	// Values holds every declared flag that appeared. A switch records "true"; a valued
	// flag records its value. A flag given twice records the LAST one, because an argv
	// assembled by concatenation reads left to right and the nearer word is the override.
	Values map[string]string
	// Positionals are the words after the `--` separator, verbatim and in order.
	Positionals []string
	// Unknown holds every argument that was not declared, in order, whatever it looks
	// like. A leading dash does not make a word a flag here: only declaring it does.
	Unknown []string
}

// Skill mirrors one element of magus\skills's result: a skill this workspace offers an
// agent, as the text the agent loads.
//
// Source is "shipped" (magus's own catalog) or "local" (hand-authored in an installed
// skills directory). Form is "short" or "full"; a local skill has one body and reports
// "full", since nothing was withheld from it. Body is the SKILL.md text after its
// frontmatter. Current reports whether every installed copy of this form is byte-equal
// to what this binary would write, and is false when no install carries it; a local
// skill is always current, since nothing ships a newer copy.
type Skill struct {
	Name        string
	Description string
	Source      string
	Form        string
	Body        string
	Current     bool
}
