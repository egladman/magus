package spell

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// installSpell is a spell declaring one pnpm install, the shape the typescript spell
// ships, with overrides applied to the install record.
func installSpell(override func(in map[string]any)) mapObj {
	in := map[string]any{
		"name":        "pnpm-install",
		"command":     map[string]any{"bin": "pnpm", "args": []string{"install", "--frozen-lockfile"}},
		"dir":         "node_modules",
		"relocatable": true,
		"stamps":      []string{"node_modules/.pnpm/lock.yaml"},
		"inputs":      []string{".npmrc"},
		"tools":       []string{"pnpm"},
	}
	if override != nil {
		override(in)
	}
	return mapObj{
		"name":  "ts",
		"tools": map[string]any{"pnpm": map[string]any{"probe": map[string]any{"bin": "pnpm", "args": []string{"--version"}}}},
		"manifests": []any{map[string]any{
			"value":          "package.json",
			"lockCandidates": []string{"pnpm-lock.yaml", "package-lock.json"},
			"installs":       map[string]any{"pnpm-lock.yaml": in},
		}},
	}
}

func TestDecode_InstallSynthesizesTheOp(t *testing.T) {
	m, err := Decode(installSpell(nil))
	require.NoError(t, err)

	op, ok := m.Ops["pnpm-install"]
	require.True(t, ok, "a declared install must reach the op table")
	want := spells.Install{
		Name:        "pnpm-install",
		Command:     spells.Command{Bin: "pnpm", Args: []string{"install", "--frozen-lockfile"}},
		Dir:         "node_modules",
		Relocatable: true,
		Stamps:      []string{"node_modules/.pnpm/lock.yaml"},
		Inputs:      []string{".npmrc"},
		Tools:       []string{"pnpm"},
	}
	assert.Equal(t, spells.Op{
		Kind:    spells.OpKindInstall,
		Command: want.Command,
		Install: &spells.InstallSpec{Spell: "ts", Manifests: []spells.Manifest{{
			Value:          "package.json",
			LockCandidates: []string{"pnpm-lock.yaml"},
			Installs:       map[string]spells.Install{"pnpm-lock.yaml": want},
		}}},
	}, op)
}

// Each rule is a declaration bug knowable at load, so each fails the spell rather than
// one project's install later.
func TestDecode_InstallMisdeclarationsAreErrors(t *testing.T) {
	cases := []struct {
		name string
		src  mapObj
		want string
	}{
		{"lock not a candidate", func() mapObj {
			s := installSpell(nil)
			man := s["manifests"].([]any)[0].(map[string]any)
			man["installs"] = map[string]any{"yarn.lock": man["installs"].(map[string]any)["pnpm-lock.yaml"]}
			return s
		}(), `"yarn.lock" is not one of this manifest's lockCandidates`},
		{"relocatable without dir", installSpell(func(in map[string]any) { delete(in, "dir") }), "relocatable names no dir"},
		{"stamp outside the project", installSpell(func(in map[string]any) { in["stamps"] = []string{"../x"} }), "must be a path inside the project"},
		{"undeclared tool", installSpell(func(in map[string]any) { in["tools"] = []string{"node"} }), `tool "node" is not declared`},
		{"no command", installSpell(func(in map[string]any) { delete(in, "command") }), "command is required"},
		{"no name", installSpell(func(in map[string]any) { delete(in, "name") }), "name is required"},
		{"authored op collides", func() mapObj {
			s := installSpell(nil)
			s["ops"] = map[string]any{"pnpm-install": map[string]any{"bin": "pnpm"}}
			return s
		}(), "drop the op"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(tc.src)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))
}

func TestResolveInstall(t *testing.T) {
	pnpm := spells.Install{Command: spells.Command{Bin: "pnpm"}}
	spec := &spells.InstallSpec{Spell: "ts", Manifests: []spells.Manifest{{
		Value:          "package.json",
		LockCandidates: []string{"pnpm-lock.yaml", "yarn.lock"},
		Installs:       map[string]spells.Install{"pnpm-lock.yaml": pnpm},
	}}}

	t.Run("walks up to a hoisted lock", func(t *testing.T) {
		root := t.TempDir()
		proj := filepath.Join(root, "pkg", "a")
		writeFile(t, filepath.Join(proj, "package.json"))
		writeFile(t, filepath.Join(root, "pnpm-lock.yaml"))
		got, found, err := ResolveInstall(spec, proj, root)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, spells.InstallChoice{
			Manifest: filepath.Join(proj, "package.json"),
			Lock:     filepath.Join(root, "pnpm-lock.yaml"),
			Install:  pnpm,
		}, got)
	})
	t.Run("no lock pins nothing", func(t *testing.T) {
		proj := t.TempDir()
		writeFile(t, filepath.Join(proj, "package.json"))
		_, found, err := ResolveInstall(spec, proj, proj)
		require.NoError(t, err)
		assert.False(t, found)
	})
	t.Run("no manifest", func(t *testing.T) {
		_, found, err := ResolveInstall(spec, t.TempDir(), "")
		require.NoError(t, err)
		assert.False(t, found)
	})
	t.Run("a lock with no install is an error", func(t *testing.T) {
		proj := t.TempDir()
		writeFile(t, filepath.Join(proj, "package.json"))
		writeFile(t, filepath.Join(proj, "yarn.lock"))
		_, _, err := ResolveInstall(spec, proj, proj)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `spell "ts" declares no install for`)
	})
}

