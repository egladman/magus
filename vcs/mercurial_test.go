package vcs

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/stamp"
	"github.com/egladman/magus/types"
)

// The three hg-family sections share one config file and never touch the user's own
// lines or each other; each rewrite of a current section is a no-op.
func TestWriteHgSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hgrc")
	require.NoError(t, os.WriteFile(path, []byte("[ui]\nusername = me\n"), 0o644))

	for _, write := range []func() (bool, error){
		func() (bool, error) {
			return writeHgMergeDriverSection(filepath.Dir(path), path, types.MergeDriverGlobs{Outputs: []string{"gen/**", "dist/**"}}, stamp.Judge{})
		},
		func() (bool, error) {
			return writeHgRefreshSection(filepath.Dir(path), path, "hg", "magus job run sync-graph", stamp.Judge{})
		},
		func() (bool, error) {
			return writeHgDriftSection(filepath.Dir(path), path, "hg", "magus job run check-drift", stamp.Judge{})
		},
	} {
		changed, err := write()
		require.NoError(t, err)
		assert.True(t, changed, "the first write of each section changes the file")
	}

	want := fmt.Sprintf(`[ui]
username = me

%s
%s
%s`, generatedMarkers.section("[merge-patterns]\n"+
		"glob:gen/** = magus\n"+
		"glob:dist/** = magus\n"+
		"\n[merge-tools]\n"+
		"magus.executable = magus\n"+
		"magus.args = vcs merge-driver $base $local $other 0 $local\n"+
		"magus.premerge = False\n"+
		"magus.gui = False\n"+
		"magus.disabled = True\n"), refreshMarkers.section("[hooks]\n"+
		"update.magus-refresh = magus job run sync-graph >/dev/null 2>&1 || true\n"), driftMarkers.section("[hooks]\n"+
		"commit.magus-drift-notice = magus job run check-drift >/dev/null 2>&1 || true\n"+
		"outgoing.magus-drift-notice = magus job run check-drift >/dev/null 2>&1 || true\n"))
	assertFile(t, path, want, 0o644)

	changed, err := writeHgRefreshSection(filepath.Dir(path), path, "hg", "magus job run sync-graph", stamp.Judge{})
	require.NoError(t, err)
	assert.False(t, changed)
	changed, err = writeHgMergeDriverSection(filepath.Dir(path), path, types.MergeDriverGlobs{Outputs: []string{"gen/**"}}, stamp.Judge{})
	require.NoError(t, err)
	assert.True(t, changed, "a dropped glob rewrites the section in place")
	present, err := managedSectionPresent(path, generatedMarkers)
	require.NoError(t, err)
	assert.True(t, present)
}

// An auto-resolve glob routes to the same tool as an output, once even when an output
// glob names it too, and a second write of the same globs changes nothing.
func TestWriteHgMergeDriverSectionRoutesAutoResolveGlobs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hgrc")
	globs := types.MergeDriverGlobs{Outputs: []string{"gen/**"}, AutoResolve: []string{"CHANGELOG.md", "gen/**", "docs/**/*.md"}}
	changed, err := writeHgMergeDriverSection(filepath.Dir(path), path, globs, stamp.Judge{})
	require.NoError(t, err)
	assert.True(t, changed)
	assertFile(t, path, generatedMarkers.section("[merge-patterns]\n"+
		"glob:CHANGELOG.md = magus\n"+
		"glob:gen/** = magus\n"+
		"glob:docs/**/*.md = magus\n"+
		"\n[merge-tools]\n"+
		"magus.executable = magus\n"+
		"magus.args = vcs merge-driver $base $local $other 0 $local\n"+
		"magus.premerge = False\n"+
		"magus.gui = False\n"+
		"magus.disabled = True\n"), 0o644)

	changed, err = writeHgMergeDriverSection(filepath.Dir(path), path, globs, stamp.Judge{})
	require.NoError(t, err)
	assert.False(t, changed, "installing twice is idempotent")
}

// TestHgMergeToolRoutesOnlyWhatOutputsClaim pins, on a real hg, that the tool gets only the
// declared outputs. A hand file among them (carved out) keeps Mercurial's :merge because
// merge-patterns takes its first match. And a source file must not reach magus through
// the fallback: with no ui.merge set, filemerge._picktool ranks every [merge-tools] entry
// by priority, and picked magus for every conflicted file until the tool was disabled.
func TestHgMergeToolRoutesOnlyWhatOutputsClaim(t *testing.T) {
	if _, err := exec.LookPath("hg"); err != nil {
		t.Skip("hg not available")
	}
	dir := t.TempDir()
	hgInitRepo(t, dir, map[string]string{"a.txt": "one\n", "gen/x.go": "x\n", "gen/runtime.go": "hand\n"})
	_, err := writeHgMergeDriverSection(dir, filepath.Join(dir, ".hg", "hgrc"),
		types.MergeDriverGlobs{Outputs: []string{"gen/*.go"}, Carved: []string{"gen/runtime.go"}}, stamp.Judge{})
	require.NoError(t, err)
	// The tool has to be found for a pattern to pick it; the test binary stands in for magus.
	self, err := os.Executable()
	require.NoError(t, err)

	cmd := exec.CommandContext(t.Context(), "hg", "--config", "merge-tools.magus.executable="+self,
		"debugpickmergetool", "a.txt", "gen/runtime.go", "gen/x.go")
	cmd.Dir = dir
	// No user or system config, so no ui.merge a developer set can mask the fallback.
	cmd.Env = append(os.Environ(), "HGRCPATH=", "HGPLAIN=1")
	out, err := cmd.Output()
	require.NoError(t, err)

	assert.Equal(t, "a.txt = :merge\ngen/runtime.go = :merge\ngen/x.go = magus\n", string(out))
}

