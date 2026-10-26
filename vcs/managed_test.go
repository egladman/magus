package vcs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/stamp"
)

const tornBanner = "# BEGIN magus-generated - do not edit this section manually"

func TestReplaceManagedSection(t *testing.T) {
	want := generatedMarkers.section("a.go merge=magus\n")

	tests := []struct {
		name    string
		text    string
		want    string
		wantErr string
	}{
		{
			name: "empty file becomes the section alone",
			text: "",
			want: want,
		},
		{
			name: "unmanaged text keeps its content and gains the section",
			text: "*.png binary\n",
			want: "*.png binary\n\n" + want,
		},
		{
			name: "existing section is replaced in place",
			text: generatedMarkers.section("old.go merge=magus\n"),
			want: want,
		},
		{
			name: "surrounding hand-written lines survive on both sides",
			text: fmt.Sprintf(`*.png binary

%s
*.jpg binary
`, generatedMarkers.section("old.go merge=magus\n")),
			want: fmt.Sprintf(`*.png binary

%s
*.jpg binary
`, want),
		},
		{
			// The banner wording changed once (an em-dash went away). Matching begin as
			// a prefix keeps the old section findable instead of stranding it above a
			// freshly appended copy.
			name: "section written with the older banner wording is replaced, not duplicated",
			text: fmt.Sprintf(`%s — do not edit this section manually
old.go merge=magus
%s
`, generatedMarkers.begin, generatedMarkers.end),
			want: want,
		},
		{
			// The regression this was rewritten for: an end marker found by whole-text
			// scan matched the FIRST section's end, so a begin further down failed the
			// ordering test and appended instead of replacing, once per invocation.
			name: "accumulated duplicate sections collapse to one",
			text: fmt.Sprintf(`%s
%s
%s`, generatedMarkers.section("first.go merge=magus\n"), generatedMarkers.section("second.go merge=magus\n"), generatedMarkers.section("third.go merge=magus\n")),
			want: want,
		},
		{
			name: "duplicates collapse without eating hand-written lines between them",
			text: fmt.Sprintf(`%s
keep-me
%s`, generatedMarkers.section("first.go merge=magus\n"), generatedMarkers.section("second.go merge=magus\n")),
			want: want + "\nkeep-me\n",
		},
		{
			name:    "a begin with no end is an error naming its line",
			text:    "*.png binary\n" + tornBanner + "\ndangling\n",
			wantErr: `line 2: "# BEGIN magus-generated" has no "# END magus-generated" before the next begin marker or the end of the file, delete the torn section by hand and rerun`,
		},
		{
			// Pairing the torn begin with the complete section's end would swallow the
			// lines between them.
			name: "a begin whose end follows the next begin is an error",
			text: fmt.Sprintf(`%s
dangling

%s`, tornBanner, generatedMarkers.section("old.go merge=magus\n")),
			wantErr: `line 1: "# BEGIN magus-generated" has no "# END magus-generated" before the next begin marker or the end of the file, delete the torn section by hand and rerun`,
		},
		{
			name: "mid-line begin is ignored so the section is appended",
			text: fmt.Sprintf("note: see %s in docs\n", generatedMarkers.begin),
			want: fmt.Sprintf(`note: see %s in docs

%s`, generatedMarkers.begin, want),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := replaceManagedSection(tt.text, want, generatedMarkers)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestReplaceManagedSectionIdempotent pins what the accumulation bug broke: applying the
// same section repeatedly converges after the first write.
func TestReplaceManagedSectionIdempotent(t *testing.T) {
	want := generatedMarkers.section("a.go merge=magus\n")
	text := "*.png binary\n"
	for range 5 {
		var err error
		text, err = replaceManagedSection(text, want, generatedMarkers)
		require.NoError(t, err)
	}
	assert.Equal(t, "*.png binary\n\n"+want, text)
}

func TestManagedSectionPresent(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
		return path
	}

	got, err := managedSectionPresent(filepath.Join(dir, "missing"), generatedMarkers)
	require.NoError(t, err, "a missing file holds no section and is not an error")
	assert.False(t, got)

	got, err = managedSectionPresent(write("complete", generatedMarkers.section("a\n")), generatedMarkers)
	require.NoError(t, err)
	assert.True(t, got)

	got, err = managedSectionPresent(write("quoted", fmt.Sprintf("see %s\n", generatedMarkers.begin)), generatedMarkers)
	require.NoError(t, err)
	assert.False(t, got, "a marker quoted mid-line is not a section")

	torn := write("torn", tornBanner+"\n")
	_, err = managedSectionPresent(torn, generatedMarkers)
	require.EqualError(t, err, torn+`: line 1: "# BEGIN magus-generated" has no "# END magus-generated" before the next begin marker or the end of the file, delete the torn section by hand and rerun`)
}

func TestEnsureShShebang(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		want    string
		wantErr bool
	}{
		{name: "empty file", text: "", want: "#!/bin/sh\n\n"},
		{name: "body without a shebang", text: "\necho hi\n", want: "#!/bin/sh\n\necho hi\n"},
		{name: "sh", text: "#!/bin/sh\necho hi\n", want: "#!/bin/sh\necho hi\n"},
		{name: "bash with a flag", text: "#!/bin/bash -e\necho hi\n", want: "#!/bin/bash -e\necho hi\n"},
		{name: "zsh", text: "#!/usr/bin/zsh\n", want: "#!/usr/bin/zsh\n"},
		{name: "env sh", text: "#!/usr/bin/env sh\n", want: "#!/usr/bin/env sh\n"},
		{name: "env -S bash", text: "#!/usr/bin/env -S bash -e\n", want: "#!/usr/bin/env -S bash -e\n"},
		{
			// The kernel reads a shebang only at byte 0, so one after a blank line is a
			// comment and the hook runs under sh anyway.
			name: "a shebang after a blank line does not count",
			text: "\n#!/usr/bin/python3\n",
			want: "#!/bin/sh\n\n#!/usr/bin/python3\n",
		},
		{name: "python", text: "#!/usr/bin/python3\nprint('hi')\n", wantErr: true},
		{name: "env node", text: "#!/usr/bin/env node\n", wantErr: true},
		{name: "bare shebang", text: "#!\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ensureShShebang("hooks/pre-push", tt.text)
			if tt.wantErr {
				first, _, _ := strings.Cut(tt.text, "\n")
				require.EqualError(t, err, fmt.Sprintf(`vcs: hook hooks/pre-push runs "%s", not a POSIX shell, and magus appends sh to its hooks, call that script from a sh hook instead`, first))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// A CRLF hook gets its shebang checked and prepended on the same LF view, then comes back
// CRLF throughout.
func TestRenderManagedFileKeepsCRLF(t *testing.T) {
	body := "magus job run sync-graph >/dev/null 2>&1 || true\n"
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }

	got, err := renderManagedFile("post-merge", crlf("echo mine\n"), refreshMarkers, body, hookFile, stamp.Judge{})
	require.NoError(t, err)
	assert.Equal(t, crlf("#!/bin/sh\n\necho mine\n\n"+refreshMarkers.section(body)), got)

	got, err = renderManagedFile("post-merge", crlf("#!/bin/sh\necho mine\n"), refreshMarkers, body, hookFile, stamp.Judge{})
	require.NoError(t, err)
	assert.Equal(t, crlf("#!/bin/sh\necho mine\n\n"+refreshMarkers.section(body)), got)

	_, err = renderManagedFile("post-merge", crlf("#!/usr/bin/env node\n"), refreshMarkers, body, hookFile, stamp.Judge{})
	require.Error(t, err, "the interpreter check reads the CRLF line without its carriage return")
}

func TestWriteManagedSectionHookEndsExecutable(t *testing.T) {
	body := "magus job run check-drift >/dev/null 2>&1 || true\n"

	t.Run("an existing 0644 hook", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "post-commit")
		require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho mine\n"), 0o644))

		changed, err := writeManagedSection(filepath.Dir(path), path, driftMarkers, body, hookFile, stamp.Judge{})
		require.NoError(t, err)
		assert.True(t, changed)
		assertFile(t, path, "#!/bin/sh\necho mine\n\n"+driftMarkers.section(body), 0o755)
	})

	t.Run("a current 0644 hook is only made executable", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "post-commit")
		want := "#!/bin/sh\n\n" + driftMarkers.section(body)
		require.NoError(t, os.WriteFile(path, []byte(want), 0o644))

		changed, err := writeManagedSection(filepath.Dir(path), path, driftMarkers, body, hookFile, stamp.Judge{})
		require.NoError(t, err)
		assert.True(t, changed, "a hook git would skip is not current")
		assertFile(t, path, want, 0o755)
	})

	t.Run("a new hook", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "post-commit")
		changed, err := writeManagedSection(filepath.Dir(path), path, driftMarkers, body, hookFile, stamp.Judge{})
		require.NoError(t, err)
		assert.True(t, changed)
		assertFile(t, path, "#!/bin/sh\n\n"+driftMarkers.section(body), 0o755)
	})
}