// TestBuiltinTypescriptInstall pins the shipped pnpm install: it never writes the
// lockfile, prefers the store, and update turns it into a re-resolve.
func TestBuiltinTypescriptInstall(t *testing.T) {
	spec, ok := Builtins()["typescript"]
	require.True(t, ok)
	op, ok := spec.Ops["pnpm-install"]
	require.True(t, ok)
	require.Equal(t, spells.OpKindInstall, op.Kind)
	in := op.Install.Manifests[0].Installs["pnpm-lock.yaml"]
	assert.Equal(t, []string{"install", "--frozen-lockfile", "--prefer-offline"}, in.Command.Args)
	steps, err := ExplainCharms(in.Command.Args, in.Command.Charms, []string{"update"})
	require.NoError(t, err)
	assert.Equal(t, []string{"update"}, steps[len(steps)-1].Command)
	assert.True(t, in.Relocatable)
	assert.Equal(t, []string{"node_modules/.pnpm/lock.yaml", "node_modules/.modules.yaml"}, in.Stamps)
	assert.Equal(t, []string{"node", "pnpm"}, in.Tools)
}

// TestBuiltinTypescriptInstallSplitsByBinary pins the naming rename: one op per
// binary, not one "install" that resolves dynamically. npm-ci covers BOTH
// package-lock.json and npm-shrinkwrap.json, since both run `npm ci`.
func TestBuiltinTypescriptInstallSplitsByBinary(t *testing.T) {
	spec, ok := Builtins()["typescript"]
	require.True(t, ok)
	_, hasInstall := spec.Ops["install"]
	assert.False(t, hasInstall, "the plain name is retired; each binary gets its own op")

	npm, ok := spec.Ops["npm-ci"]
	require.True(t, ok)
	require.Equal(t, spells.OpKindInstall, npm.Kind)
	require.Equal(t, "npm", npm.Bin)
	man := npm.Install.Manifests[0]
	assert.ElementsMatch(t, []string{"package-lock.json", "npm-shrinkwrap.json"}, man.LockCandidates)
	for _, lock := range man.LockCandidates {
		assert.Equal(t, "npm-ci", man.Installs[lock].Name)
	}
}

// TestDecode_InstallGroupsSharedNameOneOp pins the grouping rule two lock candidates
// declaring the same Name (a shape only typescript's npm-ci exercises today) register
// as ONE op scoped to both their lock candidates, not two.
func TestDecode_InstallGroupsSharedNameOneOp(t *testing.T) {
	s := installSpell(nil)
	man := s["manifests"].([]any)[0].(map[string]any)
	man["lockCandidates"] = []string{"pnpm-lock.yaml", "npm-lock.json"}
	installs := man["installs"].(map[string]any)
	other := map[string]any{}
	for k, v := range installs["pnpm-lock.yaml"].(map[string]any) {
		other[k] = v
	}
	other["name"] = "pnpm-install"
	other["command"] = map[string]any{"bin": "npm-alias-of-pnpm", "args": []string{"install"}}
	installs["npm-lock.json"] = other

	m, err := Decode(s)
	require.NoError(t, err)
	require.Len(t, m.Ops, 1, "both lock candidates share a name and register as one op")
	op, ok := m.Ops["pnpm-install"]
	require.True(t, ok)
	assert.ElementsMatch(t, []string{"pnpm-lock.yaml", "npm-lock.json"}, op.Install.Manifests[0].LockCandidates)
}

// fakeWorktrees lays out a primary checkout and one linked worktree the way git does,
// using files only, and returns their roots.
func fakeWorktrees(t *testing.T) (primary, linked string) {
	t.Helper()
	names := fakeWorktreeSet(t, "linked")
	return names["primary"], names["linked"]
}

