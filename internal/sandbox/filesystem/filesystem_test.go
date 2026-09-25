package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// allowlist builds a Ruleset over dir with the given grants, normalizing the rule
// path the way a policy build must. See TestUnnormalizedRulePathMatchesNothing for
// what happens when a caller forgets.
func allowlist(t *testing.T, dir string, read, write, exec bool) Ruleset {
	t.Helper()
	return Ruleset{Rules: []Rule{{Path: ResolveRulePath(dir), Read: read, Write: write, Exec: exec}}}
}

// TestCheckHonoursThePerRuleGrants is the core allowlist table: a rule grants an
// access only when that access's flag is set, and a path outside every rule is
// denied whatever the flags say.
func TestCheckHonoursThePerRuleGrants(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	inside := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(inside, []byte("x"), 0o644))
	outside := filepath.Join(t.TempDir(), "other.txt")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0o644))

	for _, tc := range []struct {
		name              string
		read, write, exec bool
	}{
		{"read-only rule", true, false, false},
		{"write-only rule", false, true, false},
		{"exec-only rule", false, false, true},
		{"read+write rule", true, true, false},
		{"rule granting nothing", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rs := allowlist(t, dir, tc.read, tc.write, tc.exec)
			for access, want := range map[Access]bool{Read: tc.read, Write: tc.write, Exec: tc.exec} {
				if want {
					assert.NoError(t, rs.Check(inside, access), access.String())
				} else {
					assert.ErrorIs(t, rs.Check(inside, access), ErrDenied, access.String())
				}
				assert.ErrorIs(t, rs.Check(outside, access), ErrDenied, "a path under no rule is denied")
			}
		})
	}
}

// TestSiblingSharingAPathPrefixIsNotUnderTheRule guards the containment check.
// "under" compares strings, so without the separator it appends, a rule on
// /ws/allowed would also grant /ws/allowed-evil, a sibling directory the policy
// never mentioned. This is the single most consequential line in the package.
func TestSiblingSharingAPathPrefixIsNotUnderTheRule(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	sibling := filepath.Join(root, "allowed-evil")
	require.NoError(t, os.MkdirAll(allowed, 0o755))
	require.NoError(t, os.MkdirAll(sibling, 0o755))

	rs := allowlist(t, allowed, true, true, false)

	assert.NoError(t, rs.Check(filepath.Join(allowed, "ok.txt"), Read))
	assert.ErrorIs(t, rs.Check(filepath.Join(sibling, "escape.txt"), Read), ErrDenied,
		"a sibling sharing the rule's path prefix must not inherit its grant")
	assert.NoError(t, rs.Check(allowed, Read), "the rule's own directory is under itself")
}

// TestUnnormalizedRulePathMatchesNothing is why ResolveRulePath exists. A checked
// path is symlink-resolved before comparison, so a rule carrying the unresolved
// form compares against a different string and silently matches nothing: the
// policy looks configured and grants none of what it names. On macOS the temp dir
// sits under /var, itself a symlink to /private/var, which makes this reproducible.
func TestUnnormalizedRulePathMatchesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))

	resolved := ResolveRulePath(dir)
	if resolved == filepath.Clean(dir) {
		t.Skip("temp dir has no symlink in its path on this platform; nothing to prove")
	}

	raw := Ruleset{Rules: []Rule{{Path: filepath.Clean(dir), Read: true}}}
	assert.ErrorIs(t, raw.Check(file, Read), ErrDenied,
		"an unresolved rule path must not silently appear to work")
	assert.NoError(t, allowlist(t, dir, true, false, false).Check(file, Read),
		"the same rule normalized does grant it")
}

// TestTraversalIsNormalizedBeforeTheAllowlistCheck: the check runs on the resolved
// path, so ../ cannot be used to leave the allowlist and still match it.
func TestTraversalIsNormalizedBeforeTheAllowlistCheck(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	allowed := filepath.Join(root, "ws")
	secret := filepath.Join(root, "secret")
	require.NoError(t, os.MkdirAll(allowed, 0o755))
	require.NoError(t, os.MkdirAll(secret, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(secret, "key"), []byte("x"), 0o600))

	rs := allowlist(t, allowed, true, true, false)

	escape := filepath.Join(allowed, "..", "secret", "key")
	assert.ErrorIs(t, rs.Check(escape, Read), ErrDenied,
		"a path that traverses out of the allowlist is denied on its resolved form")
}

// A ".." after a symlink climbs out of the link's TARGET, as the kernel walks it.
// Cleaning first would read /ws/l/../x as /ws/x and admit a file outside the rule.
func TestDotDotAfterASymlinkClimbsTheTarget(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	home := filepath.Join(root, "home")
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".config"), 0o755))
	require.NoError(t, os.MkdirAll(ws, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, "x"), []byte("secret"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "x"), []byte("fine"), 0o600))
	require.NoError(t, os.Symlink(filepath.Join(home, ".config"), filepath.Join(ws, "l")))

	rs := allowlist(t, ws, true, true, false)
	path := ws + "/l/../x"
	assert.ErrorIs(t, rs.Check(path, Read), ErrDenied)
	got, err := normalizePath(path)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(ResolveRulePath(home), "x"), got)
}

