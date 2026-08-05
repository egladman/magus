package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileInfoBuzzObject(t *testing.T) {
	fi := FileInfo{Size: 42, Mtime: 1000.5, Mode: 0o644, IsDir: true}
	assert.Equal(t, BuzzObject{
		"size":   int64(42),
		"mtime":  1000.5,
		"mode":   int64(0o644),
		"is_dir": true,
	}, fi.BuzzObject())
}

func TestHTTPResponseBuzzObject(t *testing.T) {
	r := HTTPResponse{Status: 200, Body: "ok", Headers: map[string]string{"Content-Type": "text/plain"}}
	assert.Equal(t, BuzzObject{
		"status":  200,
		"body":    "ok",
		"headers": map[string]string{"Content-Type": "text/plain"},
	}, r.BuzzObject())
}

func TestSemverVersionBuzzObject(t *testing.T) {
	v := SemverVersion{Major: 1, Minor: 2, Patch: 3, Prerelease: "rc1", Metadata: "build5", Original: "1.2.3-rc1+build5"}
	assert.Equal(t, BuzzObject{
		"major":      1,
		"minor":      2,
		"patch":      3,
		"prerelease": "rc1",
		"metadata":   "build5",
		"original":   "1.2.3-rc1+build5",
	}, v.BuzzObject())
}

func TestSemverVersionString(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    SemverVersion
		want string
	}{
		{"bare", SemverVersion{Major: 1, Minor: 2, Patch: 3}, "v1.2.3"},
		{"prerelease", SemverVersion{Major: 1, Minor: 2, Patch: 3, Prerelease: "rc1"}, "v1.2.3-rc1"},
		{"metadata", SemverVersion{Major: 1, Minor: 2, Patch: 3, Metadata: "build5"}, "v1.2.3+build5"},
		{"prerelease and metadata", SemverVersion{Major: 1, Minor: 2, Patch: 3, Prerelease: "rc1", Metadata: "build5"}, "v1.2.3-rc1+build5"},
		{"zero value", SemverVersion{}, "v0.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.v.String())
		})
	}
}

// String is the canonical rendering, not a round-trip of what the user wrote:
// a non-canonical Original (a leading zero, a missing "v") must NOT survive it.
func TestSemverVersionStringNotOriginal(t *testing.T) {
	v := SemverVersion{Major: 1, Minor: 2, Patch: 3, Original: "1.02.3"}
	assert.Equal(t, "v1.2.3", v.String())
	assert.NotEqual(t, v.Original, v.String())
}

func TestSemverNextBuzzObject(t *testing.T) {
	n := SemverNext{Major: "v1.0.0", Minor: "v0.2.0", Patch: "v0.1.1"}
	assert.Equal(t, BuzzObject{
		"major": "v1.0.0",
		"minor": "v0.2.0",
		"patch": "v0.1.1",
	}, n.BuzzObject())
}

func TestURLBuzzObject(t *testing.T) {
	u := URL{Scheme: "https", Host: "example.com", Port: "8443", Path: "/a", Query: "x=1", Fragment: "top"}
	assert.Equal(t, BuzzObject{
		"scheme":   "https",
		"host":     "example.com",
		"port":     "8443",
		"path":     "/a",
		"query":    "x=1",
		"fragment": "top",
	}, u.BuzzObject())
}

// TestBuzzObject_NamedStringsCrossAsPlainStrings guards a failure with no symptom.
// BuzzObject is a map[string]any, and the encoder that turns one into a Buzz value type
// -switches on IDENTITY: a named string type (DoctorCheckStatus, TargetRunState) matches
// neither `case string` nor `case []string`, so the field silently arrived as null and a
// magusfile branching on `check.status == "fail"` compared against nothing. A named MAP
// type sprang this same trap once before, which is why host.AnyVal carries an explicit
// BuzzObject case.
//
// Asserted here rather than at the encoder because this is the generator's contract: the
// value it puts in the map must already be a plain string.
func TestBuzzObject_NamedStringsCrossAsPlainStrings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		obj   BuzzObject
		field string
		want  string
	}{
		{"DoctorCheck.status", DoctorCheck{Status: DoctorFail}.BuzzObject(), "status", "fail"},
		{"StatusTargetRun.state", StatusTargetRun{State: TargetRunPassed}.BuzzObject(), "state", "passed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.obj[tc.field]
			require.True(t, ok, "field must be present")
			assert.IsType(t, "", got, "must be a plain string, not the named type, or it crosses as null")
			assert.Equal(t, tc.want, got)
		})
	}
}
