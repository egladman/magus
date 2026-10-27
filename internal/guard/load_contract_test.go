package guard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadContractNamesTheTriggerPaths(t *testing.T) {
	for _, p := range []string{
		"internal/spell/gen/decls/magus.buzz",
		"internal/interp/bindings/gen/boundary_objects.go",
		"std/gen/std-api.lock",
		"internal/commands/testdata/api-go.lock",
		"libs/gopherbuzz/checker_members.go",
		"libs/gopherbuzz/compiler.go",
	} {
		assert.True(t, loadContract(p), p)
	}
	for _, p := range []string{
		"run.go",
		"libs/gopherbuzz/checker_test.go",
		"libs/gopherbuzz/vm.go",
		"internal/spell/gen/types/quote.buzz",
		"docs/index.md",
	} {
		assert.False(t, loadContract(p), p)
	}
}

// contractCheckout is a checkout of magus with a base commit, then one commit changing the
// host declarations and one changing a doc, returning the three commits.
func contractCheckout(t *testing.T) (root string, base, decls, doc string) {
	t.Helper()
	root = t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root, "-c", "user.email=t@example.com", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...).CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	git("init", "-q", "-b", "main")
	write("go.mod", fmt.Sprintf("module %s\n\ngo 1.26\n", ownModule))
	write("magus.yaml", "{}\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	base = git("rev-parse", "HEAD")
	write("internal/spell/gen/decls/magus.buzz", "export fun x() > void {}\n")
	git("add", "-A")
	git("commit", "-q", "-m", "decls")
	decls = git("rev-parse", "HEAD")
	write("docs/a.md", "a\n")
	git("add", "-A")
	git("commit", "-q", "-m", "doc")
	doc = git("rev-parse", "HEAD")
	git("reset", "-q", "--hard", base)
	return root, base, decls, doc
}

// A pick that changes the load contract is refused while the tree loads, served the dry
// run instead; the rest of a pick and an ordinary one run.
func TestContractPickIsRefusedOnlyForTheLoadContract(t *testing.T) {
	root, base, decls, doc := contractCheckout(t)
	at := location{workspace: root, dir: root}
	ctx := context.Background()
	deny := func(line string) string { return denyContractPick(ctx, Dependencies{}, at, line, DialectBash) }

	assert.Contains(t, deny("git cherry-pick "+decls), "changes the load contract (internal/spell/gen/decls/magus.buzz)")
	assert.Contains(t, deny(fmt.Sprintf("git cherry-pick -x %s..%s", base, doc)), "load contract")
	assert.Contains(t, deny("git merge "+decls), "load contract")
	assert.Empty(t, deny("git cherry-pick "+doc), "an ordinary pick")
	assert.Empty(t, deny("git cherry-pick --continue"))
	assert.Empty(t, deny("git merge --ff-only "+decls), "a fast-forward lands a tree something already built")
	assert.Empty(t, denyContractPick(ctx, Dependencies{LoadFailure: assert.AnError}, at, "git cherry-pick "+decls, DialectBash),
		"while the tree does not load, finishing the pick is the way out")
}

// A ./magus built before HEAD's load-contract change refuses a state-changing line, once
// per HEAD from the cache, and lets reads and the rebuild through. A dirty build may hold
// the change already, so it is not refused.
func TestContractSkewRefusesChangesUntilTheRebuild(t *testing.T) {
	root, base, decls, _ := contractCheckout(t)
	out, err := exec.Command("git", "-C", root, "reset", "-q", "--hard", decls).CombinedOutput()
	require.NoError(t, err, string(out))
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus"), []byte("#!/bin/sh\n"), 0o755))
	at := location{workspace: root, dir: root, cacheDir: t.TempDir()}
	asked := 0
	deps := Dependencies{Binary: filepath.Join(root, "magus"), BinaryCommit: base, Revision: func(context.Context, string, string) string {
		asked++
		return decls
	}}
	ctx := context.Background()

	deny := denyContractSkew(ctx, deps, at, "touch notes.txt", DialectBash)
	assert.Contains(t, deny, fmt.Sprintf("./magus was built at %s, before internal/spell/gen/decls/magus.buzz changed the load contract; rebuild ./magus with `./magus run go-build .` first.", base[:12]))
	assert.Empty(t, denyContractSkew(ctx, deps, at, "git status", DialectBash))
	assert.Empty(t, denyContractSkew(ctx, deps, at, "./magus run go-build .", DialectBash))

	skew, ok := checkoutSkew(ctx, deps, at)
	require.True(t, ok)
	assert.Equal(t, []string{"internal/spell/gen/decls/magus.buzz"}, skew.Contract)
	kept, err := os.ReadFile(filepath.Join(at.cacheDir, binarySkewFile))
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(kept), "\n"), "computed once for this HEAD and binary")

	deps.BinaryDirty = true
	assert.Empty(t, denyContractSkew(ctx, deps, location{workspace: root, dir: root, cacheDir: t.TempDir()}, "touch notes.txt", DialectBash))
	deps.BinaryDirty, deps.Binary = false, "/usr/local/bin/magus"
	assert.Empty(t, denyContractSkew(ctx, deps, at, "touch notes.txt", DialectBash), "only the checkout's own ./magus is compared")
}
