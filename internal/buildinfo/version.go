package buildinfo

import (
	"fmt"
	"strings"

	"github.com/egladman/magus/libs/figure"
	"github.com/egladman/magus/types"
)

// SourceRepository is where this binary's commit can be fetched, provided it was pushed.
const SourceRepository = "https://github.com/egladman/magus"

// UnknownVersion is the unstamped-build default, and the dev-build sentinel the proc
// adoption gate keys on to fingerprint one (proc.devVersionSentinel); keep the two in
// sync. Named because a server reports it too, and the two must not be compared.
const UnknownVersion = "unknown"

// BuiltDirty reports whether b was built from a modified tree, whose source exists only
// on the machine that built it. go-build marks that in the version (describe's -dirty
// suffix); a bare `go build` leaves it in the VCS stamp instead.
func BuiltDirty(b types.MagusBuild) bool {
	if strings.HasSuffix(b.Version, "-dirty") {
		return true
	}
	return b.Version == UnknownVersion && Modified()
}

// VersionOutput is the structured view of the build stamp. Engine is unconditional
// here even though the text form gates it behind -v: a verbosity flag is a reading
// convenience for a human, and a caller parsing json should not have to discover that
// a field exists only at a higher verbosity.
type VersionOutput struct {
	Version string `json:"version"    yaml:"version"`
	// Commit is the full revision: a forge serves a shallow fetch only by the whole id.
	Commit string `json:"commit"     yaml:"commit"`
	// Dirty means the tree held uncommitted changes, so Commit is not this binary's source.
	Dirty      bool   `json:"dirty"      yaml:"dirty"`
	Repository string `json:"repository" yaml:"repository"`
	BuildDate  string `json:"build_date" yaml:"build_date"`
	// No omitempty: a local build reports built_by as "" rather than dropping the key,
	// so `-o json` and `-o template` see one record shape either way.
	BuiltBy string `json:"built_by"   yaml:"built_by"`
	Engine  string `json:"engine"     yaml:"engine"`
	// Embedded is the identity of the Buzz modules compiled into this binary. Two builds
	// at one commit share a version string even when their embedded sources differ.
	Embedded VersionEmbedded `json:"embedded" yaml:"embedded"`
	// ServerVersion is what the server reported. A POINTER because the field has two
	// different silences and one spelling for both: NIL is "the probe never ran"
	// (--client, or an output format that does not render it) and the key is dropped,
	// while a non-nil empty string is "the probe ran and nothing answered".
	//
	// omitzero, not omitempty: under json/v2 omitempty drops a pointer to "" as well as
	// a nil one, which is exactly the collapse this pointer exists to undo. omitzero
	// drops only the nil.
	ServerVersion *string `json:"server,omitzero" yaml:"server,omitempty"`
}

// VersionEmbedded digests the embedded sources a generated output can depend on.
type VersionEmbedded struct {
	// FigureSHA256 is the hex SHA-256 of the magus/figure source; diagrams-generate in
	// docs keys on it, since the figures render from this copy and not the checkout's.
	FigureSHA256 string `json:"figure_sha256" yaml:"figure_sha256"`
	// ContractSHA256 is the digest of the contract it was built with, which
	// a checkout of magus it judges must carry. See internal/contract. The caller fills
	// it: contract imports this package, so computing it here would close a cycle.
	ContractSHA256 string `json:"contract_sha256" yaml:"contract_sha256"`
}

// NewVersionOutput is the stamp of build b, without the server half and without
// the contract digest, which can fail to hash: the caller adds it.
func NewVersionOutput(b types.MagusBuild, builtBy string) VersionOutput {
	return VersionOutput{
		Version: b.Version, Commit: b.Commit, Dirty: BuiltDirty(b), Repository: SourceRepository,
		BuildDate: b.Date, BuiltBy: builtBy, Engine: "buzz",
		Embedded: VersionEmbedded{FigureSHA256: figure.SourceSHA256()},
	}
}

// ServerProbe is the server half of `magus version`: what, if anything, the server said
// when asked.
type ServerProbe struct {
	// Version is what it reported, "" when nothing answered.
	Version string
}

// ServerLine renders the server half of the text form, against the client's own version.
//
// The unknown case is its own line rather than a comparison: an unstamped client also
// reports "unknown", so an unstamped client talking to an unstamped server would have
// read as "server: unknown", the same line two matching stamped builds print.
func ServerLine(p ServerProbe, client string) string {
	switch {
	case p.Version == "":
		return "server: not running"
	case p.Version == UnknownVersion:
		return "server: running, version not reported"
	case p.Version != client:
		return fmt.Sprintf("server: %s (differs from this client)", p.Version)
	default:
		return "server: " + p.Version
	}
}

// HasVerboseFlag reports whether args contains -v or --verbose.
func HasVerboseFlag(args []string) bool {
	for _, a := range args {
		if a == "-v" || a == "--verbose" {
			return true
		}
	}
	return false
}
