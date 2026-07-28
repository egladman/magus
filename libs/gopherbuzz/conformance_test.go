package buzz_test

// This file automates the "strict superset of upstream" claim in README.md and
// the UpstreamRef pin in version.go. Nothing previously ran the upstream
// behavior suite against gopherbuzz; measured reality (2026-07-28) is that only
// 13 of the 84 files pass. TestUpstreamConformance makes that number a checked,
// monotonic fact instead of an unverified claim: it fails if a passing file
// regresses, and it also fails if an un-listed file starts passing, so an
// improvement cannot land without updating testdata/upstream-behavior-allowlist.txt.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
)

const allowlistPath = "testdata/upstream-behavior-allowlist.txt"

// upstreamCheckoutDir resolves the upstream buzz-language/buzz checkout: the
// GOPHERBUZZ_UPSTREAM_DIR env var if set, else ~/Repos/buzz. It reports ok=false
// (never an error) so the caller can t.Skip cleanly on a machine without the
// checkout - this test must stay hermetic and green in that case.
func upstreamCheckoutDir() (dir string, ok bool) {
	if v := os.Getenv("GOPHERBUZZ_UPSTREAM_DIR"); v != "" {
		dir = v
	} else if home, err := os.UserHomeDir(); err == nil {
		dir = filepath.Join(home, "Repos", "buzz")
	} else {
		return "", false
	}
	if info, err := os.Stat(filepath.Join(dir, "tests", "behavior")); err != nil || !info.IsDir() {
		return "", false
	}
	return dir, true
}

// TestUpstreamConformance runs every upstream tests/behavior/*.buzz file
// in-process through gopherbuzz (Session + the bundled std modules, the same
// surface `magus buzz -t` installs) and checks the result against the checked-in
// allowlist in both directions.
func TestUpstreamConformance(t *testing.T) {
	dir, ok := upstreamCheckoutDir()
	if !ok {
		t.Skip("no upstream buzz checkout found: set GOPHERBUZZ_UPSTREAM_DIR or check one out to ~/Repos/buzz (github.com/buzz-language/buzz) to run this test")
	}

	skipIfUpstreamRefMismatch(t, dir)

	behaviorDir := filepath.Join(dir, "tests", "behavior")
	files, err := filepath.Glob(filepath.Join(behaviorDir, "*.buzz"))
	if err != nil {
		t.Fatalf("glob %s: %v", behaviorDir, err)
	}
	if len(files) == 0 {
		t.Fatalf("no .buzz files found in %s", behaviorDir)
	}
	sort.Strings(files)

	allowed, err := loadAllowlist(allowlistPath)
	if err != nil {
		t.Fatalf("load allowlist: %v", err)
	}

	seen := make(map[string]bool, len(files))
	var regressions, improvements []string
	for _, path := range files {
		name := filepath.Base(path)
		seen[name] = true
		pass, detail := runUpstreamBehaviorFile(t, path)
		switch {
		case allowed[name] && !pass:
			regressions = append(regressions, fmt.Sprintf("%s: %s", name, detail))
		case !allowed[name] && pass:
			improvements = append(improvements, name)
		}
	}

	// An allowlist entry naming a file the upstream checkout no longer has is
	// itself a regression signal (upstream renamed or removed a test we claimed
	// to pass) - fold it into the same failure so it isn't silently ignored.
	for name := range allowed {
		if !seen[name] {
			regressions = append(regressions, fmt.Sprintf("%s: allowlisted but not found in %s", name, behaviorDir))
		}
	}

	if len(regressions) > 0 {
		sort.Strings(regressions)
		t.Errorf("upstream conformance regressed for %d allowlisted file(s):\n%s", len(regressions), strings.Join(regressions, "\n"))
	}
	if len(improvements) > 0 {
		sort.Strings(improvements)
		t.Errorf("%d file(s) now pass that are not in %s - add them there to record the improvement:\n%s",
			len(improvements), allowlistPath, strings.Join(improvements, "\n"))
	}
}

