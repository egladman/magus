package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFileInfoToMap(t *testing.T) {
	fi := FileInfo{Size: 42, Mtime: 1000.5, Mode: 0o644, IsDir: true}
	assert.Equal(t, map[string]any{
		"size":   int64(42),
		"mtime":  1000.5,
		"mode":   int64(0o644),
		"is_dir": true,
	}, fi.ToMap())
}

func TestHTTPResponseToMap(t *testing.T) {
	r := HTTPResponse{Status: 200, Body: "ok", Headers: map[string]string{"Content-Type": "text/plain"}}
	assert.Equal(t, map[string]any{
		"status":  200,
		"body":    "ok",
		"headers": map[string]string{"Content-Type": "text/plain"},
	}, r.ToMap())
}

func TestSemverVersionToMap(t *testing.T) {
	v := SemverVersion{Major: 1, Minor: 2, Patch: 3, Prerelease: "rc1", Metadata: "build5", Original: "1.2.3-rc1+build5"}
	assert.Equal(t, map[string]any{
		"major":      1,
		"minor":      2,
		"patch":      3,
		"prerelease": "rc1",
		"metadata":   "build5",
		"original":   "1.2.3-rc1+build5",
	}, v.ToMap())
}

func TestURLToMap(t *testing.T) {
	u := URL{Scheme: "https", Host: "example.com", Port: "8443", Path: "/a", Query: "x=1", Fragment: "top"}
	assert.Equal(t, map[string]any{
		"scheme":   "https",
		"host":     "example.com",
		"port":     "8443",
		"path":     "/a",
		"query":    "x=1",
		"fragment": "top",
	}, u.ToMap())
}
