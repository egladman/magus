package vcs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testBegin = "# BEGIN magus-generated"
	testEnd   = "# END magus-generated"
)

func section(body string) string {
	return testBegin + " - do not edit this section manually\n" + body + testEnd + "\n"
}

func TestReplaceManagedSection(t *testing.T) {
	want := section("a.go merge=magus\n")

	tests := []struct {
		name string
		text string
		want string
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
			text: section("old.go merge=magus\n"),
			want: want,
		},
		{
			name: "surrounding hand-written lines survive on both sides",
			text: "*.png binary\n\n" + section("old.go merge=magus\n") + "\n*.jpg binary\n",
			want: "*.png binary\n\n" + want + "\n*.jpg binary\n",
		},
		{
			// The banner wording changed once (an em-dash went away). Matching begin as
			// a prefix keeps the old section findable instead of stranding it above a
			// freshly appended copy.
			name: "section written with the older banner wording is replaced, not duplicated",
			text: testBegin + " — do not edit this section manually\nold.go merge=magus\n" + testEnd + "\n",
			want: want,
		},
		{
			// The regression this was rewritten for: an end marker found by whole-text
			// scan matched the FIRST section's end, so a begin further down failed the
			// ordering test and appended instead of replacing, once per invocation.
			name: "accumulated duplicate sections collapse to one",
			text: section("first.go merge=magus\n") + "\n" +
				section("second.go merge=magus\n") + "\n" +
				section("third.go merge=magus\n"),
			want: want,
		},
		{
			name: "duplicates collapse without eating hand-written lines between them",
			text: section("first.go merge=magus\n") + "\nkeep-me\n" + section("second.go merge=magus\n"),
			want: want + "\nkeep-me\n",
		},
		{
			// A truncated or hand-mangled file must not have its remainder swallowed.
			name: "unterminated begin is left alone and the section is appended",
			text: "*.png binary\n" + testBegin + " - do not edit this section manually\ndangling\n",
			want: "*.png binary\n" + testBegin + " - do not edit this section manually\ndangling\n\n" + want,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := replaceManagedSection(tt.text, want, testBegin, testEnd)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestReplaceManagedSectionIdempotent pins what the accumulation bug broke: applying the
// same section repeatedly converges after the first write.
func TestReplaceManagedSectionIdempotent(t *testing.T) {
	want := section("a.go merge=magus\n")
	text := "*.png binary\n"
	for range 5 {
		text = replaceManagedSection(text, want, testBegin, testEnd)
	}
	assert.Equal(t, "*.png binary\n\n"+want, text)
	assert.Equal(t, 1, strings.Count(text, testBegin), "exactly one managed section")
}

// The two EnsureMergeDriver tests below live here rather than in git_test.go beside the
// method they call, and reach across to git_test.go's gitInitRepo helper to do it. Their
// subject is the managed-section contract this file owns - written once, replaced not
// appended - which the pure replaceManagedSection tests above can only prove in memory.
// Splitting them from those would put the two halves of one invariant in two files.
//
// TestEnsureMergeDriverIdempotent drives EnsureMergeDriver end to end against a real repo,
// which is where the accumulation actually bit: every magus invocation runs it, so a section
// appended rather than replaced grew .gitattributes by a block per command.
//
// EnsureMergeDriver builds its own git commands and does NOT route them through gitEnv, so
// this pins the ambient git environment itself. GIT_DIR is the one that matters: git exports
// it inside every hook and every `rebase --exec`, so running the suite from a pre-commit hook
// sent `git config merge.magus.driver` into whatever repo was being committed to - the test
// passing all the while, because it never looked. Pointing the variables at the fixture makes
// the target explicit rather than merely unset, and the config assertion below is what proves
// the write landed here.
func TestEnsureMergeDriverIdempotent(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("GIT_DIR", filepath.Join(repo, ".git"))
	t.Setenv("GIT_WORK_TREE", repo)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	gitInitRepo(t, repo, map[string]string{"magus.yaml": "version: 1\n"})
	outputGlobs := []string{"gen/**", "docs/gen/**"}
	attrsPath := filepath.Join(repo, ".gitattributes")

	changed, err := gitVCS{}.EnsureMergeDriver(t.Context(), repo, outputGlobs)
	require.NoError(t, err)
	assert.True(t, changed, "first call installs the section")

	changed, err = gitVCS{}.EnsureMergeDriver(t.Context(), repo, outputGlobs)
	require.NoError(t, err)
	assert.False(t, changed, "second call has nothing to do")

	// Assert the CONTENT, not just that two reads agree: with changed==false a write is
	// impossible, so comparing the two reads can only ever restate the line above. An
	// EnsureMergeDriver that wrote an empty section would satisfy that and fail a user.
	attrs, err := os.ReadFile(attrsPath)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(attrs), gitAttrsBegin), "exactly one managed section")
	// Whole lines, not substrings: "docs/gen/**" ends with "gen/**", so a substring count
	// finds the narrower glob inside the wider one and reports two.
	lines := strings.Split(string(attrs), "\n")
	for _, glob := range outputGlobs {
		want := glob + " merge=magus linguist-generated"
		n := 0
		for _, line := range lines {
			if line == want {
				n++
			}
		}
		assert.Equal(t, 1, n, "section names %s exactly once", glob)
	}

	// The registration is half of what EnsureMergeDriver promises, and reading it back from
	// the fixture is also what would catch the config escaping into another repository.
	assert.Contains(t, gitConfigValue(t, repo, "merge.magus.driver"), gitDriverArgs,
		"driver registered in the fixture repo")

	// Re-wiring is the other reason EnsureMergeDriver exists: a project that declares an
	// output later must be added, not left frozen at the shape the workspace had on the day
	// init ran. The steady state above cannot show that.
	changed, err = gitVCS{}.EnsureMergeDriver(t.Context(), repo, []string{"gen/**", "dist/**"})
	require.NoError(t, err)
	assert.True(t, changed, "a changed glob set re-wires")
	attrs, err = os.ReadFile(attrsPath)
	require.NoError(t, err)
	assert.Contains(t, string(attrs), "dist/** merge=magus linguist-generated", "new glob added")
	assert.NotContains(t, string(attrs), "docs/gen/**", "dropped glob removed, not accumulated")
	assert.Equal(t, 1, strings.Count(string(attrs), gitAttrsBegin), "still exactly one section")
}

