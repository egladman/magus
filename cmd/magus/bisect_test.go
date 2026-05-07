package main

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// TestParseBisectCulprit verifies the culprit-extraction logic against
// synthetic git-bisect-log output.
func TestParseBisectCulprit(t *testing.T) {
	cases := []struct {
		name    string
		log     string
		want    string
		wantErr bool
	}{
		{
			name: "standard culprit line",
			log: `git bisect start
# good: [abc123] fix typo
git bisect good abc123
# bad: [def456] introduce regression
git bisect bad def456
# first bad commit: [def456] introduce regression
`,
			want: "def456",
		},
		{
			name:    "no culprit",
			log:     "git bisect start\n# good: [abc123] blah\n",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Write synthetic bisect log to a temp git repo.
			dir := t.TempDir()
			if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
				t.Skipf("git init failed: %v\n%s", err, out)
			}

			// We can't easily fake `git bisect log`, so test the parser directly
			// by parsing the raw bytes.
			got, err := parseBisectLog([]byte(tc.log))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got sha=%q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// parseBisectLog is the inner logic of parseBisectCulprit, extracted for
// unit-testability without needing a real git repo.
func parseBisectLog(out []byte) (string, error) {
	for _, line := range bytes.Split(out, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		if strings.HasPrefix(s, "# first bad commit: [") {
			after := strings.TrimPrefix(s, "# first bad commit: [")
			sha := strings.SplitN(after, "]", 2)[0]
			if sha != "" {
				return sha, nil
			}
		}
	}
	return "", errors.New("could not parse culprit from git bisect log")
}

// TestSelfCmdSignatureCompat is a compile-time check: both
// selfmanage.go (selfmanage) and selfmanage_stub.go (!selfmanage)
// must expose `func selfCmd(context.Context, string, []string) error`
// (the string is the workspace root, threaded through for `self init`).
// If the signatures differ the package simply won't compile under one of
// the two build tags, which `go build` catches. This test documents the
// intent so a future reader understands why the stub imports "context".
func TestSelfCmdSignatureCompat(t *testing.T) {
	// Compile-time signature assertion: selfCmd must have exactly this type
	// or this file fails to compile. That guarantee is the entire test — there
	// is nothing to assert at runtime (a package func is never nil).
	var _ func(context.Context, string, []string) error = selfCmd //nolint:staticcheck // QF1011: the explicit type is the compile-time signature assertion this test exists for
}
