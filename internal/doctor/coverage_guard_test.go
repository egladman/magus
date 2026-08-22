package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// plant writes a file under root, creating its parents, and returns the path.
func plant(t *testing.T, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

// touchAt backdates or advances a path's mtime, which is the whole input to the
// staleness comparison.
func touchAt(t *testing.T, path string, at time.Time) {
	t.Helper()
	require.NoError(t, os.Chtimes(path, at, at))
}

// withoutPathMagus empties PATH so exec.LookPath("magus") cannot resolve, which is what
// makes "no guard at all" reachable on a developer machine that has one installed.
func withoutPathMagus(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

func TestNewestGoSource(t *testing.T) {
	root := t.TempDir()
	base := time.Now().Add(-24 * time.Hour)

	touchAt(t, plant(t, root, "old.go", "package a\n"), base)
	newest := plant(t, root, "sub/new.go", "package b\n")
	touchAt(t, newest, base.Add(time.Hour))
	// Not Go source, so its mtime must not win.
	touchAt(t, plant(t, root, "README.md", "hi\n"), base.Add(10*time.Hour))
	// The directories that never hold guard sources, each planted with a file newer
	// than everything else so a missing skip shows up as the wrong answer.
	for _, dir := range []string{".git", "node_modules", "gen", ".claude"} {
		touchAt(t, plant(t, root, dir+"/skipped.go", "package c\n"), base.Add(20*time.Hour))
	}

	at, path := newestGoSource(root)
	assert.Equal(t, filepath.FromSlash("sub/new.go"), path)
	assert.WithinDuration(t, base.Add(time.Hour), at, time.Second)
}

func TestNewestGoSourceWithoutGoFiles(t *testing.T) {
	at, path := newestGoSource(t.TempDir())
	assert.True(t, at.IsZero())
	assert.Equal(t, "", path)
}

func TestCheckGuardBinary(t *testing.T) {
	// A stale guard is worse than an absent one: an absent guard is noticed within a
	// command or two, a stale one is trusted indefinitely.
	t.Run("older than the working tree", func(t *testing.T) {
		root := t.TempDir()
		now := time.Now()
		touchAt(t, plant(t, root, "main.go", "package main\n"), now)
		bin := plant(t, root, "magus", "#!/bin/sh\n")
		require.NoError(t, os.Chmod(bin, 0o755))
		touchAt(t, bin, now.Add(-time.Hour))

		got := (&runner{ws: rootStubWorkspace{root: root}}).checkGuardBinary()
		assert.Equal(t, types.DoctorFail, got.Status)
		assert.Contains(t, got.Message, "stale rules")
		require.Len(t, got.Details, 3)
		assert.Contains(t, got.Details[1], filepath.FromSlash("main.go"))
		assert.Contains(t, got.Details[2], "rebuild:")
	})

	t.Run("newer than every tracked Go source", func(t *testing.T) {
		root := t.TempDir()
		now := time.Now()
		touchAt(t, plant(t, root, "main.go", "package main\n"), now.Add(-time.Hour))
		bin := plant(t, root, "magus", "#!/bin/sh\n")
		require.NoError(t, os.Chmod(bin, 0o755))
		touchAt(t, bin, now)

		got := (&runner{ws: rootStubWorkspace{root: root}}).checkGuardBinary()
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Contains(t, got.Message, "hook would run ./magus")
	})

	// The resolved path is reported always, not only on failure: "which binary is
	// judging me?" has no other way to be asked.
	t.Run("falls back to PATH", func(t *testing.T) {
		dir := t.TempDir()
		fake := filepath.Join(dir, "magus")
		require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755))
		t.Setenv("PATH", dir)

		got := (&runner{ws: rootStubWorkspace{root: t.TempDir()}}).checkGuardBinary()
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Contains(t, got.Message, "no ./magus built")
		assert.Contains(t, got.Message, fake)
	})

	t.Run("no binary anywhere", func(t *testing.T) {
		withoutPathMagus(t)
		got := (&runner{ws: rootStubWorkspace{root: t.TempDir()}}).checkGuardBinary()
		assert.Equal(t, types.DoctorFail, got.Status)
		assert.Contains(t, got.Message, "a guard hook is unenforced")
		assert.Equal(t, []string{"build one: magus run build ."}, got.Details)
	})

	// A non-executable ./magus is not a binary a hook can run, so resolution has to
	// carry on to PATH rather than stopping at the name.
	t.Run("non-executable ./magus", func(t *testing.T) {
		withoutPathMagus(t)
		root := t.TempDir()
		plant(t, root, "magus", "not a binary\n")

		got := (&runner{ws: rootStubWorkspace{root: root}}).checkGuardBinary()
		assert.Equal(t, types.DoctorFail, got.Status)
		assert.Contains(t, got.Message, "no ./magus and no magus on PATH")
	})
}

