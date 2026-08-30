package spells

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

// A version probe's raw stdout is not a version. Every tool magus ships a probe for
// wraps its version in prose, and several wrap BUILD IDENTITY around it:
//
//	go version go1.26.0 linux/amd64
//	golangci-lint has version 2.5.0 built with go1.25.1 from ff63786c on 2025-09-21T19:04:05Z
//	node --version                v22.22.2
//	python3 --version             Python 3.11.15
//	docker --version              Docker version 29.3.1, build c2be9cc
//	bash --version                GNU bash, version 5.2.21(1)-release (x86_64-pc-linux-gnu)
//
// Mixing those into the cache key verbatim makes it depend on things that are not the
// tool's version - the commit golangci-lint was built from, the Go version it was built
// WITH, docker's build hash, the host's OS/arch - so two machines running the identical
// version compute different keys and share no cache.
//
// So a probe result is EXTRACTED before use, then narrowed to the component the spell
// says matters.

// versionPattern finds the first semver-shaped token in a probe's output.
//
// It deliberately allows leading letters (`go1.26.0`) rather than requiring a word
// boundary, because Go's own probe glues the prefix on. Taking the FIRST match is what
// makes that safe in the noisier cases: golangci-lint reports its own version before
// the Go version it was built with, and docker reports its version before its build
// hash, so first-match lands on the tool's own version in every sample above.
//
// Two digit groups are required. A single group would match the `(1)` in bash's
// `5.2.21(1)-release` and the `_64` in `x86_64`; requiring `N.N` means the only
// candidates are things already shaped like a version.
var versionPattern = regexp.MustCompile(`v?(\d+)\.(\d+)(?:\.(\d+))?(?:-([0-9A-Za-z][0-9A-Za-z.-]*))?`)

// ExtractVersion pulls the first semver-shaped token out of a probe's output and
// returns it in canonical `vX.Y.Z` form. ok is false when the output carries nothing
// version-shaped, which is the caller's signal to fall back rather than an error: a
// tool is allowed to print something magus cannot parse, and refusing to run because
// of it would be a worse failure than a coarse cache key.
//
// The `v` prefix is not decoration. golang.org/x/mod/semver - whose Major/MajorMinor
// ARE the narrowing this file needs - requires it and returns "" without it, so
// emitting canonical form here is what lets those functions be used directly instead
// of wrapped.
func ExtractVersion(output string) (string, bool) {
	m := versionPattern.FindStringSubmatch(output)
	if m == nil {
		return "", false
	}
	patch := m[3]
	if patch == "" {
		patch = "0"
	}
	v := "v" + m[1] + "." + m[2] + "." + patch
	if m[4] != "" {
		v += "-" + m[4]
	}
	if !semver.IsValid(v) {
		return "", false
	}
	return v, true
}

// VersionComponent names how much of a probed version reaches the cache key.
//
// The values are the SemVer spec's own component names, and the same strings
// node-semver's inc() accepts and diff() returns - nothing here is magus vocabulary a
// reader has to learn. It is a defined type rather than a bare string so a Go SDK
// caller cannot typo one silently; a magusfile spelling is checked at decode.
type VersionComponent string

const (
	// VersionNone is the zero value: no extraction happens and the probe's whole
	// output keys the cache. It is not a component - it is the ABSENCE of a request
	// to find one, and it is the default precisely because guessing which number in
	// a tool's output is its version is a guess magus should not make unasked.
	VersionNone VersionComponent = ""
	// VersionMajor extracts a semver and keeps the major, so every 2.x shares one entry.
	VersionMajor VersionComponent = "major"
	// VersionMinor extracts a semver and keeps major.minor, so patch releases share one.
	VersionMinor VersionComponent = "minor"
	// VersionPatch extracts a semver and keeps major.minor.patch. It narrows nothing a
	// team has to reason about - two builds of one version agree - so it is the right
	// declaration for a tool that pads its version line with build identity.
	VersionPatch VersionComponent = "patch"
)

// KeyFunc returns the narrowing this component names, as the same func(string) string
// shape golang.org/x/mod/semver already exports - so a Go SDK caller can pass
// semver.Major directly instead of going through a VersionComponent at all.
//
// It errors rather than falling back, because an unknown component reaching here is a
// declaration bug: the callers that accept authored input validate first.
func (c VersionComponent) KeyFunc() (VersionKeyFunc, error) {
	switch c {
	case VersionMajor:
		return semver.Major, nil
	case VersionMinor:
		return semver.MajorMinor, nil
	case VersionPatch:
		// Canonical already drops build metadata; trimming the prerelease off it
		// leaves exactly vX.Y.Z, composed from x/mod rather than re-parsed here.
		return func(v string) string {
			return strings.TrimSuffix(semver.Canonical(v), semver.Prerelease(v))
		}, nil
	default:
		return nil, fmt.Errorf("unknown version component %s; want one of %s",
			c, strings.Join(c.Values(), ", "))
	}
}

