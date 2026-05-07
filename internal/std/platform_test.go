package std

import (
	"context"
	"testing"
)

func TestHostPlatform_NonEmpty(t *testing.T) {
	osName, arch, _ := HostPlatform()
	if osName == "" {
		t.Error("HostPlatform() osName is empty")
	}
	if arch == "" {
		t.Error("HostPlatform() arch is empty")
	}
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
	for _, tc := range []struct{ in, style, want string }{
		// Normalize varied spellings to canonical Go GOARCH.
		{"x86_64", "", "amd64"},
		{"X86_64", "", "amd64"},
		{"amd64", "", "amd64"},
		{"aarch64", "", "arm64"},
		{"arm64", "", "arm64"},
		{"armv7l", "", "arm"},
		{"i686", "", "386"},
		{"loongarch64", "", "loong64"},
		// Inverse: render canonical in another convention.
		{"arm64", "uname", "aarch64"},
		{"amd64", "uname", "x86_64"},
		{"aarch64", "go", "arm64"},
		// uname form with no distinct spelling falls back to the Go form.
		{"s390x", "uname", "s390x"},
		// Unrecognized → "".
		{"sparc", "", ""},
		{"", "", ""},
	} {
		got, err := PlatformArch(context.Background(), tc.in, tc.style)
		if err != nil {
			t.Errorf("PlatformArch(%q, %q): %v", tc.in, tc.style, err)
			continue
		}
		if got != tc.want {
			t.Errorf("PlatformArch(%q, %q) = %q, want %q", tc.in, tc.style, got, tc.want)
		}
	}
}

func TestPlatformOS(t *testing.T) {
	for _, tc := range []struct{ in, style, want string }{
		{"Darwin", "", "darwin"},
		{"macOS", "", "darwin"},
		{"mac", "", "darwin"},
		{"OSX", "", "darwin"},
		{"win", "", "windows"},
		{"Windows", "", "windows"},
		{"gnu/linux", "", "linux"},
		// Inverse.
		{"darwin", "uname", "Darwin"},
		{"macOS", "uname", "Darwin"},
		{"linux", "uname", "Linux"},
		// Unrecognized → "".
		{"haiku", "", ""},
	} {
		got, err := PlatformOS(context.Background(), tc.in, tc.style)
		if err != nil {
			t.Errorf("PlatformOS(%q, %q): %v", tc.in, tc.style, err)
			continue
		}
		if got != tc.want {
			t.Errorf("PlatformOS(%q, %q) = %q, want %q", tc.in, tc.style, got, tc.want)
		}
	}
}

func TestPlatformUnknownStyle(t *testing.T) {
	if _, err := PlatformArch(context.Background(), "amd64", "bogus"); err == nil {
		t.Error("PlatformArch with unknown style: expected error, got nil")
	}
	if _, err := PlatformOS(context.Background(), "linux", "bogus"); err == nil {
		t.Error("PlatformOS with unknown style: expected error, got nil")
	}
}
