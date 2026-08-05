package spells

import (
	"fmt"
	"regexp"
	"strings"

	semver "github.com/Masterminds/semver/v3"
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
// Mixing those strings into the cache key verbatim - which is what magus did before
// this file existed - makes the key depend on things that are not the tool's version:
// the commit golangci-lint was built from, the timestamp it was built at, the Go
// version it was built WITH, docker's build hash, and the host's OS/arch. Two machines
// running the identical tool version therefore compute different keys and share no
// cache, and a distro rebuild moves the key with no version change at all.
//
// So a probe result is EXTRACTED before it is used, and then BUCKETED by what the
// spell author says may bust the cache.

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
// returns it in canonical `X.Y.Z` form. ok is false when the output carries nothing
// version-shaped, which is the caller's signal to fall back rather than an error:
// a tool is allowed to print something magus cannot parse, and refusing to run
// because of it would be a worse failure than a coarse cache key.
func ExtractVersion(output string) (string, bool) {
	m := versionPattern.FindStringSubmatch(output)
	if m == nil {
		return "", false
	}
	patch := m[3]
	if patch == "" {
		patch = "0"
	}
	v := m[1] + "." + m[2] + "." + patch
	if m[4] != "" {
		v += "-" + m[4]
	}
	return v, true
}

// Precision names how much of a probed version a derived bucket keeps.
type Precision string

const (
	// PrecisionMajor buckets 2.5.0 as ">=2.0.0 <3.0.0": any 2.x replays 2.x's cache.
	PrecisionMajor Precision = "major"
	// PrecisionMinor buckets 2.5.0 as ">=2.5.0 <2.6.0": patch upgrades replay.
	PrecisionMinor Precision = "minor"
	// PrecisionPatch buckets 2.5.0 as ">=2.5.0 <2.5.1": only prerelease/build noise
	// replays. This is the narrowest bucket that still discards build identity, and
	// it is what a spell wants when it means "the version, and nothing else about
	// how this binary was built".
	PrecisionPatch Precision = "patch"
)

// VersionBucket is one entry in a policy: either an explicit semver constraint, or a
// precision that DERIVES a constraint from whatever was probed.
//
// Both forms resolve to a constraint, which is what makes precision sugar rather than
// a second mechanism - `major` is not a special key in the cache, it is a shorthand for
// the range covering the probed version's major. Exactly one field is set.
type VersionBucket struct {
	// Constraint is an explicit semver range, e.g. ">= 1.0.0, < 2.0.0". Masterminds
	// constraint syntax, the same dialect magus.yaml's required_version already uses.
	Constraint string `json:"constraint,omitempty"`
	// Precision derives the constraint from the probed version. A precision bucket
	// always matches, so it is only ever useful last, as the catch-all.
	Precision Precision `json:"precision,omitempty"`
}

// VersionPolicy declares what may bust the cache for one probed tool.
//
// The zero value is today's behavior and stays the default: probe, and let any change
// in the extracted version move the key. A policy narrows that, and narrowing is a
// CLAIM - "this tool's output does not change across this range" - so widening a
// bucket trades cache hits for the risk of replaying an artifact a different tool
// version would not have produced. That is the author's call to make and the reason
// the default stays exact.
type VersionPolicy struct {
	// Literal is an author-supplied constant used as the version token verbatim, for a
	// tool that cannot report its own version at all. No process is spawned; the author
	// bumps the string by hand to bust the cache. When set, every other field is ignored.
	Literal string `json:"literal,omitempty"`
	// Buckets are evaluated in declaration order and the FIRST match wins, so explicit
	// constraints belong before a precision catch-all. Empty means exact.
	Buckets []VersionBucket `json:"buckets,omitempty"`
}

// IsZero reports whether the policy asks for anything beyond the exact version.
func (p VersionPolicy) IsZero() bool { return p.Literal == "" && len(p.Buckets) == 0 }

// derive builds the constraint a precision bucket stands for, given the probed version.
func derive(p Precision, v *semver.Version) (string, error) {
	switch p {
	case PrecisionMajor:
		return fmt.Sprintf(">= %d.0.0, < %d.0.0", v.Major(), v.Major()+1), nil
	case PrecisionMinor:
		return fmt.Sprintf(">= %d.%d.0, < %d.%d.0", v.Major(), v.Minor(), v.Major(), v.Minor()+1), nil
	case PrecisionPatch:
		return fmt.Sprintf(">= %d.%d.%d, < %d.%d.%d", v.Major(), v.Minor(), v.Patch(), v.Major(), v.Minor(), v.Patch()+1), nil
	default:
		return "", fmt.Errorf("unknown precision %q (want major, minor, or patch)", p)
	}
}

// VersionToken reduces a probe's raw output to the string that enters the cache key,
// and returns a note when it had to degrade.
//
// The note is never an error. Every degradation here - unparseable output, a version
// outside every declared bucket - is a case where magus can still produce a CORRECT
// key by being more conservative than the author asked for, and failing the run
// instead would break a build for a cache-key reason. A tool upgrade that lands
// outside the declared ranges should cost a cache miss, not a red pipeline.
func VersionToken(output string, policy VersionPolicy) (token string, note string) {
	if policy.Literal != "" {
		return policy.Literal, ""
	}

	raw := strings.TrimSpace(output)
	probed, ok := ExtractVersion(raw)
	if !ok {
		// Nothing version-shaped. The whole output is the token, which is exactly what
		// magus did for every tool before extraction existed - correct, just coarse.
		return raw, "no semver-shaped token in probe output; keying on the whole output"
	}
	if len(policy.Buckets) == 0 {
		return probed, ""
	}

	v, err := semver.NewVersion(probed)
	if err != nil {
		return probed, fmt.Sprintf("extracted %q is not a usable semver (%v); keying on it exactly", probed, err)
	}

	for i, b := range policy.Buckets {
		constraint := b.Constraint
		if b.Precision != "" {
			derived, err := derive(b.Precision, v)
			if err != nil {
				return probed, fmt.Sprintf("bucket %d: %v; keying on %s exactly", i, err, probed)
			}
			// A derived bucket is built FROM the probed version, so it always contains
			// it. Skipping the match check keeps the prerelease case honest: semver
			// constraints exclude prereleases from a plain range, which would push
			// 2.5.0-rc1 out of its own major bucket.
			return derived, ""
		}
		c, err := semver.NewConstraint(constraint)
		if err != nil {
			return probed, fmt.Sprintf("bucket %d: unparseable constraint %q (%v); keying on %s exactly", i, constraint, err, probed)
		}
		if c.Check(v) {
			return constraint, ""
		}
	}
	return probed, fmt.Sprintf("%s matches none of the %d declared version buckets; keying on it exactly", probed, len(policy.Buckets))
}
