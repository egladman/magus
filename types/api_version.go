package types

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/egladman/magus/libs/apiversion"
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
func (v APIVersion) Group() string { return apiversion.APIVersion(v).Group() }

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
func (v APIVersion) Stability() Stability {
	return Stability(apiversion.APIVersion(v).Stability())
}

// Compare orders by group, then by version within the group. A
// malformed version sorts first.
func (v APIVersion) Compare(w APIVersion) int {
	return apiversion.APIVersion(v).Compare(apiversion.APIVersion(w))
}

// RefuseVersion wraps apiversion's refusal of got where the reader reads
// only want. It is nil when got is want. what names the record; fix is
// what the person does next, such as deleting the store. A newer version
// of the same group asks for a newer magus instead.
func RefuseVersion(want, got APIVersion, what, fix string) error {
	err := apiversion.APIVersion(want).Refuse(apiversion.APIVersion(got))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, apiversion.ErrNewer):
		return fmt.Errorf("%s: %w", what, refusal{err: err, fix: "upgrade magus"})
	default:
		return fmt.Errorf("%s: %w", what, refusal{err: err, fix: fix})
	}
}

// refusal is apiversion's refusal followed by what the person does next.
type refusal struct {
	err error
	fix string
}

func (r refusal) Error() string { return fmt.Sprintf("%s: %s", r.err, r.fix) }

func (r refusal) Unwrap() error { return r.err }