// TestResolveGuardBinaryForWiring is kept separate from checkGuardBinary on purpose, so
// a change to one check's resolution order cannot silently retarget the other's canary.
func TestResolveGuardBinaryForWiring(t *testing.T) {
	t.Run("prefers ./magus", func(t *testing.T) {
		root := t.TempDir()
		bin := plant(t, root, "magus", "#!/bin/sh\n")
		require.NoError(t, os.Chmod(bin, 0o755))

		got, ok := resolveGuardBinaryForWiring(root)
		require.True(t, ok)
		assert.Equal(t, bin, got)
	})

	t.Run("falls back to PATH", func(t *testing.T) {
		dir := t.TempDir()
		fake := filepath.Join(dir, "magus")
		require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755))
		t.Setenv("PATH", dir)

		got, ok := resolveGuardBinaryForWiring(t.TempDir())
		require.True(t, ok)
		assert.Equal(t, fake, got)
	})

	t.Run("nothing to resolve", func(t *testing.T) {
		withoutPathMagus(t)
		_, ok := resolveGuardBinaryForWiring(t.TempDir())
		assert.False(t, ok)
	})

	t.Run("a directory named magus is not a binary", func(t *testing.T) {
		withoutPathMagus(t)
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "magus"), 0o755))

		_, ok := resolveGuardBinaryForWiring(root)
		assert.False(t, ok)
	})
}

func TestGuardWiringCandidates(t *testing.T) {
	withHome := guardWiringCandidates("/repo", "/home/dev")
	assert.Equal(t, []string{
		filepath.Join("/repo", ".claude", "settings.json"),
		filepath.Join("/repo", ".cursor", "hooks.json"),
		filepath.Join("/repo", ".opencode", "plugins"),
		filepath.Join("/home/dev", ".codex", "config.toml"),
		filepath.Join("/home/dev", ".config", "opencode", "plugins"),
	}, withHome)

	// os.UserHomeDir can fail, and the workspace-relative candidates still apply.
	assert.Len(t, guardWiringCandidates("/repo", ""), 3)
}

func TestGuardReferencedTemplates(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, ".claude")
	require.NoError(t, os.MkdirAll(configDir, 0o755))

	fromRoot := plant(t, root, "docs/guides/magus-guard-command.sh", "#!/bin/sh\n")
	besideConfig := plant(t, configDir, "magus-guard-path.sh", "#!/bin/sh\n")

	t.Run("resolves against the root and against the config dir", func(t *testing.T) {
		body := []byte(`{"command": "sh docs/guides/magus-guard-command.sh", "other": "magus-guard-path.sh"}`)
		found, missing := guardReferencedTemplates(root, configDir, body)
		assert.Equal(t, []string{fromRoot, besideConfig}, found)
		assert.Empty(t, missing)
	})

	// A config whose hook points at a template that is not there runs nothing;
	// reporting only what resolved would grade exactly that case as healthy.
	t.Run("an unresolvable token is the finding", func(t *testing.T) {
		body := []byte(`{"command": "sh hooks/cursor-guard.sh"}`)
		found, missing := guardReferencedTemplates(root, configDir, body)
		assert.Empty(t, found)
		assert.Equal(t, []string{"hooks/cursor-guard.sh"}, missing, "the token is reported as the config wrote it")
	})

	t.Run("a config naming no template", func(t *testing.T) {
		found, missing := guardReferencedTemplates(root, configDir, []byte(`{"hooks": []}`))
		assert.Empty(t, found)
		assert.Empty(t, missing)
	})

	// The token starts at the nearest quote, space, tab, newline or '=', so a bare
	// basename is taken whole rather than swallowing the word before it.
	t.Run("a bare basename", func(t *testing.T) {
		bare := plant(t, root, "cursor-guard.sh", "#!/bin/sh\n")
		found, missing := guardReferencedTemplates(root, configDir, []byte("hook=cursor-guard.sh\n"))
		assert.Equal(t, []string{bare}, found)
		assert.Empty(t, missing)
	})
}