// The dot-dir's files answer without starting hg or sl, from the checkout root or under
// it; only a checkout with no readable dirstate asks the tool.
func TestHgOperationInProgressReadsTheDotDir(t *testing.T) {
	asked := func() (bool, error) {
		t.Error("the tool was asked")
		return false, nil
	}
	parents := func(p2 byte) []byte {
		b := make([]byte, 40)
		b[0] = 1
		b[39] = p2
		return b
	}
	for _, tt := range []struct {
		name  string
		files map[string][]byte
		want  string
	}{
		{"clean", map[string][]byte{"dirstate": parents(0)}, ""},
		{"histedit", map[string][]byte{"dirstate": parents(0), "histedit-state": nil}, types.OperationRebase},
		{"rebase over a merge state", map[string][]byte{"dirstate": parents(0), "rebasestate": nil, "merge/state2": nil}, types.OperationRebase},
		{"graft", map[string][]byte{"dirstate": parents(0), "graftstate": nil}, types.OperationCherryPick},
		{"conflicted update", map[string][]byte{"dirstate": parents(0), "merge/state": nil}, types.OperationMerge},
		{"merge that merged no file", map[string][]byte{"dirstate": parents(1)}, types.OperationMerge},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for name, body := range tt.files {
				writeRepoFile(t, root, ".sl/"+name, string(body))
			}
			require.NoError(t, os.Mkdir(filepath.Join(root, "sub"), 0o755))
			for _, at := range []string{root, filepath.Join(root, "sub")} {
				op, err := hgOperationInProgress(at, ".sl", asked)
				require.NoError(t, err)
				assert.Equal(t, tt.want, op, at)
			}
		})
	}

	op, err := hgOperationInProgress(t.TempDir(), ".hg", func() (bool, error) { return true, nil })
	require.NoError(t, err)
	assert.Equal(t, types.OperationMerge, op, "no dot-dir leaves the answer to the tool")
}

func TestHgHookValueSkipsATrackedRelativeBinary(t *testing.T) {
	if _, err := exec.LookPath("hg"); err != nil {
		t.Skip("hg not available")
	}
	value := hgHookValue("hg", "./magus job run sync-graph")
	for _, tc := range []struct {
		name    string
		files   map[string]string
		runs    bool
		message string
	}{
		{"untracked", map[string]string{"a.txt": "a\n"}, true, ""},
		{"tracked", map[string]string{"a.txt": "a\n", "magus": ""}, false, "this branch commits ./magus, so the hook will not run it; run 'magus job run sync-graph' with your own build"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			hgInitRepo(t, dir, tc.files)
			sentinel := sentinelMagus(t, dir)
			cmd := exec.CommandContext(t.Context(), "sh", "-c", value)
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "HGRCPATH=", "HGPLAIN=1")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			require.NoError(t, cmd.Run(), stderr.String())
			if tc.runs {
				assert.FileExists(t, sentinel)
			} else {
				assert.NoFileExists(t, sentinel, "the committed binary ran")
			}
			assert.Contains(t, stderr.String(), tc.message)
		})
	}
}

func TestReadHgFamilyHooks(t *testing.T) {
	for _, tc := range []struct {
		dot, prog, config string
		refresh, drift    func(string) (string, bool, error)
	}{
		{".hg", "hg", "hgrc", ReadHgRefreshHook, ReadHgDriftHook},
		{".sl", "sl", "config", ReadSaplingRefreshHook, ReadSaplingDriftHook},
	} {
		t.Run(tc.prog, func(t *testing.T) {
			_, _, err := tc.refresh(t.TempDir())
			require.ErrorIs(t, err, types.ErrVCSUnsupported)
			_, _, err = tc.drift(t.TempDir())
			require.ErrorIs(t, err, types.ErrVCSUnsupported)

			root := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(root, tc.dot), 0o755))
			_, ok, err := tc.refresh(root)
			require.NoError(t, err)
			assert.False(t, ok, "nothing installed yet")

			path := filepath.Join(root, tc.dot, tc.config)
			_, err = writeHgRefreshSection(root, path, tc.prog, "./magus job run sync-graph", stamp.Judge{})
			require.NoError(t, err)
			_, err = writeHgDriftSection(root, path, tc.prog, "magus job run check-drift", stamp.Judge{})
			require.NoError(t, err)

			got, ok, err := tc.refresh(root)
			require.NoError(t, err)
			assert.True(t, ok)
			assert.Equal(t, "./magus job run sync-graph", got)
			got, ok, err = tc.drift(root)
			require.NoError(t, err)
			assert.True(t, ok)
			assert.Equal(t, "magus job run check-drift", got)
		})
	}
}