// VersionKeyFunc narrows an extracted version to the string that enters the cache key.
//
// It is a func type rather than a single-method interface because that is where Go
// landed: slices.SortFunc over sort.Interface, http.HandlerFunc over a Handler you
// must declare a type for. golang.org/x/mod/semver's Major, MajorMinor, and Canonical
// all satisfy it as written.
type VersionKeyFunc func(version string) string

// VersionKey declares what a probed tool contributes to the cache key.
//
// The zero value keys on the probe's WHOLE output. Extraction is opt-in because finding
// the version means guessing which number is the version, and that guess is wrong often
// enough to matter: govulncheck prints the Go version first and its vulnerability
// database's date last, so guessing picks the wrong number AND discards the field that
// decides whether the verdict holds.
//
// Declaring UpTo is two requests at once: extract a semver, and keep this much of it.
// UpTo patch exists to shed the commit hashes and build timestamps tools pad with.
type VersionKey struct {
	// Const is an author-supplied constant used as the token verbatim, for a tool that
	// cannot report its own version at all. No process is spawned; the author edits the
	// string by hand to invalidate. When set, UpTo is ignored.
	Const string `json:"const,omitempty"`
	// UpTo asks for extraction and names how much of the extracted version to keep.
	// Absent means no extraction: the whole output keys the cache.
	UpTo VersionComponent `json:"upTo,omitempty"`
}

// IsZero reports whether the key asks for anything beyond the exact version.
func (k VersionKey) IsZero() bool { return k.Const == "" && k.UpTo == VersionNone }

// VersionToken reduces a probe's raw output to the string that enters the cache key,
// and returns a note when it had to degrade.
//
// The note is never an error. Every degradation here is a case where magus can still
// produce a CORRECT key by being more conservative than the author asked for, and
// failing the run instead would break a build for a cache-key reason.
func VersionToken(output string, key VersionKey) (token string, note string) {
	if key.Const != "" {
		return key.Const, ""
	}
	raw := strings.TrimSpace(output)
	if key.UpTo == VersionNone {
		// No extraction was asked for, so none happens. Not a degradation - the
		// default, and the only answer that cannot silently discard something the
		// tool considered part of its identity.
		return raw, ""
	}
	probed, ok := ExtractVersion(raw)
	if !ok {
		return raw, "no semver-shaped token in probe output; keying on the whole output"
	}
	fn, err := key.UpTo.KeyFunc()
	if err != nil {
		return probed, fmt.Sprintf("%v; keying on %s exactly", err, probed)
	}
	narrowed := fn(probed)
	if narrowed == "" {
		return probed, fmt.Sprintf("could not narrow %s to %s; keying on it exactly", probed, key.UpTo)
	}
	return narrowed, ""
}

// VersionBounds is the window of versions a binary is allowed to report: an
// inclusive floor and an exclusive ceiling, each a plain version.
//
// Two named bounds rather than one constraint string, deliberately: a range language is
// a mini-language in a field, where the comma means AND and `^` and `~` mean materially
// different things across npm, Composer and the library this parses with. A toolchain
// window needs a floor and sometimes a ceiling, not disjunction.
//
// The shape also makes a defect unrepresentable: the single-constraint form reported
// every failure as ToolTooOld, so a too-NEW binary was told it was too old.
//
// Below is EXCLUSIVE because that is the bound people mean. "Not the 25 line" written as
// an inclusive `<= 24` rejects 24.19.0.
//
// Declared by two owners with different authority, and intersected before use: a spell
// states what its ops need to function at all, and a workspace states policy. Neither
// can loosen the other.
type VersionBounds struct {
	// Min is the oldest version accepted, inclusive. Empty accepts any.
	Min string `json:"min,omitempty" yaml:"min,omitempty"`
	// Below is the first version REJECTED, exclusive. Empty accepts any.
	Below string `json:"below,omitempty" yaml:"below,omitempty"`
}

// IsZero reports whether the window constrains nothing.
func (b VersionBounds) IsZero() bool { return b.Min == "" && b.Below == "" }

// Verdict names how a probed version relates to a window.
type Verdict int

const (
	// VerdictInside means the version satisfies every bound that was declared.
	VerdictInside Verdict = iota
	// VerdictTooOld means the version is below Min.
	VerdictTooOld
	// VerdictTooNew means the version is at or above Below.
	VerdictTooNew
	// VerdictUnknown means the comparison could not be made - an unparsable probed
	// version, or a bound that is not a version. It is never a violation: a window
	// magus cannot evaluate must not fail a build, the same way an unprobeable tool
	// is not "too old".
	VerdictUnknown
)

// Check reports how version sits in the window.
//
// A bound that fails to parse yields VerdictUnknown rather than being skipped. Skipping
// would let a typo silently widen the window to everything, which is the failure mode a
// declared bound exists to prevent; callers that can reject at declaration time do so,
// and this is the backstop for the ones that cannot.
func (b VersionBounds) Check(version string) Verdict {
	if b.IsZero() {
		return VerdictInside
	}
	v, ok := normalizeBound(version)
	if !ok {
		return VerdictUnknown
	}
	if b.Min != "" {
		min, ok := normalizeBound(b.Min)
		if !ok {
			return VerdictUnknown
		}
		if semver.Compare(v, min) < 0 {
			return VerdictTooOld
		}
	}
	if b.Below != "" {
		ceiling, ok := normalizeBound(b.Below)
		if !ok {
			return VerdictUnknown
		}
		if semver.Compare(v, ceiling) >= 0 {
			return VerdictTooNew
		}
	}
	return VerdictInside
}