func TestGuardTemplateMarkerProblem(t *testing.T) {
	marker := agent.GuardTemplateMarker

	t.Run("current", func(t *testing.T) {
		body := []byte("#!/bin/sh\n# " + marker + " " + strconv.Itoa(agent.GuardTemplateVersion) + "\n")
		assert.Equal(t, "", guardTemplateMarkerProblem("/x.sh", body))
	})

	t.Run("ahead of this binary", func(t *testing.T) {
		body := []byte("# " + marker + " " + strconv.Itoa(agent.GuardTemplateVersion+1) + "\n")
		assert.Equal(t, "", guardTemplateMarkerProblem("/x.sh", body))
	})

	t.Run("behind", func(t *testing.T) {
		body := []byte("# " + marker + " 1\n")
		got := guardTemplateMarkerProblem("/x.sh", body)
		assert.Contains(t, got, "/x.sh carries template version 1, current is "+strconv.Itoa(agent.GuardTemplateVersion))
		assert.Contains(t, got, "re-download it")
	})

	// A MISSING marker is a finding, not a pass: the marker postdates the templates,
	// so a copy carrying none is older than versioning. Measured on a real machine, a
	// plugin still calling a removed subcommand graded healthy without this.
	t.Run("no marker at all", func(t *testing.T) {
		got := guardTemplateMarkerProblem("/x.sh", []byte("#!/bin/sh\necho hi\n"))
		assert.Contains(t, got, "carries no "+marker+" line")
		assert.Contains(t, got, "predates template versioning")
	})

	// An unreadable version is treated as current rather than as a version-0 copy:
	// the marker is there, so the file is not from before versioning.
	t.Run("unparsable version", func(t *testing.T) {
		assert.Equal(t, "", guardTemplateMarkerProblem("/x.sh", []byte("# "+marker+" seven\n")))
	})

	// The marker on the last line, with no newline after it.
	t.Run("marker at end of file", func(t *testing.T) {
		assert.Equal(t, "", guardTemplateMarkerProblem("/x.sh", []byte("# "+marker+" "+strconv.Itoa(agent.GuardTemplateVersion))))
	})
}

