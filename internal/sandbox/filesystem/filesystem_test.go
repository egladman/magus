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

// TestCheckHonoursThePerRuleGrants is the core allowlist table: a rule grants a
// mode only when that mode's bit is set, and a path outside every rule is denied
// whatever the bits say.
func TestCheckHonoursThePerRuleGrants(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	inside := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(inside, []byte("x"), 0o644))
	outside := filepath.Join(t.TempDir(), "other.txt")
	require.NoError(t, os.WriteFile(outside, []byte("x"), 0o644))

	for _, tc := range []struct {
		name              string
		read, write       bool
		wantRead, wantWri bool
	}{
		{"read-only rule", true, false, true, false},
		{"write-only rule", false, true, false, true},
		{"read+write rule", true, true, true, true},
		{"rule granting nothing", false, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rs := allowlist(t, dir, tc.read, tc.write, false)

			if tc.wantRead {
				assert.NoError(t, rs.CheckRead(inside))
			} else {
				assert.ErrorIs(t, rs.CheckRead(inside), ErrDenied)
			}
			if tc.wantWri {
				assert.NoError(t, rs.CheckWrite(inside))
			} else {
				assert.ErrorIs(t, rs.CheckWrite(inside), ErrDenied)
			}
			// A path under no rule at all is denied regardless of the grants.
			assert.ErrorIs(t, rs.CheckRead(outside), ErrDenied)
			assert.ErrorIs(t, rs.CheckWrite(outside), ErrDenied)
		})
	}
}

// TestSiblingSharingAPathPrefixIsNotUnderTheRule guards the containment check.
// "under" compares strings, so without the separator it appends, a rule on
// /ws/allowed would also grant /ws/allowed-evil - a sibling directory the policy
// never mentioned. This is the single most consequential line in the package.
func TestSiblingSharingAPathPrefixIsNotUnderTheRule(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	sibling := filepath.Join(root, "allowed-evil")
	require.NoError(t, os.MkdirAll(allowed, 0o755))
	require.NoError(t, os.MkdirAll(sibling, 0o755))

	rs := allowlist(t, allowed, true, true, false)

	assert.NoError(t, rs.CheckRead(filepath.Join(allowed, "ok.txt")))
	assert.ErrorIs(t, rs.CheckRead(filepath.Join(sibling, "escape.txt")), ErrDenied,
		"a sibling sharing the rule's path prefix must not inherit its grant")
	// The rule's own directory is "under" itself.
	assert.NoError(t, rs.CheckRead(allowed))
}

// TestCheckExecRequiresReadNotExec pins a contract that reads as a bug and is not
// one: checkAccess treats modeExec exactly like modeRead, so Rule.Exec is never
// consulted here. The execve grant is enforced by the kernel layer instead (see
// landlock_linux.go). Two mistakes this catches: "fixing" checkAccess to require
// r.Exec (which would deny every exec on a read-only-but-executable path the
// landlock layer allows), and reading CheckExec == nil as "exec is permitted".
func TestCheckExecRequiresReadNotExec(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "tool")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755))

	readOnly := allowlist(t, dir, true, false, false) // Read set, Exec NOT set
	assert.NoError(t, readOnly.CheckExec(bin), "exec check passes on Read alone")

	execOnly := allowlist(t, dir, false, false, true) // Exec set, Read NOT set
	assert.ErrorIs(t, execOnly.CheckExec(bin), ErrDenied,
		"Rule.Exec alone does not satisfy the path-shape check; it is the kernel layer's input")
}

// TestUnnormalizedRulePathMatchesNothing is why ResolveRulePath exists. A checked
// path is symlink-resolved before comparison, so a rule carrying the unresolved
// form compares against a different string and silently matches nothing - the
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
	assert.ErrorIs(t, raw.CheckRead(file), ErrDenied,
		"an unresolved rule path must not silently appear to work")
	assert.NoError(t, allowlist(t, dir, true, false, false).CheckRead(file),
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
	assert.ErrorIs(t, rs.CheckRead(escape), ErrDenied,
		"a path that traverses out of the allowlist is denied on its resolved form")
}

// TestWriteToANonExistentPathResolvesItsNearestRealAncestor covers the create
// case: a write target does not exist yet, so normalizePath walks up to the
// nearest ancestor that does, resolves that, and re-attaches the missing tail.
//
// The second case is the regression. Resolving only the IMMEDIATE parent left the
// whole path lexical whenever that parent was also missing - creating a file in a
// directory this run has yet to make - so any symlink above it went unresolved and
// could never match a resolved rule path. On macOS every temp dir is under such a
// symlink (/var -> /private/var), so nested creates were denied outright.
func TestWriteToANonExistentPathResolvesItsNearestRealAncestor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rs := allowlist(t, dir, true, true, false)

	assert.NoError(t, rs.CheckWrite(filepath.Join(dir, "not-created-yet.txt")),
		"one missing level resolves against its existing parent")
	assert.NoError(t, rs.CheckWrite(filepath.Join(dir, "missing-dir", "deep.txt")),
		"a missing parent walks further up rather than falling back to a lexical path")
	assert.NoError(t, rs.CheckWrite(filepath.Join(dir, "a", "b", "c", "deep.txt")),
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
	assert.NoError(t, rs.CheckWrite(filepath.Join(nested, "f.txt")),
		"the broader rule still grants write even though a narrower read-only rule matched first")
}

// TestEmptyInputsAreDenied: an empty rule path matches nothing (it would otherwise
// prefix-match every absolute path), and an empty checked path is rejected outright.
func TestEmptyInputsAreDenied(t *testing.T) {
	t.Parallel()
	assert.False(t, under("/anything", ""), "an empty rule path must match nothing")

	rs := Ruleset{Rules: []Rule{{Path: "", Read: true, Write: true}}}
	assert.ErrorIs(t, rs.CheckRead("/etc/passwd"), ErrDenied)

	full := Ruleset{Rules: []Rule{{Path: "/", Read: true}}}
	err := full.CheckRead("")
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
		// Whatever comes back must be absolute and clean.
		if len(result) == 0 || result[0] != '/' {
			t.Errorf("normalizePath(%q) = %q: not absolute", path, result)
		}
	})
}