// A dangling link inside the allowlist names a file outside it. Creating through the
// link creates the target, so the target is what gets checked.
func TestWriteThroughADanglingSymlinkChecksItsTarget(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	require.NoError(t, os.MkdirAll(ws, 0o755))
	outside := filepath.Join(root, "home", ".bashrc")
	require.NoError(t, os.Symlink(outside, filepath.Join(ws, "rc")))

	rs := allowlist(t, ws, true, true, false)
	assert.ErrorIs(t, rs.Check(filepath.Join(ws, "rc"), Write), ErrDenied)

	// A relative dangling link that stays inside is still fine.
	require.NoError(t, os.Symlink("sub/new.txt", filepath.Join(ws, "inside")))
	assert.NoError(t, rs.Check(filepath.Join(ws, "inside"), Write))
}

// A symlink loop is refused rather than walked forever.
func TestSymlinkLoopIsDenied(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.Symlink("b", filepath.Join(dir, "a")))
	require.NoError(t, os.Symlink("a", filepath.Join(dir, "b")))
	rs := allowlist(t, dir, true, true, true)
	assert.ErrorIs(t, rs.Check(filepath.Join(dir, "a"), Read), ErrDenied)
}

// TestWriteToANonExistentPathResolvesItsNearestRealAncestor covers the create
// case: a write target does not exist yet, so its existing prefix is resolved and
// the missing tail re-attached.
//
// Resolving only the IMMEDIATE parent once left the whole path lexical whenever
// that parent was also missing, so a symlink above it went unresolved and could
// never match a resolved rule path. On macOS every temp dir is under such a symlink
// (/var -> /private/var), so nested creates were denied outright.
func TestWriteToANonExistentPathResolvesItsNearestRealAncestor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rs := allowlist(t, dir, true, true, false)

	assert.NoError(t, rs.Check(filepath.Join(dir, "not-created-yet.txt"), Write),
		"one missing level resolves against its existing parent")
	assert.NoError(t, rs.Check(filepath.Join(dir, "missing-dir", "deep.txt"), Write),
		"a missing parent walks further up rather than falling back to a lexical path")
	assert.NoError(t, rs.Check(filepath.Join(dir, "a", "b", "c", "deep.txt"), Write),
		"and it keeps walking for arbitrarily deep missing tails")
}

// TestAnyMatchingRuleGrants: rules are alternatives, not a first-match-wins list,
// so a narrow read-only rule does not veto a broader one that grants write.
func TestAnyMatchingRuleGrants(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	require.NoError(t, os.MkdirAll(nested, 0o755))

	rs := Ruleset{Rules: []Rule{
		{Path: ResolveRulePath(nested), Read: true},            // narrow, read-only, listed first
		{Path: ResolveRulePath(root), Read: true, Write: true}, // broad, also grants write
	}}
	assert.NoError(t, rs.Check(filepath.Join(nested, "f.txt"), Write),
		"the broader rule still grants write even though a narrower read-only rule matched first")
}

// TestEmptyInputsAreDenied: an empty rule path matches nothing (it would otherwise
// prefix-match every absolute path), and an empty checked path is rejected outright.
func TestEmptyInputsAreDenied(t *testing.T) {
	t.Parallel()
	assert.False(t, Under("/anything", ""), "an empty rule path must match nothing")

	rs := Ruleset{Rules: []Rule{{Path: "", Read: true, Write: true}}}
	assert.ErrorIs(t, rs.Check("/etc/passwd", Read), ErrDenied)

	full := Ruleset{Rules: []Rule{{Path: "/", Read: true}}}
	err := full.Check("", Read)
	assert.ErrorIs(t, err, ErrDenied, "an empty checked path is denied, not treated as the cwd")
	assert.NotErrorIs(t, err, errors.ErrUnsupported)
}

// TestResolveRulePathFallsBackToLexicalClean keeps the policy builder total: a path
// normalizePath rejects still yields something comparable rather than an empty
// string, which would match nothing and silently drop the rule.
func TestResolveRulePathFallsBackToLexicalClean(t *testing.T) {
	t.Parallel()
	assert.Equal(t, ".", ResolveRulePath(""), "the empty path cleans to . rather than staying empty")
	assert.Equal(t, "/a/b", ResolveRulePath("/a/./b/"), "an existing-path miss still returns a clean form")
}

func TestAccessString(t *testing.T) {
	t.Parallel()
	assert.Equal(t, []string{"read", "write", "exec", "access(9)"},
		[]string{Read.String(), Write.String(), Exec.String(), Access(9).String()})
}

// FuzzNormalizePath exercises path-shape handling with adversarial inputs.
func FuzzNormalizePath(f *testing.F) {
	f.Add("/workspace/foo")
	f.Add("/workspace/../etc/passwd")
	f.Add("relative/path")
	f.Add("")
	f.Add("/workspace/foo\x00bar")
	f.Add("//workspace//foo")
	f.Fuzz(func(t *testing.T, path string) {
		result, err := normalizePath(path)
		if err != nil {
			return // empty or bad paths are rejected; that's fine
		}
		if !filepath.IsAbs(result) || filepath.Clean(result) != result {
			t.Errorf("normalizePath(%q) = %q: not absolute and clean", path, result)
		}
	})
}
