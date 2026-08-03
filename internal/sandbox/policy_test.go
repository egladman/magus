package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPolicyContext_RoundTrip(t *testing.T) {
	p := &Policy{}
	ctx := WithPolicy(context.Background(), p)
	got := FromContext(ctx)
	assert.Same(t, p, got, "FromContext should return the stored Policy")
}

func TestFromContext_Empty(t *testing.T) {
	assert.Nil(t, FromContext(context.Background()), "FromContext(empty) should return nil")
}

func TestPolicy_CheckRead_NilPolicy(t *testing.T) {
	var p *Policy
	// A nil policy (no restrictions) should allow all reads.
	assert.NoError(t, p.CheckRead("/any/path"), "nil Policy.CheckRead should allow all reads")
}

func TestUnionPolicies_NilSafe(t *testing.T) {
	p := UnionPolicies()
	assert.NotNil(t, p, "UnionPolicies() of zero policies should not return nil")
}

// noopMetrics satisfies MetricsRecorder without doing work. The benchmarks stamp one
// because RecordCheck returns immediately when ctx carries no recorder (metrics.go), and
// that early return IS the whole difference between CheckRead and CheckReadCtx. Without a
// recorder a benchmark named CheckReadCtx measures the one thing its name promises to
// include. A real run always stamps one (internal/sandbox/apply).
type noopMetrics struct{}

func (noopMetrics) RecordSandboxCheck(context.Context, string, string, string) {}
func (noopMetrics) RecordSandboxEnvDropped(context.Context, string, int64)     {}

// benchPolicy returns a policy shaped like a real run's, plus the workspace root its last
// rule guards. The root is returned rather than recovered from the rules, because reaching
// back through Rules[len-1] made the fixture's two halves silently interdependent: append a
// rule and the path builders start writing somewhere else entirely.
//
// Rule paths go through ResolveRulePath. checkAccess symlink-resolves the path it is handed
// and compares it against the rule string as written, so an unresolved rule matches nothing
// and every call falls through to ErrDenied plus its fmt.Errorf. On macOS TempDir sits under
// /var, itself a symlink to /private/var, so omitting it benchmarks the DENY path here while
// benchmarking the allow path on Linux - the same benchmark name reporting two different
// algorithms by OS. See TestUnnormalizedRulePathMatchesNothing.
//
// None of the fixed rules may be an ancestor of the temp root, or which rule matches (and at
// what depth in the scan) becomes a function of TMPDIR. That is why there is no /private/tmp
// entry here despite it being realistic.
func benchPolicy(b *testing.B) (*Policy, string) {
	b.Helper()
	root := filesystem.ResolveRulePath(b.TempDir())
	return &Policy{FS: filesystem.Ruleset{Rules: []filesystem.Rule{
		{Path: "/usr/lib", Read: true},
		{Path: "/usr/share", Read: true},
		{Path: "/opt/homebrew", Read: true, Exec: true},
		{Path: root, Read: true, Write: true},
	}}}, root
}

// benchCtx carries the policy and a recorder, matching what a real binding-layer call sees.
func benchCtx(p *Policy) context.Context {
	return WithMetrics(WithPolicy(context.Background(), p), noopMetrics{})
}

// benchPaths returns n absolute paths under root, in the shape fs.glob hands to
// CheckReadCtx one match at a time, creating them on disk when create is set.
//
// Unexported behind the two named wrappers below rather than called directly: the flag
// selects between two different algorithms inside normalizePath, not a shade of the same
// one, and a bare true/false at the call site does not say which is being measured.
func benchPaths(b *testing.B, p *Policy, root string, n int, create bool) []string {
	b.Helper()
	dir := filepath.Join(root, "internal", "pkg")
	if create {
		require.NoError(b, os.MkdirAll(dir, 0o755))
	}
	paths := make([]string, n)
	for i := range paths {
		paths[i] = filepath.Join(dir, fmt.Sprintf("file%d.go", i))
		if create {
			require.NoError(b, os.WriteFile(paths[i], []byte("package pkg\n"), 0o644))
		}
	}
	// A benchmark that measures the deny path reports a number unrelated to its own name and
	// says nothing while doing it. Both branches here must be ALLOWED - absence selects
	// normalizePath's ancestor walk, not a denial - so assert it instead of trusting the
	// ruleset and the path builder to keep agreeing.
	require.NoError(b, p.CheckRead(paths[0]), "benchmark paths must be inside the allowlist")
	return paths
}

// benchExistingPaths is the fs.glob shape: matches came from a directory listing, so they
// exist and normalizePath resolves them on its first EvalSymlinks.
func benchExistingPaths(b *testing.B, p *Policy, root string, n int) []string {
	b.Helper()
	return benchPaths(b, p, root, n, true)
}

// benchMissingPaths is the not-yet-created shape: nothing on disk, so normalizePath's first
// EvalSymlinks fails and it walks up the tree resolving each ancestor in turn.
//
// Measured through CheckReadCtx even though a missing path is usually a write target: the
// branch lives in normalizePath, which read and write checks share, so going through the
// read entry point isolates the resolve cost instead of also swapping the access mode.
func benchMissingPaths(b *testing.B, p *Policy, root string, n int) []string {
	b.Helper()
	return benchPaths(b, p, root, n, false)
}

// BenchmarkCheckReadCtx is the per-call cost of the binding-layer read check, split by which
// normalizePath branch the path takes. fs.glob calls this once per match (std/fs.go), so a
// large glob pays the "existing" number per file and anything added here is multiplied by
// the match count - which is what makes it the baseline for read attribution.
//
// Sub-benchmarks rather than two flat names: benchstat compares a shared prefix, and the
// existing/missing pair is the comparison these exist to support.
//
// Both numbers are dominated by EvalSymlinks, which lstats every path component, so they
// scale with the depth of TMPDIR rather than with anything in checkAccess. Read them as the
// cost of the check as callers actually pay it, not as a measurement of the rule scan.
//
// Each sub-benchmark builds its OWN policy and root. Sharing one root silently destroys the
// distinction being measured: the existing case creates file0..fileN, the missing case then
// asks for the same names under the same directory, and they are no longer missing. The two
// reported identical numbers when this was shared, which is the only symptom it produces.
func BenchmarkCheckReadCtx(b *testing.B) {
	b.Run("existing", func(b *testing.B) {
		p, root := benchPolicy(b)
		paths := benchExistingPaths(b, p, root, 1024)
		ctx := benchCtx(p)
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			_ = p.CheckReadCtx(ctx, paths[i%len(paths)])
		}
	})

	b.Run("missing", func(b *testing.B) {
		p, root := benchPolicy(b)
		paths := benchMissingPaths(b, p, root, 1024)
		ctx := benchCtx(p)
		b.ReportAllocs()
		for i := 0; b.Loop(); i++ {
			_ = p.CheckReadCtx(ctx, paths[i%len(paths)])
		}
	})
}