func TestWriteManagedSectionKeepsAnExistingMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hgrc")
	require.NoError(t, os.WriteFile(path, []byte("[ui]\nusername = me\n"), 0o600))

	changed, err := writeManagedSection(filepath.Dir(path), path, refreshMarkers, "[hooks]\n", configFile, stamp.Judge{})
	require.NoError(t, err)
	assert.True(t, changed)
	assertFile(t, path, "[ui]\nusername = me\n\n"+refreshMarkers.section("[hooks]\n"), 0o600)
}

// The steady state runs on every workspace load, so it must not touch the file at all:
// same inode, same mtime.
func TestWriteManagedSectionNoOpDoesNotRewrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gitattributes")
	_, err := writeManagedSection(filepath.Dir(path), path, generatedMarkers, "gen/** merge=magus\n", configFile, stamp.Judge{})
	require.NoError(t, err)
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	require.NoError(t, os.Chtimes(path, past, past))
	before, err := os.Stat(path)
	require.NoError(t, err)

	changed, err := writeManagedSection(filepath.Dir(path), path, generatedMarkers, "gen/** merge=magus\n", configFile, stamp.Judge{})
	require.NoError(t, err)
	assert.False(t, changed)

	after, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, after), "a no-op must not replace the file")
	assert.True(t, past.Equal(after.ModTime()), "mtime moved from %s to %s", past, after.ModTime())
}

