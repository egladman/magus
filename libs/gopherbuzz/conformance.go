package buzz

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// UpstreamCheckoutEnv names the variable that points UpstreamCheckout at a checkout of
// upstream Buzz (github.com/buzz-language/buzz).
const UpstreamCheckoutEnv = "GOPHERBUZZ_UPSTREAM_DIR"

// UpstreamCheckout returns the upstream Buzz checkout to measure this engine against:
// $GOPHERBUZZ_UPSTREAM_DIR when set, else ~/Repos/buzz. ok is false, never an error,
// when that directory holds no tests/behavior suite, so a caller on a machine without
// the checkout skips rather than fails.
func UpstreamCheckout() (dir string, ok bool) {
	if v := os.Getenv(UpstreamCheckoutEnv); v != "" {
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

// UpstreamCommit returns the commit UpstreamRef pins: the hex after its `-g`, as
// `git describe` writes it ("0.5.0-265-g294d8f9" pins "294d8f9"). ok is false when
// UpstreamRef carries no such suffix, so a checkout cannot be verified against it.
func UpstreamCommit() (sha string, ok bool) {
	return describedCommit(UpstreamRef)
}

func describedCommit(ref string) (string, bool) {
	i := strings.LastIndex(ref, "-g")
	if i < 0 {
		return "", false
	}
	sha := ref[i+len("-g"):]
	if sha == "" || strings.Trim(sha, "0123456789abcdef") != "" {
		return "", false
	}
	return sha, true
}

// ReadConformanceAllowlist reads an upstream conformance allowlist: one test file
// name per line, with blank lines and lines starting with # skipped. The set it
// returns is what the conformance suites hold the engine to in both directions.
func ReadConformanceAllowlist(path string) (map[string]bool, error) {
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