// runUpstreamBehaviorFile executes one upstream behavior file's `test "..." {}`
// blocks and reports whether the file as a whole passes: it must exec without
// error and every non-skipped test block must run without error. A single
// failing or erroring block fails the whole file, matching what `magus buzz -t`
// reports (and what upstream's own runner does per source file).
func runUpstreamBehaviorFile(t *testing.T, path string) (pass bool, detail string) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			pass, detail = false, fmt.Sprintf("panic: %v", r)
		}
	}()

	src, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Sprintf("read: %v", err)
	}

	ctx := context.Background()
	// No WithEmbedded: upstream behavior tests are real Buzz programs and must
	// parse under gopherbuzz's default strict (upstream-parity) mode.
	sess := buzz.NewSession(ctx)
	defer func() { _ = sess.Close() }()
	// Upstream files use relative imports (e.g. "../utils/hello"); resolving them
	// requires the file's own directory as an include dir, matching how upstream's
	// toolchain and `magus buzz -t <file>` (which inherits CWD) resolve them.
	sess.SetIncludeDirs([]string{filepath.Dir(path)})
	buzzstd.Register(sess)

	if err := sess.Exec(ctx, string(src)); err != nil {
		return false, fmt.Sprintf("exec: %v", err)
	}

	tests := sess.Tests()
	if len(tests) == 0 {
		return false, "no test blocks found"
	}
	for _, tc := range tests {
		if _, err := sess.CallValue(ctx, tc.Fn, nil); err != nil {
			if _, skipped := buzzstd.SkipMessage(err); skipped {
				continue
			}
			return false, fmt.Sprintf("test %q: %v", tc.Name, err)
		}
	}
	return true, ""
}

// loadAllowlist reads the newline-separated, #-comment-tolerant allowlist file
// into a set of base filenames.
func loadAllowlist(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	out := make(map[string]bool)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out, scanner.Err()
}

// upstreamRefSHA is the `-g<sha>` suffix of buzz.UpstreamRef (a `git describe`
// string like "0.5.0-251-ged42f47"), the exact commit gopherbuzz was last
// validated against.
var upstreamRefSHA = regexp.MustCompile(`-g([0-9a-f]+)$`)

// warnIfUpstreamRefMismatch compares the upstream checkout's actual HEAD commit
// against the sha pinned in buzz.UpstreamRef, logging (never failing) a warning
// on mismatch: the conformance numbers below are only meaningful measured
// against that exact pinned commit, but a stale local checkout should not block
// this test from running - it just means the result is against a different (and
// possibly non-comparable) upstream state.
// skipIfUpstreamRefMismatch skips when the checkout is not at the pinned commit.
//
// It SKIPS rather than warns-and-continues because the allowlist is a set of
// filenames recorded at one specific upstream commit. Against any other commit the
// comparison is not merely noisy, it is meaningless in both directions: a newer
// checkout adds files the allowlist has never seen (reported as unrecorded
// improvements) and an older one removes files it lists (reported as regressions).
// Either way the failure says nothing about gopherbuzz. Failing on it would also
// make a plain `go test ./...` red for anyone who happens to keep a buzz checkout
// at ~/Repos/buzz, which is the fallback path - a spurious failure on unrelated
// work is worse than no measurement.
func skipIfUpstreamRefMismatch(t *testing.T, dir string) {
	t.Helper()
	m := upstreamRefSHA.FindStringSubmatch(buzz.UpstreamRef)
	if m == nil {
		t.Logf("warning: buzz.UpstreamRef %q has no parseable -g<sha> suffix; cannot verify checkout", buzz.UpstreamRef)
		return
	}
	pinned := m[1]

	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Logf("warning: could not determine HEAD commit of upstream checkout %s: %v", dir, err)
		return
	}
	head := strings.TrimSpace(string(out))
	if !strings.HasPrefix(head, pinned) {
		t.Skipf("upstream checkout %s is at %s, but gopherbuzz.UpstreamRef pins %s (%s); the allowlist is only meaningful against the pinned commit. Run `magus run conformance gopherbuzz`, or point GOPHERBUZZ_UPSTREAM_DIR at a checkout of %s",
			dir, head, buzz.UpstreamRef, pinned, pinned)
	}
}
