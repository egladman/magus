package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

func TestPolicyContextRoundTrip(t *testing.T) {
	p := &Policy{}
	assert.Same(t, p, PolicyFromContext(WithPolicy(context.Background(), p)))
	assert.Nil(t, PolicyFromContext(context.Background()), "no policy is sandbox off")
}

// A nil policy is the sandbox off: every check passes and every name is inherited.
func TestNilPolicyAllowsEverything(t *testing.T) {
	var p *Policy
	ctx := t.Context()
	assert.NoError(t, p.CheckRead(ctx, "/etc/shadow"))
	assert.NoError(t, p.CheckWrite(ctx, "/etc/passwd"))
	assert.NoError(t, p.CheckExec(ctx, "/tmp/payload"))
	assert.True(t, p.AllowsEnv("GITHUB_TOKEN"))
}

// Each check honours only its own flag: a read grant permits no exec, the gap that
// once let a host without landlock run anything it could read.
func TestPolicyChecksHonourTheirOwnFlag(t *testing.T) {
	dir := filesystem.ResolveRulePath(t.TempDir())
	p := &Policy{FS: filesystem.Ruleset{Rules: []filesystem.Rule{{Path: dir, Read: true}}}}
	ctx := t.Context()
	assert.NoError(t, p.CheckRead(ctx, filepath.Join(dir, "tool")))
	assert.ErrorIs(t, p.CheckWrite(ctx, filepath.Join(dir, "tool")), filesystem.ErrDenied)
	assert.ErrorIs(t, p.CheckExec(ctx, filepath.Join(dir, "tool")), filesystem.ErrDenied)
}

// A symlink inside the workspace pointing out of it is checked where it points.
func TestSymlinkEscapeRejected(t *testing.T) {
	ws := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0o600))
	link := filepath.Join(ws, "evil")
	require.NoError(t, os.Symlink(outside, link))
	p := BuildPolicy(PolicyOptions{Workspace: ws})
	assert.ErrorIs(t, p.CheckRead(t.Context(), link), filesystem.ErrDenied)
}

func TestAllowsEnv(t *testing.T) {
	p := BuildPolicy(PolicyOptions{Env: env.Allowlist{Names: []string{"GOPATH"}, Prefixes: []string{"NPM_CONFIG_*"}}})
	for name, want := range map[string]bool{
		"PATH": true, "HOME": true, "GOPATH": true, "NPM_CONFIG_CACHE": true, "MAGUS_RUN_ID": true,
		"GITHUB_TOKEN": false, "AWS_ACCESS_KEY_ID": false, "NPM_TOKEN": false,
		"MAGUS_PROC_SOCKET": false, "MAGUS_SERVER_ADDRESS": false,
	} {
		assert.Equal(t, want, p.AllowsEnv(name), name)
	}
}

// TestApplyWithoutLandlockReportsUnsupported: Supported() false must mean Apply
// reports ErrUnsupported, never a success and never a different error.
func TestApplyWithoutLandlockReportsUnsupported(t *testing.T) {
	if runtime.GOOS == "linux" && Supported() {
		t.Skip("Apply would restrict this test process")
	}
	assert.ErrorIs(t, Apply(BuildPolicy(PolicyOptions{Workspace: t.TempDir()})), ErrUnsupported)
	assert.NoError(t, Apply(nil), "Apply(nil) is a no-op")
}

func TestUnionPoliciesMergesRules(t *testing.T) {
	a := &Policy{
		FS: filesystem.Ruleset{Rules: []filesystem.Rule{
			{Path: "/shared", Read: true},
			{Path: "/only-in-a", Read: true, Write: true},
		}},
		Env: env.Allowlist{Names: []string{"PATH", "HOME"}},
	}
	b := &Policy{
		FS: filesystem.Ruleset{Rules: []filesystem.Rule{
			{Path: "/shared", Write: true, Exec: true},
			{Path: "/only-in-b", Read: true},
		}},
		Env: env.Allowlist{Names: []string{"PATH", "GOPATH"}, Prefixes: []string{"MISE_*"}},
	}
	u := UnionPolicies(nil, a, b)
	assert.Equal(t, []filesystem.Rule{
		{Path: "/shared", Read: true, Write: true, Exec: true},
		{Path: "/only-in-a", Read: true, Write: true},
		{Path: "/only-in-b", Read: true},
	}, u.FS.Rules)
	assert.Equal(t, env.Allowlist{Names: []string{"PATH", "HOME", "GOPATH"}, Prefixes: []string{"MISE_*"}}, u.Env)
	assert.NotNil(t, UnionPolicies(), "the union of nothing is an empty policy")
}

