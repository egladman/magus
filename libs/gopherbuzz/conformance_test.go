package buzz_test

// This file automates the "strict superset of upstream" claim in README.md and
// the UpstreamRef pin in version.go. Nothing previously ran the upstream
// behavior suite against gopherbuzz. This comment used to name a score, and the
// score it named was wrong twice over: "13 of 84" came from a hand-count against
// an UNPINNED upstream checkout, which is both the wrong file count (83 at the
// pin) and the wrong result - README.md calls out that exact mistake. The count
// belongs in one place only, testdata/upstream-behavior-allowlist.txt, which this
// test enforces. TestUpstreamConformance makes that number a checked,
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

	// Upstream runs this suite from its own repo root, and several files depend on
	// that: fs.buzz stats README.md, run-file.buzz runs tests/utils/testing.buzz.
	// Reading them from elsewhere failed on the working directory rather than on
	// anything about the language. Done after loadAllowlist, which resolves
	// allowlistPath relative to THIS package's directory.
	t.Chdir(dir)

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

// ── The other upstream suites ────────────────────────────────────────────────
//
// tests/behavior above is one of six directories upstream ships. Measuring only it
// overstates parity, because it asks a single question: does correct source produce
// the right answer? Two more are measurable here and are measured below, so the
// README can state a number instead of an impression:
//
//   compile_errors/ — 77 programs upstream REJECTS. gopherbuzz accepting one is a
//                     soundness gap: malformed source that compiles clean.
//   fuzzed/         — 644 malformed inputs that must not crash the front end.
//
// The remaining three are deliberately not run: bench/ is upstream's benchmarks (see
// benchmarks/ for gopherbuzz's own), manual/ is interactive, and utils/ holds helper
// modules the behavior tests import rather than tests of its own.

const compileErrorsAllowlistPath = "testdata/upstream-compile-errors-allowlist.txt"

// TestUpstreamCompileErrors checks, against the same monotonic-allowlist contract as
// TestUpstreamConformance, which programs upstream rejects that gopherbuzz also
// rejects. Upstream's runner takes the file's first line as the expected message and
// requires compilation to fail; this asserts the weaker, still meaningful property
// that it fails at all, since the two implementations word diagnostics differently.
//
// The allowlist may only GROW: a listed file that starts compiling clean is a
// regression (gopherbuzz got laxer), and an unlisted file that starts failing is
// progress that has to be recorded.
func TestUpstreamCompileErrors(t *testing.T) {
	dir, ok := upstreamCheckoutDir()
	if !ok {
		t.Skip("no upstream buzz checkout found: set GOPHERBUZZ_UPSTREAM_DIR or check one out to ~/Repos/buzz (github.com/buzz-language/buzz) to run this test")
	}
	skipIfUpstreamRefMismatch(t, dir)

	errDir := filepath.Join(dir, "tests", "compile_errors")
	files, err := filepath.Glob(filepath.Join(errDir, "*.buzz"))
	if err != nil {
		t.Fatalf("glob %s: %v", errDir, err)
	}
	if len(files) == 0 {
		t.Fatalf("no .buzz files found in %s", errDir)
	}
	sort.Strings(files)

	allowed, err := loadAllowlist(compileErrorsAllowlistPath)
	if err != nil {
		t.Fatalf("load allowlist: %v", err)
	}
	t.Chdir(dir)

	seen := make(map[string]bool, len(files))
	var regressions, improvements []string
	for _, path := range files {
		name := filepath.Base(path)
		seen[name] = true
		rejected := upstreamRejects(t, path, dir)
		switch {
		case allowed[name] && !rejected:
			regressions = append(regressions, name+": now compiles clean; it must still be rejected")
		case !allowed[name] && rejected:
			improvements = append(improvements, name)
		}
	}
	for name := range allowed {
		if !seen[name] {
			regressions = append(regressions, name+": allowlisted but not found in "+errDir)
		}
	}
	if len(regressions) > 0 {
		sort.Strings(regressions)
		t.Errorf("compile-error parity regressed for %d file(s):\n%s", len(regressions), strings.Join(regressions, "\n"))
	}
	if len(improvements) > 0 {
		sort.Strings(improvements)
		t.Errorf("%d file(s) are now correctly rejected that are not in %s - add them there to record the improvement:\n%s",
			len(improvements), compileErrorsAllowlistPath, strings.Join(improvements, "\n"))
	}
}

// upstreamRejects reports whether compiling path fails. A panic counts as rejection
// for the allowlist's purpose but is reported separately by the fuzz test, which is
// where "must not crash" belongs.
func upstreamRejects(t *testing.T, path, root string) (rejected bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			rejected = true
		}
	}()
	src, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	ctx := context.Background()
	sess := buzz.NewSession(ctx)
	defer func() { _ = sess.Close() }()
	// Several of these import a helper from tests/utils, so both directories have to
	// resolve or the failure would be the import rather than the error under test.
	sess.SetIncludeDirs([]string{filepath.Dir(path), filepath.Join(root, "tests", "utils")})
	buzzstd.Register(sess)
	return sess.Exec(ctx, string(src)) != nil
}

// TestUpstreamFuzzCorpusDoesNotPanic runs upstream's fuzz corpus through the front end
// and requires that none of it panics. A compile error is the expected outcome for
// malformed input; a panic is a crash, and in the unsafe build a panic is the polite
// version of what a bad assumption does.
//
// The corpus is upstream's checked-in AFL output -- real programs with a byte corrupted,
// named by the mutation that produced them. SCOPE: this covers parse, check and compile
// only, deliberately not execution. Executing arbitrary fuzzed source invites a
// non-terminating loop, and the front end is where malformed input is supposed to be
// caught. The README states the same limit rather than implying the corpus is run.
func TestUpstreamFuzzCorpusDoesNotPanic(t *testing.T) {
	dir, ok := upstreamCheckoutDir()
	if !ok {
		t.Skip("no upstream buzz checkout found: set GOPHERBUZZ_UPSTREAM_DIR or check one out to ~/Repos/buzz (github.com/buzz-language/buzz) to run this test")
	}
	skipIfUpstreamRefMismatch(t, dir)

	files, err := filepath.Glob(filepath.Join(dir, "tests", "fuzzed", "*.buzz"))
	if err != nil {
		t.Fatalf("glob fuzzed: %v", err)
	}
	if len(files) == 0 {
		t.Fatalf("no .buzz files found in tests/fuzzed")
	}
	sort.Strings(files)

	var panicked []string
	for _, path := range files {
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicked = append(panicked, fmt.Sprintf("%s: %v", filepath.Base(path), r))
				}
			}()
			src, readErr := os.ReadFile(path)
			if readErr != nil {
				return
			}
			ctx := context.Background()
			sess := buzz.NewSession(ctx)
			defer func() { _ = sess.Close() }()
			_, _ = sess.Compile(string(src)) // an error is fine; a panic is not
		}()
	}
	if len(panicked) > 0 {
		sort.Strings(panicked)
		t.Errorf("the front end panicked on %d of %d fuzz inputs:\n%s",
			len(panicked), len(files), strings.Join(panicked, "\n"))
	}
}