func TestWriteManagedSectionRefusesATornSection(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".gitattributes")
	torn := "*.png binary\n" + tornBanner + "\ngen/** merge=magus\n"
	require.NoError(t, os.WriteFile(path, []byte(torn), 0o644))

	changed, err := writeManagedSection(filepath.Dir(path), path, generatedMarkers, "gen/** merge=magus\n", configFile, stamp.Judge{})
	require.EqualError(t, err, path+`: line 2: "# BEGIN magus-generated" has no "# END magus-generated" before the next begin marker or the end of the file, delete the torn section by hand and rerun`)
	assert.False(t, changed)
	assertFile(t, path, torn, 0o644)
}

func TestWriteManagedSectionRefusesANonShHook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-push")
	script := "#!/usr/bin/env python3\nprint('mine')\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))

	changed, err := writeManagedSection(filepath.Dir(path), path, driftMarkers, "magus job run check-drift\n", hookFile, stamp.Judge{})
	require.EqualError(t, err, fmt.Sprintf(`vcs: hook %s runs "#!/usr/bin/env python3", not a POSIX shell, and magus appends sh to its hooks, call that script from a sh hook instead`, path))
	assert.False(t, changed)
	assertFile(t, path, script, 0o755)
}

// A hook another tool manages is often a symlink; the write lands in its target and the
// link survives.
func TestWriteManagedSectionFollowsASymlinkedHook(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "shared-hook")
	require.NoError(t, os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755))
	link := filepath.Join(dir, "post-merge")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	_, err := writeManagedSection(dir, link, refreshMarkers, "true\n", hookFile, stamp.Judge{})
	require.NoError(t, err)

	info, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink, "the link is still a link")
	assertFile(t, target, "#!/bin/sh\n\n"+refreshMarkers.section("true\n"), 0o755)
}

// TestWithRepoLockSerializesWriters runs refresh and drift writers against one config
// concurrently. Unserialized, a read-modify-write drops the other kind's section.
func TestWithRepoLockSerializesWriters(t *testing.T) {
	meta := t.TempDir()
	path := filepath.Join(meta, "hgrc")
	var inside, overlapped atomic.Int32
	var wg sync.WaitGroup
	for i := range 16 {
		m := refreshMarkers
		if i%2 == 1 {
			m = driftMarkers
		}
		wg.Go(func() {
			_, err := lockedWrite(t.Context(), meta, func() (bool, error) {
				if inside.Add(1) > 1 {
					overlapped.Add(1)
				}
				defer inside.Add(-1)
				time.Sleep(time.Millisecond)
				return writeManagedSection(filepath.Dir(path), path, m, "[hooks]\n", configFile, stamp.Judge{})
			})
			assert.NoError(t, err)
		})
	}
	wg.Wait()

	assert.Zero(t, overlapped.Load(), "two writers held the lock at once")
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	refresh, drift := refreshMarkers.section("[hooks]\n"), driftMarkers.section("[hooks]\n")
	// Whichever kind landed first stays first; each kind appears exactly once.
	assert.Contains(t, []string{fmt.Sprintf(`%s
%s`, refresh, drift), fmt.Sprintf(`%s
%s`, drift, refresh)}, string(got))
}