// fakeWorktreeSet lays out a primary checkout (holding the shared .git) plus one linked
// worktree per name, the way git does, using files only. It returns every root keyed by
// name, "primary" included, so a test can pick which siblings exist without hand-rolling
// the admin-dir plumbing per case.
func fakeWorktreeSet(t *testing.T, names ...string) map[string]string {
	t.Helper()
	base := t.TempDir()
	primary := filepath.Join(base, "primary")
	require.NoError(t, os.MkdirAll(primary, 0o755))
	out := map[string]string{"primary": primary}
	for _, name := range names {
		dir := filepath.Join(base, name)
		admin := filepath.Join(primary, ".git", "worktrees", name)
		require.NoError(t, os.MkdirAll(admin, 0o755))
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: "+admin+"\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(admin, "gitdir"), []byte(filepath.Join(dir, ".git")+"\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(admin, "commondir"), []byte("../..\n"), 0o644))
		out[name] = dir
	}
	return out
}

func TestSeedInstall(t *testing.T) {
	if !cloneSupported {
		t.Skip("no whole-tree clone on " + runtime.GOOS)
	}
	choice := spells.InstallChoice{Install: spells.Install{Dir: "node_modules", Relocatable: true}}

	t.Run("clones an absent tree from a sibling checkout", func(t *testing.T) {
		primary, linked := fakeWorktrees(t)
		writeFile(t, filepath.Join(primary, "app", "node_modules", ".pnpm", "lock.yaml"))
		proj := filepath.Join(linked, "app")
		require.NoError(t, os.MkdirAll(proj, 0o755))

		from, found, err := SeedInstall(context.Background(), choice, proj)
		require.NoError(t, err)
		require.True(t, found)
		real, _ := filepath.EvalSymlinks(primary)
		assert.Equal(t, real, from)
		assert.FileExists(t, filepath.Join(proj, "node_modules", ".pnpm", "lock.yaml"))
		leftovers, _ := filepath.Glob(filepath.Join(proj, "node_modules"+seedSuffix+"*"))
		assert.Empty(t, leftovers)
	})
	t.Run("never seeds over an existing tree", func(t *testing.T) {
		primary, linked := fakeWorktrees(t)
		writeFile(t, filepath.Join(primary, "app", "node_modules", "from-primary"))
		proj := filepath.Join(linked, "app")
		writeFile(t, filepath.Join(proj, "node_modules", "mine"))

		from, found, err := SeedInstall(context.Background(), choice, proj)
		require.NoError(t, err)
		assert.False(t, found)
		assert.Empty(t, from)
		assert.NoFileExists(t, filepath.Join(proj, "node_modules", "from-primary"))
	})
	t.Run("not relocatable", func(t *testing.T) {
		primary, linked := fakeWorktrees(t)
		writeFile(t, filepath.Join(primary, "app", ".venv", "x"))
		proj := filepath.Join(linked, "app")
		require.NoError(t, os.MkdirAll(proj, 0o755))

		from, found, err := SeedInstall(context.Background(), spells.InstallChoice{Install: spells.Install{Dir: ".venv"}}, proj)
		require.NoError(t, err)
		assert.False(t, found)
		assert.Empty(t, from)
		assert.NoDirExists(t, filepath.Join(proj, ".venv"))
	})
	t.Run("removes a clone a dead process abandoned", func(t *testing.T) {
		primary, linked := fakeWorktrees(t)
		writeFile(t, filepath.Join(primary, "app", "node_modules", "x"))
		proj := filepath.Join(linked, "app")
		// Far above any pid macOS hands out, so it reads as dead.
		stale := filepath.Join(proj, "node_modules"+seedSuffix+"99999999")
		writeFile(t, filepath.Join(stale, "partial"))

		_, _, err := SeedInstall(context.Background(), choice, proj)
		require.NoError(t, err)
		assert.NoDirExists(t, stale)
	})
	t.Run("leaves a live seed's lock and directory alone", func(t *testing.T) {
		primary, linked := fakeWorktrees(t)
		writeFile(t, filepath.Join(primary, "app", "node_modules", "x"))
		proj := filepath.Join(linked, "app")
		// A reused pid must not decide this: the flock, not the number, is live.
		live := filepath.Join(proj, "node_modules"+seedSuffix+"424242")
		writeFile(t, filepath.Join(live, "partial"))
		lock, err := acquireSeedLock(live + seedLockSuffix)
		require.NoError(t, err)
		defer lock.Close()

		_, _, err = SeedInstall(context.Background(), choice, proj)
		require.NoError(t, err)
		assert.DirExists(t, live)
	})
	t.Run("tries the next sibling when one clone fails", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("running as root; permission checks do not apply")
		}
		roots := fakeWorktreeSet(t, "bad", "good", "linked")
		badSrc := filepath.Join(roots["bad"], "app", "node_modules")
		writeFile(t, filepath.Join(badSrc, "x"))
		writeFile(t, filepath.Join(roots["good"], "app", "node_modules", "x"))
		proj := filepath.Join(roots["linked"], "app")
		require.NoError(t, os.MkdirAll(proj, 0o755))
		// Unreadable, so cloning it fails and SeedInstall must fall through to "good"
		// rather than giving up on the first sibling ("bad" sorts before "good").
		require.NoError(t, os.Chmod(badSrc, 0o000))
		t.Cleanup(func() { _ = os.Chmod(badSrc, 0o755) })

		from, found, err := SeedInstall(context.Background(), choice, proj)
		require.NoError(t, err)
		require.True(t, found)
		real, _ := filepath.EvalSymlinks(roots["good"])
		assert.Equal(t, real, from)
		assert.FileExists(t, filepath.Join(proj, "node_modules", "x"))
	})
}
