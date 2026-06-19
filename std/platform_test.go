package std

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostPlatform_NonEmpty(t *testing.T) {
	osName, arch, _ := HostPlatform()
	assert.NotEmpty(t, osName, "HostPlatform() osName is empty")
	assert.NotEmpty(t, arch, "HostPlatform() arch is empty")
}

func TestHostPlatform_KnownOS(t *testing.T) {
	osName, _, _ := HostPlatform()
	known := map[string]bool{
		"linux": true, "darwin": true, "windows": true,
		"freebsd": true, "netbsd": true, "openbsd": true,
	}
	if !known[osName] {
		// Unexpected GOOS is not an error — just document it.
		t.Logf("HostPlatform() osName = %q (not in known set)", osName)
	}
}

func TestPlatformArch(t *testing.T) {
	arch := func(in, style string) string {
		got, err := PlatformArch(context.Background(), in, style)
		require.NoError(t, err, "PlatformArch(%q, %q)", in, style)
		return got
	}

	// Normalize varied spellings to canonical Go GOARCH.
	assert.Equal(t, "amd64", arch("x86_64", ""))
	assert.Equal(t, "amd64", arch("X86_64", ""))
	assert.Equal(t, "amd64", arch("amd64", ""))
	assert.Equal(t, "arm64", arch("aarch64", ""))
	assert.Equal(t, "arm64", arch("arm64", ""))
	assert.Equal(t, "arm", arch("armv7l", ""))
	assert.Equal(t, "386", arch("i686", ""))
	assert.Equal(t, "loong64", arch("loongarch64", ""))
	// Inverse: render canonical in another convention.
	assert.Equal(t, "aarch64", arch("arm64", "uname"))
	assert.Equal(t, "x86_64", arch("amd64", "uname"))
	assert.Equal(t, "arm64", arch("aarch64", "go"))
	// uname form with no distinct spelling falls back to the Go form.
	assert.Equal(t, "s390x", arch("s390x", "uname"))
	// Unrecognized → "".
	assert.Equal(t, "", arch("sparc", ""))
	assert.Equal(t, "", arch("", ""))
}

func TestPlatformOS(t *testing.T) {
	osFn := func(in, style string) string {
		got, err := PlatformOS(context.Background(), in, style)
		require.NoError(t, err, "PlatformOS(%q, %q)", in, style)
		return got
	}

	assert.Equal(t, "darwin", osFn("Darwin", ""))
	assert.Equal(t, "darwin", osFn("macOS", ""))
	assert.Equal(t, "darwin", osFn("mac", ""))
	assert.Equal(t, "darwin", osFn("OSX", ""))
	assert.Equal(t, "windows", osFn("win", ""))
	assert.Equal(t, "windows", osFn("Windows", ""))
	assert.Equal(t, "linux", osFn("gnu/linux", ""))
	// Inverse.
	assert.Equal(t, "Darwin", osFn("darwin", "uname"))
	assert.Equal(t, "Darwin", osFn("macOS", "uname"))
	assert.Equal(t, "Linux", osFn("linux", "uname"))
	// Unrecognized → "".
	assert.Equal(t, "", osFn("haiku", ""))
}

func TestPlatformUnknownStyle(t *testing.T) {
	_, err := PlatformArch(context.Background(), "amd64", "bogus")
	assert.Error(t, err, "PlatformArch with unknown style: expected error")
	_, err = PlatformOS(context.Background(), "linux", "bogus")
	assert.Error(t, err, "PlatformOS with unknown style: expected error")
}