func TestWithRepoLockHonorsCancellationWhileHeld(t *testing.T) {
	meta := t.TempDir()
	held := flock.New(filepath.Join(meta, managedLockName))
	require.NoError(t, held.Lock())
	t.Cleanup(func() { _ = held.Unlock() })

	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)
	ran := false
	err := withRepositoryLock(ctx, meta, func() error {
		ran = true
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, ran, "fn must not run without the lock")
}

// Two builds with no repository to order them: the version decides.
var (
	olderMagus = stamp.Writer{Version: "v0.4.3-120-g1111111", Commit: "1111111aaaa", Date: "2026-09-01T00:00:00Z"}
	newerMagus = stamp.Writer{Version: "v0.5.0", Commit: "2222222bbbb", Date: "2026-09-20T00:00:00Z"}
)

// A section a newer magus wrote refuses an older writer, naming the file, both builds and
// the fix, and is left as it was; an equal or older one is replaced and restamped.
func TestWriteManagedSectionRefusesADowngrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "post-commit")
	newer := stamp.Judge{Self: newerMagus}
	older := stamp.Judge{Self: olderMagus}
	_, err := writeManagedSection(filepath.Dir(path), path, regenMarkers, "magus vcs resolve --hook post-commit\n", hookFile, newer)
	require.NoError(t, err)
	written := fmt.Sprintf(`#!/bin/sh

%smagus vcs resolve --hook post-commit
%s
`, regenMarkers.banner(newerMagus), regenMarkers.end)
	assertFile(t, path, written, 0o755)

	_, err = writeManagedSection(filepath.Dir(path), path, regenMarkers, "magus vcs resolve post-commit\n", hookFile, older)
	var down *stamp.DowngradeError
	require.ErrorAs(t, err, &down)
	assert.Equal(t, stamp.DowngradeError{File: path, Recorded: newerMagus, Writer: olderMagus}, *down)
	for _, want := range []string{path, "magus v0.5.0 (commit 2222222bbbb", "magus v0.4.3-120-g1111111 (commit 1111111aaaa", "magus self update", "at least that new"} {
		assert.Contains(t, err.Error(), want)
	}
	assertFile(t, path, written, 0o755)

	_, err = removeManagedSection(path, regenMarkers, older)
	require.ErrorAs(t, err, &down)
	assertFile(t, path, written, 0o755)

	changed, err := writeManagedSection(filepath.Dir(path), path, regenMarkers, "magus vcs resolve --hook post-commit\n", hookFile, older)
	require.NoError(t, err)
	assert.False(t, changed, "the content an older writer wants is already there, so nothing is replaced")

	changed, err = writeManagedSection(filepath.Dir(path), path, regenMarkers, "magus vcs resolve --hook=v2 post-commit\n", hookFile, newer)
	require.NoError(t, err)
	assert.True(t, changed, "an equal writer replaces")
	assertFile(t, path, fmt.Sprintf(`#!/bin/sh

%smagus vcs resolve --hook=v2 post-commit
%s
`, regenMarkers.banner(newerMagus), regenMarkers.end), 0o755)
}