// ValidBound reports whether s is a bound [VersionBounds.Check] can compare.
//
// Exported so a declaration is validated with the SAME parser that later compares
// it. Validating with a second, looser one accepts bounds Check then cannot read,
// and every Check against that tool degrades to VerdictUnknown - a window that
// silently constrains nothing, which is the failure a declared bound exists to
// prevent.
func ValidBound(s string) bool {
	_, ok := normalizeBound(s)
	return ok
}

// normalizeBound puts an authored version into the form x/mod/semver compares.
//
// Authors write "1.21", not "v1.21": the `v` is Go's spelling, not the ecosystem's, and
// requiring it in a magusfile or magus.yaml would be magus vocabulary leaking into a
// field about node or python. It is added here rather than at every call site.
//
// Partial versions are deliberately left partial. x/mod compares "v1.21" as "v1.21.0",
// which is what `min = "1.21"` and `below = "25"` both already mean to the person who
// wrote them.
func normalizeBound(v string) (string, bool) {
	if v == "" {
		return "", false
	}
	if v[0] != 'v' {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return "", false
	}
	return v, true
}

// Intersect returns the narrower of two windows, bound by bound.
//
// Narrower always wins because the two declarations answer different questions and
// neither may relax the other: a spell says what its ops need to run, a workspace says
// what it has qualified. An empty bound on either side contributes nothing, so a
// workspace that sets only a ceiling keeps the spell's floor.
//
// An unparsable bound is kept rather than discarded. Dropping it would silently widen
// the result, and Check turns it into VerdictUnknown where the reader can see it.
func (b VersionBounds) Intersect(other VersionBounds) VersionBounds {
	out := b
	if higher(other.Min, b.Min) {
		out.Min = other.Min
	}
	if lower(other.Below, b.Below) {
		out.Below = other.Below
	}
	return out
}

// higher reports whether candidate is a tighter floor than current. An empty candidate
// never wins; an empty current always loses to a real one. An unparsable candidate
// loses, which leaves the existing bound in place for Check to report as unknown.
func higher(candidate, current string) bool { return tighter(candidate, current, 1) }

// lower reports whether candidate is a tighter ceiling than current.
func lower(candidate, current string) bool { return tighter(candidate, current, -1) }

// tighter is higher and lower with the comparison sign as the only difference between
// them. want is the semver.Compare result that means candidate narrows the window.
func tighter(candidate, current string, want int) bool {
	if candidate == "" {
		return false
	}
	if current == "" {
		return true
	}
	c, ok := normalizeBound(candidate)
	if !ok {
		return false
	}
	cur, ok := normalizeBound(current)
	if !ok {
		return false
	}
	return semver.Compare(c, cur) == want
}

// Tool is everything a spell declares about one binary it drives.
//
// Keyed by bin name in Descriptor.Tools, which is what lets an op resolve its own
// entry through the Command.Bin it already names.
type Tool struct {
	// Probe is the command that prints this binary's version, its result narrowed by
	// Key and mixed into the cache key. A zero Command means magus never asks -
	// correct for a tool that cannot report one, where Key.Const supplies the token.
	//
	// A Command rather than a bare argv so it matches Ready below: both are "run this
	// and read the result", and two shapes for that inside one record is the kind of
	// seam a reader has to hold in their head for no reason.
	Probe Command `json:"probe,omitempty"`
	// Key narrows what Probe's output contributes to the cache key. The zero value
	// keys on the whole output; see VersionKey.
	Key VersionKey `json:"key,omitempty"`
	// Ready gates an op on this binary being usable, for a client whose server may be
	// down. Its result is a precondition and never enters a cache key.
	Ready Command `json:"ready,omitempty"`
	// Supported is the version window this spell's ops work against. Empty accepts any
	// version.
	//
	// It is the fourth question about a tool, after does it exist, what version, and is
	// it usable. Without it a too-old binary fails with whatever that tool says about
	// an unrecognized flag - the same misleading failure readiness exists to prevent,
	// one step over. Checked against the extracted version, so it needs Probe.
	//
	// It states what the OPS need, not what a repo has qualified: "go::test does not
	// work below 1.21" belongs here, "we have not moved to node 25 yet" belongs in the
	// workspace's own bounds, which are intersected with this one.
	Supported VersionBounds `json:"supported,omitempty"`
	// Diagnostics names the convention this binary prints findings in; empty means
	// prose. On the tool rather than the op because the format is the binary's:
	// hadolint reports the same way whichever op invokes it.
	Diagnostics DiagnosticFormat `json:"diagnostics,omitempty"`
}

// HasProbe reports whether magus can learn a version for this tool, by running one or
// by being handed a constant.
func (t Tool) HasProbe() bool { return t.Probe.Bin != "" || t.Key.Const != "" }