// TestEnsureMergeDriverLeavesACRLFWorktreeClean pins the fix for the v0.4.1 windows release,
// which stamped every artifact `v0.4.1-dirty` while the other four platforms were clean.
//
// .gitattributes is the one TRACKED file magus rewrites on every workspace load. Git for
// Windows ships core.autocrlf=true and this repository declares no eol attribute, so
// checkout smudges the LF blob to CRLF on disk; writing the managed section back as LF then
// makes git report the file modified, and `git describe --dirty` says so.
//
// core.autocrlf is set on the fixture rather than mocked, so this runs the real smudge on
// any host: the checkout below produces CRLF everywhere, which is what makes the case
// reproducible off Windows. The final describe is the assertion that matters, because it is
// the exact call version() makes.
func TestEnsureMergeDriverLeavesACRLFWorktreeClean(t *testing.T) {
	repo := t.TempDir()
	t.Setenv("GIT_DIR", filepath.Join(repo, ".git"))
	t.Setenv("GIT_WORK_TREE", repo)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	outputGlobs := []string{"gen/**", "docs/gen/**"}

	// Commit the section as an LF blob, the way every non-Windows contributor does.
	_, wanted := gitVCS{}.gitAttrsState(repo, outputGlobs)
	gitInitRepo(t, repo, map[string]string{"magus.yaml": "version: 1\n", ".gitattributes": wanted})
	gitRun(t, repo, "config", "core.autocrlf", "true")
	gitRun(t, repo, "tag", "v0.4.1")

	// Re-checkout under autocrlf: this is the Windows runner's starting state.
	require.NoError(t, os.Remove(filepath.Join(repo, ".gitattributes")))
	gitRun(t, repo, "checkout", "--", ".gitattributes")
	onDisk, err := os.ReadFile(filepath.Join(repo, ".gitattributes"))
	require.NoError(t, err)
	require.Contains(t, string(onDisk), "\r\n", "the fixture reproduces the CRLF smudge")

	// changed is true here for a reason unrelated to the tracked file: a fresh clone has no
	// merge.magus.driver registered, and that write lands in .git/config.
	_, err = gitVCS{}.EnsureMergeDriver(t.Context(), repo, outputGlobs)
	require.NoError(t, err)

	after, err := os.ReadFile(filepath.Join(repo, ".gitattributes"))
	require.NoError(t, err)
	assert.Equal(t, string(onDisk), string(after),
		"the section is rewritten with the line ending the file already uses, so the bytes do not move")

	described, err := gitVCS{}.Describe(t.Context(), repo)
	require.NoError(t, err)
	assert.Equal(t, "v0.4.1", described, "a CRLF worktree is not dirt; v0.4.1 shipped as v0.4.1-dirty because it was")

	changed, err := gitVCS{}.EnsureMergeDriver(t.Context(), repo, outputGlobs)
	require.NoError(t, err)
	assert.False(t, changed, "and the steady state stays quiet rather than rewriting every load")
}

// TestEnsureMergeDriverIgnoresAmbientGitDir pins the escape gitEnviron exists to stop.
//
// GIT_DIR overrides both -C and cmd.Dir, and git exports it into every hook and
// `rebase --exec`. EnsureMergeDriver runs on workspace load, so before the scrub, a magus
// command invoked from a pre-commit hook registered the merge driver in whatever repository
// was being committed to - succeeding quietly, because nothing ever read the value back.
func TestEnsureMergeDriverIgnoresAmbientGitDir(t *testing.T) {
	repo := t.TempDir()
	gitInitRepo(t, repo, map[string]string{"magus.yaml": "version: 1\n"})
	bystander := t.TempDir()
	gitInitRepo(t, bystander, map[string]string{"magus.yaml": "version: 1\n"})

	// Point the ambient environment at the bystander, exactly as a git hook would.
	t.Setenv("GIT_DIR", filepath.Join(bystander, ".git"))
	t.Setenv("GIT_WORK_TREE", bystander)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)

	_, err := gitVCS{}.EnsureMergeDriver(t.Context(), repo, []string{"gen/**"})
	require.NoError(t, err)

	assert.Contains(t, gitConfigValue(t, repo, "merge.magus.driver"), gitDriverArgs,
		"the named repo is the one configured")
	assert.Empty(t, gitConfigValue(t, bystander, "merge.magus.driver"),
		"the repo named only by ambient GIT_DIR must be left alone")
}

// gitConfigValue reads one local config key, returning "" when it is unset. It reads with a
// scrubbed environment for the same reason the production path writes with one: an ambient
// GIT_DIR would otherwise answer about a different repository than the caller named.
func gitConfigValue(t *testing.T, repo, key string) string {
	t.Helper()
	cmd := gitExec(t.Context(), "-C", repo, "config", "--get", key)
	out, err := cmd.Output()
	if err != nil {
		return "" // git exits 1 for an unset key; any real failure surfaces as an empty value
	}
	return strings.TrimSpace(string(out))
}
