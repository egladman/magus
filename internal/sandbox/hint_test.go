package sandbox

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interactive"
)

func TestDenyHint(t *testing.T) {
	t.Parallel()

	got := denyHint("ro", "/usr/bin/curl")
	for _, want := range []string{
		"sandbox blocked access to /usr/bin/curl",
		"magus config set key=sandbox.allow.curl.path,value=/usr/bin/curl",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("denyHint(ro) missing %q in:\n%s", want, got)
		}
	}
	// ro must not emit a mode command (mode defaults to ro).
	if strings.Contains(got, "mode") {
		t.Errorf("denyHint(ro) should not set mode:\n%s", got)
	}

	w := denyHint("rw", "/data/out")
	if !strings.Contains(w, "sandbox.allow.out.path,value=/data/out") || !strings.Contains(w, "sandbox.allow.out.mode,value=rw") {
		t.Errorf("denyHint(rw) wrong:\n%s", w)
	}
}

// TestEmitDenyHint verifies the hint reaches stderr as a "hint:" line, and that
// the hints toggle silences it. Not parallel: it redirects os.Stderr and flips
// the process-wide hints switch.
func TestEmitDenyHint(t *testing.T) {
	capture := func() string {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		orig := os.Stderr
		os.Stderr = w
		EmitDenyHint("ro", "/usr/bin/curl")
		os.Stderr = orig
		_ = w.Close()
		out, _ := io.ReadAll(r)
		return string(out)
	}

	interactive.SetEnabled(true)
	defer interactive.SetEnabled(true)
	got := capture()
	for _, want := range []string{"hint:", "magus config set key=sandbox.allow.curl.path,value=/usr/bin/curl"} {
		if !strings.Contains(got, want) {
			t.Errorf("EmitDenyHint stderr missing %q:\n%s", want, got)
		}
	}

	interactive.SetEnabled(false)
	if got := capture(); got != "" {
		t.Errorf("EmitDenyHint should be silent when hints are disabled, got: %q", got)
	}
}