func TestCheckObserverRecording(t *testing.T) {
	// observe appends n hook observations of one tool into the workspace's trail.
	observe := func(t *testing.T, root, tool string, n int) {
		t.Helper()
		base := (&runner{root: root}).cacheDir()
		for i := range n {
			trail.AppendAgentCommand(context.Background(), base, trail.AgentCommand{
				Host: "test",
				Tool: tool,
				Path: fmt.Sprintf("file-%d.go", i),
			})
		}
	}

	// A workspace no agent has run in is the ordinary case, and failing it would train
	// people to ignore the check.
	t.Run("nothing recorded", func(t *testing.T) {
		got := (&runner{root: t.TempDir()}).checkObserverRecording()
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Contains(t, got.Message, "no agent activity recorded yet")
	})

	// Below the sample floor a handful of events with no reads is what a fixture or
	// one session looks like; judging it would be this check making the exact mistake
	// it exists to catch.
	t.Run("too few to judge", func(t *testing.T) {
		root := t.TempDir()
		observe(t, root, "file.write", observerMinSample-1)

		got := (&runner{root: root}).checkObserverRecording()
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Contains(t, got.Message, "too few to judge")
		assert.Contains(t, got.Message, strconv.Itoa(observerMinSample-1)+" observation(s)")
	})

	// Commands with no reads is the diagnostic pattern: wiring correct, doctor green,
	// and the story behind a change unreconstructable.
	t.Run("not one read", func(t *testing.T) {
		root := t.TempDir()
		observe(t, root, "file.write", 20)
		observe(t, root, "shell.command", 40)

		got := (&runner{root: root}).checkObserverRecording()
		assert.Equal(t, types.DoctorFail, got.Status)
		assert.Contains(t, got.Message, "NOT ONE read")
		require.NotEmpty(t, got.Details)
		assert.Contains(t, got.Details[0], "writes: 20")
		assert.Contains(t, got.Details[0], "shell: 40")
		assert.Contains(t, got.Details[0], "reads: 0")
	})

	// Wired, recording, and still useless for its purpose: a diff can name the agent
	// that wrote a file but not what it had just read.
	t.Run("sparse reading trail", func(t *testing.T) {
		root := t.TempDir()
		observe(t, root, "file.read", 5)
		observe(t, root, "file.write", 60)

		got := (&runner{root: root}).checkObserverRecording()
		assert.Equal(t, types.DoctorAdvice, got.Status)
		assert.Contains(t, got.Message, "too sparse to explain a change")
		assert.Contains(t, got.Message, "5 read(s) against 60 write(s)")
	})

	t.Run("recording healthily", func(t *testing.T) {
		root := t.TempDir()
		observe(t, root, "file.read", 60)
		observe(t, root, "file.write", 10)

		got := (&runner{root: root}).checkObserverRecording()
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Contains(t, got.Message, "recording: 60 read(s), 10 write(s)")
	})
}

func TestCacheDir(t *testing.T) {
	assert.Equal(t, filepath.Join("/repo", ".magus"), (&runner{root: "/repo"}).cacheDir())

	abs := &runner{root: "/repo", opts: options{cfg: config.Config{Cache: config.Cache{Dir: "/var/cache/magus/"}}}}
	assert.Equal(t, filepath.FromSlash("/var/cache/magus"), abs.cacheDir())

	rel := &runner{root: "/repo", opts: options{cfg: config.Config{Cache: config.Cache{Dir: "build/cache"}}}}
	assert.Equal(t, filepath.Join("/repo", "build", "cache"), rel.cacheDir())
}

func TestFirstExistingConfig(t *testing.T) {
	assert.Equal(t, "", firstExistingConfig(t.TempDir()))

	dotted := t.TempDir()
	want := plant(t, dotted, ".magus.yaml", "log:\n")
	assert.Equal(t, want, firstExistingConfig(dotted))

	// magus.yaml wins when both exist, matching the loader's own order.
	both := t.TempDir()
	plain := plant(t, both, "magus.yaml", "log:\n")
	plant(t, both, ".magus.yaml", "log:\n")
	assert.Equal(t, plain, firstExistingConfig(both))
}

func TestConfigFilePaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	root := t.TempDir()
	want := plant(t, root, "magus.yaml", "log:\n")

	got := configFilePaths(root)
	assert.Contains(t, got, want)
	// Only files that exist: every path returned is handed straight to config.LoadFile,
	// and a missing one would be reported as a config problem the user does not have.
	for _, p := range got {
		_, err := os.Stat(p)
		assert.NoError(t, err, p)
	}

	// An empty root is the daemon's path, where the workspace arrives through r.ws.
	assert.NotPanics(t, func() { configFilePaths("") })
}

func TestNameConvention(t *testing.T) {
	cases := []struct{ name, want string }{
		{"build", ""},
		{"", ""},
		{"no_cache", "snake_case"},
		{"buildAll", "camelCase"},
		{"BuildAll", "PascalCase"},
		{"Build", "PascalCase"},
		// A delimiter wins over casing: snake_case is the stronger signal.
		{"Build_All", "snake_case"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, nameConvention(c.name), "nameConvention(%q)", c.name)
	}
}