func TestUnionDeduplicatesBaseEnv(t *testing.T) {
	a := &Policy{BaseEnv: []string{"PATH=/usr/bin", "HOME=/home/u"}}
	b := &Policy{BaseEnv: []string{"PATH=/usr/bin", "GOPATH=/home/u/go"}}
	assert.Equal(t, []string{"PATH=/usr/bin", "HOME=/home/u", "GOPATH=/home/u/go"}, UnionPolicies(a, b).BaseEnv)
}

func TestUnionOfOnePolicyIsThatPolicy(t *testing.T) {
	p := BuildPolicy(PolicyOptions{Workspace: t.TempDir()})
	assert.Equal(t, p.Fingerprint(), UnionPolicies(p).Fingerprint())
}

// noopMetrics satisfies MetricsRecorder without doing work. The benchmark stamps one
// because recordCheck returns early without a recorder, and a real run always has one.
type noopMetrics struct{}

func (noopMetrics) RecordSandboxCheck(context.Context, string, string, string) {}
func (noopMetrics) RecordSandboxEnvDropped(context.Context, string, int64)     {}

// benchPolicy returns a policy shaped like a real run's, plus the workspace root its last
// rule guards. Rule paths go through ResolveRulePath: an unresolved rule matches nothing,
// and on macOS (TMPDIR under the /var symlink) the benchmark would measure the deny path.
// None of the fixed rules may be an ancestor of the temp root, or which rule matches
// becomes a function of TMPDIR.
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

// benchPaths returns n allowed paths under root in the shape fs.glob hands to CheckRead
// one match at a time, creating them on disk when create is set.
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
	require.NoError(b, p.CheckRead(context.Background(), paths[0]), "benchmark paths must be inside the allowlist")
	return paths
}

// BenchmarkCheckRead is the per-call cost of the binding-layer read check. fs.glob calls
// it once per match, so anything added here is multiplied by the match count. The cost is
// dominated by the lstat of each path component, so it scales with TMPDIR's depth; a
// missing path stops lstat-ing at its first missing component.
//
// Each sub-benchmark builds its own root: sharing one would make the missing case's files
// exist.
func BenchmarkCheckRead(b *testing.B) {
	for _, tc := range []struct {
		name   string
		create bool
	}{{"existing", true}, {"missing", false}} {
		b.Run(tc.name, func(b *testing.B) {
			p, root := benchPolicy(b)
			paths := benchPaths(b, p, root, 1024, tc.create)
			ctx := WithMetrics(WithPolicy(context.Background(), p), noopMetrics{})
			b.ReportAllocs()
			for i := 0; b.Loop(); i++ {
				_ = p.CheckRead(ctx, paths[i%len(paths)])
			}
		})
	}
}

// A denial lands on the run's trail, naming the access and the path: the console's
// sandbox-denial bell has no other producer.
func TestDenialLandsOnTheTrail(t *testing.T) {
	base := t.TempDir()
	allowed := filesystem.ResolveRulePath(t.TempDir())
	policy := &Policy{FS: filesystem.Ruleset{Rules: []filesystem.Rule{{Path: allowed, Read: true}}}}
	ctx := trail.ContextWithBase(t.Context(), base)

	require.Error(t, policy.CheckWrite(ctx, "/definitely/not/allowed/f"))
	require.NoError(t, policy.CheckRead(ctx, filepath.Join(allowed, "f")), "an allow leaves no event")

	events, err := trail.ReadRecent(base, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, trail.KindSandboxDenial, events[0].Kind)
	assert.Equal(t, "write of /definitely/not/allowed/f", events[0].Action)
	assert.Equal(t, trail.OutcomeError, events[0].Outcome)
	assert.NotEmpty(t, events[0].Error)
	assert.Empty(t, events[0].Lease, "a workspace-default denial claims no lease")
}

// A lease-narrowed policy names its lease on a denial, so a reader can say whose
// boundary was hit and whether it was bound or only claimed.
func TestDenialNamesTheLeaseThatNarrowedThePolicy(t *testing.T) {
	base := t.TempDir()
	policy := &Policy{Lease: "fleet/worker-3", LeaseFrom: types.LeaseSourceMarker}
	ctx := trail.ContextWithBase(t.Context(), base)

	require.Error(t, policy.CheckExec(ctx, "/definitely/not/allowed/f"))

	events, err := trail.ReadRecent(base, 10)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "fleet/worker-3", events[0].Lease)
	assert.Equal(t, types.LeaseSourceMarker, events[0].LeaseFrom)
	assert.Equal(t, "exec of /definitely/not/allowed/f", events[0].Action)
}
