package types

import (
	"cmp"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// APIVersion names a versioned format Kubernetes-style, as
// `magus.job/v1alpha1`: a group, a slash, then v<major> with an
// optional alpha<n> or beta<n>. Within a group the versions order
// v1alpha1 < v1alpha2 < v1beta1 < v1 < v2alpha1.
//
// A reader refuses every version but its own. Nothing migrates.
type APIVersion string

// Stability is what a version promises. Alpha may break in any
// release, beta breaks only after a deprecation, stable never breaks.
type Stability string

const (
	StabilityAlpha  Stability = "alpha"
	StabilityBeta   Stability = "beta"
	StabilityStable Stability = "stable"
)

var apiVersionRe = regexp.MustCompile(
	`^([a-z][a-z0-9-]*(?:\.[a-z][a-z0-9-]*)*)/v([1-9][0-9]*)(?:(alpha|beta)([1-9][0-9]*))?$`)

// ParseAPIVersion returns s as an APIVersion, or an error naming the
// shape s breaks.
func ParseAPIVersion(s string) (APIVersion, error) {
	if !apiVersionRe.MatchString(s) {
		return "", fmt.Errorf("api version %q is not <group>/v<major>[alpha<n>|beta<n>], as in %q",
			s, JobAPIVersion)
	}
	return APIVersion(s), nil
}

// Valid reports whether v parses.
func (v APIVersion) Valid() bool { return apiVersionRe.MatchString(string(v)) }

func (v APIVersion) String() string { return string(v) }

// Group is `magus.job` in `magus.job/v1alpha1`. It is "" when v is
// malformed.
func (v APIVersion) Group() string { return v.parts().group }

// Version is `v1alpha1` in `magus.job/v1alpha1`. It is "" when v is
// malformed.
func (v APIVersion) Version() string {
	if !v.Valid() {
		return ""
	}
	_, version, _ := strings.Cut(string(v), "/")
	return version
}

// Stability is alpha, beta or stable. It is "" when v is malformed.
func (v APIVersion) Stability() Stability { return v.parts().stability }

// Compare orders by group, then by version within the group. A
// malformed version sorts first.
func (v APIVersion) Compare(w APIVersion) int {
	x, y := v.parts(), w.parts()
	return cmp.Or(
		cmp.Compare(x.group, y.group),
		cmp.Compare(x.major, y.major),
		cmp.Compare(stabilityRank(x.stability), stabilityRank(y.stability)),
		cmp.Compare(x.minor, y.minor),
	)
}

// Refuse is a reader's error for a record that carries got where the
// reader reads only v. It is nil when got is v. what names the record;
// fix is what the person does next, such as deleting the store. A newer
// version of the same group asks for a newer magus instead.
func (v APIVersion) Refuse(what string, got APIVersion, fix string) error {
	switch {
	case got == v:
		return nil
	case got == "":
		return fmt.Errorf("%s carries no api_version, so an older magus wrote it, "+
			"and this magus reads only %s: %s", what, v, fix)
	case got.Group() == v.Group() && got.Compare(v) > 0:
		return fmt.Errorf("%s is %s, newer than the %s this magus reads: upgrade magus", what, got, v)
	default:
		return fmt.Errorf("%s is %s and this magus reads only %s: %s", what, got, v, fix)
	}
}

type apiVersionParts struct {
	group        string
	major, minor int
	stability    Stability
}

func (v APIVersion) parts() apiVersionParts {
	m := apiVersionRe.FindStringSubmatch(string(v))
	if m == nil {
		return apiVersionParts{}
	}
	p := apiVersionParts{group: m[1], stability: StabilityStable}
	p.major, _ = strconv.Atoi(m[2]) // apiVersionRe matched digits
	if m[3] != "" {
		p.stability = Stability(m[3])
		p.minor, _ = strconv.Atoi(m[4]) // apiVersionRe matched digits
	}
	return p
}

func stabilityRank(s Stability) int {
	switch s {
	case StabilityAlpha:
		return 1
	case StabilityBeta:
		return 2
	case StabilityStable:
		return 3
	default:
		return 0
	}
}
