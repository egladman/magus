package commands

import (
	"testing"
)

// -C is bound as the short form of --root, but the pre-scan that peeks the root
// before config loading only matched -root/--root. `magus -C /other/ws build`
// therefore loaded THIS workspace's magus.yaml and ran it against the OTHER
// workspace's magusfile, silently. -c stays the short form of --config
// (config.ExtractFlag owns that one), so the two must not cross.
func TestExtractRootFlagReadsTheShortForm(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"-C space", []string{"-C", "ws", "build"}, "ws"},
		{"--C space", []string{"--C", "ws", "build"}, "ws"},
		{"-C equals", []string{"-C=ws", "build"}, "ws"},
		{"--C equals", []string{"--C=ws", "build"}, "ws"},
		{"-C among other args", []string{"run", "-C", "ws", "build"}, "ws"},
		{"-C stops at the separator", []string{"run", "build", "--", "-C", "ws"}, ""},
		{"lowercase -c is config, not root", []string{"-c", "a.yaml", "build"}, ""},
		{"lowercase -c= is config, not root", []string{"-c=a.yaml", "build"}, ""},
		{"-C with no value", []string{"-C"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractRootFlag(tc.args); got != tc.want {
				t.Fatalf("ExtractRootFlag(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}

// TestPreScanExtractorsStopAtSeparator pins the "--" guard on every pre-scan
// extractor that peeks argv before the main flag parse. Without it, a token meant
// verbatim for a forwarded tool (e.g. `go::go-test . -- -v`, where -v is the test
// binary's own flag) is misread as magus's own flag. The correct pattern already
// existed in this file (HasDetachFlag, bindGlobalsAfterSubcommand); these four had
// no test at all before this fix.
func TestPreScanExtractorsStopAtSeparator(t *testing.T) {
	t.Run("ExtractRootFlag", func(t *testing.T) {
		if got := ExtractRootFlag([]string{"run", "build", "--", "-root", "x"}); got != "" {
			t.Fatalf("ExtractRootFlag = %q, want empty", got)
		}
		if got := ExtractRootFlag([]string{"-root", "ws", "run", "build"}); got != "ws" {
			t.Fatalf("ExtractRootFlag = %q, want %q", got, "ws")
		}
	})

	t.Run("ExtractQuietFlag", func(t *testing.T) {
		if got := ExtractQuietFlag([]string{"run", "build", "--", "-q"}); got {
			t.Fatalf("ExtractQuietFlag = %v, want false", got)
		}
		if got := ExtractQuietFlag([]string{"-q", "run", "build"}); !got {
			t.Fatalf("ExtractQuietFlag = %v, want true", got)
		}
	})

	t.Run("ExtractSilentFlag", func(t *testing.T) {
		if got := ExtractSilentFlag([]string{"run", "build", "--", "-s"}); got {
			t.Fatalf("ExtractSilentFlag = %v, want false", got)
		}
		if got := ExtractSilentFlag([]string{"-s", "run", "build"}); !got {
			t.Fatalf("ExtractSilentFlag = %v, want true", got)
		}
	})

	t.Run("ExtractServerEnabledFlag", func(t *testing.T) {
		if val, set := ExtractServerEnabledFlag([]string{"run", "build", "--", "-server-enabled"}); val || set {
			t.Fatalf("ExtractServerEnabledFlag = (%v, %v), want (false, false)", val, set)
		}
		if val, set := ExtractServerEnabledFlag([]string{"-server-enabled", "run", "build"}); !val || !set {
			t.Fatalf("ExtractServerEnabledFlag = (%v, %v), want (true, true)", val, set)
		}
	})

	t.Run("ExtractVerbosityCount", func(t *testing.T) {
		if got := ExtractVerbosityCount([]string{"run", "build", "--", "-v", "-v"}); got != 0 {
			t.Fatalf("ExtractVerbosityCount = %d, want 0", got)
		}
		if got := ExtractVerbosityCount([]string{"-vv", "run", "build"}); got != 2 {
			t.Fatalf("ExtractVerbosityCount = %d, want 2", got)
		}
	})
}
