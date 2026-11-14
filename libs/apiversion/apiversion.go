// Package apiversion names, reads and refuses versioned records shaped as Kubernetes objects.
package apiversion

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// APIVersion names a versioned format Kubernetes-style, as
// `example.widget/v1alpha1`: a group, a slash, then v<major> with an
// optional alpha<n> or beta<n>. The group is a lowercase DNS-1123
// subdomain with at least one dot. Within a group the versions order
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

const maxGroupLength = 253

var apiVersionRe = regexp.MustCompile(
	`^([a-z0-9](?:[-a-z0-9]*[a-z0-9])?(?:\.[a-z0-9](?:[-a-z0-9]*[a-z0-9])?)+)/v([1-9][0-9]*)(?:(alpha|beta)([1-9][0-9]*))?$`)

// Parse returns s as an APIVersion, or an error naming the
// shape s breaks.
func Parse(s string) (APIVersion, error) {
	if !APIVersion(s).Valid() {
		return "", fmt.Errorf("api version %q is not <group>/v<major>[alpha<n>|beta<n>] with a dotted DNS-1123 group, as in %q",
			s, "example.widget/v1")
	}
	return APIVersion(s), nil
}

// Valid reports whether v parses.
func (v APIVersion) Valid() bool {
	return apiVersionRe.MatchString(string(v)) && len(v.components().group) <= maxGroupLength
}

func (v APIVersion) String() string { return string(v) }

// Group is `example.widget` in `example.widget/v1alpha1`. It is "" when v
// is malformed.
func (v APIVersion) Group() string { return v.components().group }

// Version is `v1alpha1` in `example.widget/v1alpha1`. It is "" when v is
// malformed.
func (v APIVersion) Version() string {
	if !v.Valid() {
		return ""
	}
	_, version, _ := strings.Cut(string(v), "/")
	return version
}

// Stability is alpha, beta or stable. It is "" when v is malformed.
func (v APIVersion) Stability() Stability { return v.components().stability }

// Compare orders by group, then by version within the group. A
// malformed version sorts first.
func (v APIVersion) Compare(w APIVersion) int {
	x, y := v.components(), w.components()
	return cmp.Or(
		cmp.Compare(x.group, y.group),
		cmp.Compare(x.major, y.major),
		cmp.Compare(stabilityRank(x.stability), stabilityRank(y.stability)),
		cmp.Compare(x.minor, y.minor),
	)
}

// Refuse is a reader's error for a bare version file that carries got
// where the reader reads only v. It is nil when got is v, and a
// *RefusedError otherwise, with no kind on either side.
func (v APIVersion) Refuse(got APIVersion) error {
	if got == v {
		return nil
	}
	return &RefusedError{Want: TypeMeta{APIVersion: v}, Got: TypeMeta{APIVersion: got}}
}

// ErrNewer matches a RefusedError whose record is a newer version of the
// group the reader reads, so the fix is a newer reader.
var ErrNewer = errors.New("apiversion: newer than this reader")

type apiVersionComponents struct {
	group        string
	major, minor int
	stability    Stability
}

func (v APIVersion) components() apiVersionComponents {
	m := apiVersionRe.FindStringSubmatch(string(v))
	if m == nil {
		return apiVersionComponents{}
	}
	c := apiVersionComponents{group: m[1], stability: StabilityStable}
	c.major, _ = strconv.Atoi(m[2]) // apiVersionRe matched digits
	if m[3] != "" {
		c.stability = Stability(m[3])
		c.minor, _ = strconv.Atoi(m[4]) // apiVersionRe matched digits
	}
	return c
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