// An older section, and one written before stamps existed, are replaced and stamped with
// the writer; a section whose content is already current keeps the writer it has.
func TestWriteManagedSectionStampsOlderAndLegacySections(t *testing.T) {
	for name, prior := range map[string]string{
		"older": fmt.Sprintf(`%sold.go merge=magus
%s
`, generatedMarkers.banner(olderMagus), generatedMarkers.end),
		"unstamped": generatedMarkers.section("old.go merge=magus\n"),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".gitattributes")
			require.NoError(t, os.WriteFile(path, []byte("*.png binary\n\n"+prior), 0o644))
			changed, err := writeManagedSection(filepath.Dir(path), path, generatedMarkers, "gen/** merge=magus\n", configFile, stamp.Judge{Self: newerMagus})
			require.NoError(t, err)
			assert.True(t, changed)
			assertFile(t, path, fmt.Sprintf(`*.png binary

%sgen/** merge=magus
%s
`, generatedMarkers.banner(newerMagus), generatedMarkers.end), 0o644)
		})
	}

	path := filepath.Join(t.TempDir(), ".gitattributes")
	current := fmt.Sprintf(`%sgen/** merge=magus
%s
`, generatedMarkers.banner(olderMagus), generatedMarkers.end)
	require.NoError(t, os.WriteFile(path, []byte(current), 0o644))
	changed, err := writeManagedSection(filepath.Dir(path), path, generatedMarkers, "gen/** merge=magus\n", configFile, stamp.Judge{Self: newerMagus})
	require.NoError(t, err)
	assert.False(t, changed, "a stamp alone never rewrites a tracked file")
	assertFile(t, path, current, 0o644)
}

// assertFile checks path's whole content and permission bits.
func assertFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, content, string(got))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, mode, info.Mode().Perm())
}

func TestConfinedToGeneratedSection(t *testing.T) {
	t.Parallel()

	user := "*.png binary\n"
	old := fmt.Sprintf(`%s
%s`, user, generatedMarkers.section("a.go merge=magus\n"))

	tests := []struct {
		name      string
		committed string
		current   string
		want      bool
		wantErr   string
	}{
		{name: "identical", committed: old, current: old, want: false},
		{name: "section body changed", committed: old, current: fmt.Sprintf(`%s
%s`, user, generatedMarkers.section("b.go merge=magus\n")), want: true},
		{name: "section first written", committed: user, current: old, want: true},
		{name: "section dropped", committed: old, current: user, want: true},
		{name: "hand edit beside section", committed: old, current: "*.png binary\n*.jpg binary\n\n" + generatedMarkers.section("a.go merge=magus\n"), want: false},
		{name: "hand edit and section change", committed: old, current: "*.jpg binary\n\n" + generatedMarkers.section("b.go merge=magus\n"), want: false},
		{name: "blank lines moved", committed: old, current: fmt.Sprintf(`%s%s

`, user, generatedMarkers.section("a.go merge=magus\n")), want: true},
		{name: "crlf working copy", committed: old, current: strings.ReplaceAll(fmt.Sprintf(`%s
%s`, user, generatedMarkers.section("b.go merge=magus\n")), "\n", "\r\n"), want: true},
		{name: "torn committed section", committed: tornBanner + "\n", current: old, wantErr: "committed: line 1"},
		{name: "torn working section", committed: old, current: tornBanner + "\n", wantErr: "working copy: line 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ConfinedToGeneratedSection(tt.committed, tt.current)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// A managed config symlink is bounded by the repository it configures, as every other
// workspace file lookup is (file.Within): a link to elsewhere in the repository is
// followed, one leaving it is refused.
func TestWriteManagedSectionRefusesAConfigSymlinkLeavingTheRepository(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".hg"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(repo, "shared"), 0o755))
	outsideDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	outside := filepath.Join(outsideDir, "bashrc")
	require.NoError(t, os.WriteFile(outside, []byte("export A=1\n"), 0o644))
	link := filepath.Join(repo, ".hg", "hgrc")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	changed, err := writeManagedSection(repo, link, refreshMarkers, "[hooks]\n", configFile, stamp.Judge{})
	require.ErrorIs(t, err, file.ErrOutsideRoot)
	assert.Contains(t, err.Error(), link)
	assert.Contains(t, err.Error(), outside)
	assert.False(t, changed)
	assertFile(t, outside, "export A=1\n", 0o644)

	inside := filepath.Join(repo, "shared", "hgrc")
	require.NoError(t, os.WriteFile(inside, []byte("[ui]\n"), 0o644))
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(inside, link))
	changed, err = writeManagedSection(repo, link, refreshMarkers, "[hooks]\n", configFile, stamp.Judge{})
	require.NoError(t, err, "a link outside .hg but inside the repository is followed")
	assert.True(t, changed)
	assertFile(t, inside, "[ui]\n\n"+refreshMarkers.section("[hooks]\n"), 0o644)
}