func TestEscapesRoot(t *testing.T) {
	assert.False(t, escapesRoot(""), "many node kinds put a name rather than a path here")
	assert.False(t, escapesRoot("internal/doctor/checks.go"))
	assert.False(t, escapesRoot("a..b"), "a name containing dots is not a parent segment")
	assert.True(t, escapesRoot("../elsewhere/x.go"))
	assert.True(t, escapesRoot("a/../../b"))
}

func TestToSlashRel(t *testing.T) {
	root := filepath.FromSlash("/repo")
	assert.Equal(t, "internal/doctor/checks.go",
		toSlashRel(root, filepath.Join(root, "internal", "doctor", "checks.go")))
	// filepath.Rel fails against an empty root, and the absolute path is better than
	// a detail naming no file at all.
	assert.Equal(t, filepath.FromSlash("/repo/x.go"), toSlashRel("", filepath.FromSlash("/repo/x.go")))
}

func TestSymlinkEscapes(t *testing.T) {
	// EvalSymlinks first: macOS's TempDir sits under the /var -> /private/var link, and
	// an unresolved root makes every in-tree target read as an escape.
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	plant(t, root, "inside.txt", "hi\n")
	plant(t, outside, "outside.txt", "hi\n")

	t.Run("in-tree", func(t *testing.T) {
		link := filepath.Join(root, "in.link")
		require.NoError(t, os.Symlink(filepath.Join(root, "inside.txt"), link))
		_, escapes := symlinkEscapes(root, link)
		assert.False(t, escapes)
	})

	// The sandbox-escape vector this check exists for, where landlock is unavailable.
	t.Run("escaping", func(t *testing.T) {
		link := filepath.Join(root, "out.link")
		require.NoError(t, os.Symlink(filepath.Join(outside, "outside.txt"), link))
		target, escapes := symlinkEscapes(root, link)
		assert.True(t, escapes)
		assert.Contains(t, target, "outside.txt")
	})

	// EvalSymlinks fails on a dangling link, so direction is judged lexically instead
	// of the link being waved through.
	t.Run("dangling relative link", func(t *testing.T) {
		link := filepath.Join(root, "dangling.link")
		require.NoError(t, os.Symlink("../gone.txt", link))
		_, escapes := symlinkEscapes(root, link)
		assert.True(t, escapes)
	})

	t.Run("dangling in-tree link", func(t *testing.T) {
		link := filepath.Join(root, "dangling-inside.link")
		require.NoError(t, os.Symlink("gone.txt", link))
		_, escapes := symlinkEscapes(root, link)
		assert.False(t, escapes)
	})
}

func TestPrunedPrefix(t *testing.T) {
	// Nobody writes "gen/*.binpb" hoping it matches nothing.
	for _, glob := range []string{"gen/*.binpb", "./gen/**/*.go", "node_modules/**/*.js", "vendor/*"} {
		_, ok := prunedPrefix(glob)
		assert.True(t, ok, "prunedPrefix(%q)", glob)
	}
	dir, ok := prunedPrefix("gen/*.binpb")
	require.True(t, ok)
	assert.Equal(t, "gen", dir)

	// A wildcard-free path names one file and is resolved by stat, so it reaches the
	// key from inside a pruned tree normally.
	for _, glob := range []string{"gen/knowledge-graph.json", "**/*.go", "internal/**/*.go", "src/*.ts"} {
		_, ok := prunedPrefix(glob)
		assert.False(t, ok, "prunedPrefix(%q)", glob)
	}
}

// TestPrunedPrefixIgnoresARelativePrefix keeps "." and ".." out of the segment scan,
// which would otherwise never match an ignore dir but would cost a lookup each.
func TestPrunedPrefixIgnoresARelativePrefix(t *testing.T) {
	dir, ok := prunedPrefix("../gen/*.go")
	require.True(t, ok)
	assert.Equal(t, "gen", dir)
}
