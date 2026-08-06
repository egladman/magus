package std

import (
	"strings"
	"testing"
)

// TestProbeBinsNameRealReplacements pins the advisory table rather than the warning
// itself: the warning is a sync.Once on stderr, so a test that captured it would pass or
// fail depending on whether an earlier test in this package had already tripped it. What
// is worth pinning is that each entry names a REAL alternative - an entry pointing back at
// another shell-out is advice that sends the reader in a circle, and nothing else catches
// that.
func TestProbeBinsNameRealReplacements(t *testing.T) {
	if _, ok := probeBins["test"]; !ok {
		t.Fatal("test(1) must be listed: it is the shell-out that cost the docs site ~113k forks a build")
	}
	for bin, instead := range probeBins {
		if bin == "" || instead == "" {
			t.Errorf("probeBins[%q] = %q: both sides must be non-empty", bin, instead)
		}
		if strings.Contains(instead, "os.execute") {
			t.Errorf("probeBins[%q] suggests %q, which is the thing being warned about", bin, instead)
		}
	}
}
