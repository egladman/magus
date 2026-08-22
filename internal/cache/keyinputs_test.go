package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMaskKeyInputsHidesEnvValues: env values are replaced by a stable digest, every
// other class is untouched, and an unset marker keeps its shape. The digest must
// still change when the value does, or the diff could not name the drifted variable.
func TestMaskKeyInputsHidesEnvValues(t *testing.T) {
	in := []string{
		"keyVersion:3",
		"src:pkg/a/main.go:abc:0",
		"env:TOKEN=sk-live-super-secret",
		"env:EMPTY=",
		"env:MISSING:unset",
		"tool:go1.25.0",
	}
	got := MaskKeyInputs(in)

	joined := strings.Join(got, "\n")
	assert.NotContains(t, joined, "sk-live-super-secret", "an env value must never survive masking")
	assert.Equal(t, []string{"keyVersion:3", "src:pkg/a/main.go:abc:0"}, got[:2], "non-env classes pass through")
	assert.Equal(t, "tool:go1.25.0", got[5])
	assert.Equal(t, "env:MISSING:unset", got[4], "an unset marker carries no value to mask")
	assert.Regexp(t, `^env:TOKEN=sha256:[0-9a-f]{12}$`, got[2])
	assert.Regexp(t, `^env:EMPTY=sha256:[0-9a-f]{12}$`, got[3])

	assert.Equal(t, got, MaskKeyInputs(in), "masking is deterministic - two machines agree")
	changed := append([]string(nil), in...)
	changed[2] = "env:TOKEN=sk-live-rotated"
	assert.NotEqual(t, got[2], MaskKeyInputs(changed)[2], "a changed value must change its digest")
}

// TestPersistKeyInputsStoresNoEnvValues: what lands on disk is masked, so a store
// shared or inspected later cannot leak a token that rode an allowlisted env var.
func TestPersistKeyInputsStoresNoEnvValues(t *testing.T) {
	dir := t.TempDir()
	s := NewOutputStore(dir)
	const key = "d00dfeedd00dfeed"
	ref := mustPersist(t, s, key, []byte("ok\n"), OutputDescriptor{Project: "p", Target: "build"})
	require.NoError(t, s.PersistKeyInputs(context.Background(), key, []string{"env:TOKEN=sk-live-super-secret"}))

	raw, err := os.ReadFile(filepath.Join(dir, "outputs", key, keyInputsName))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "sk-live-super-secret", "the raw sidecar must hold no env value")

	got, err := s.KeyInputsByRef(ref)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Regexp(t, `^env:TOKEN=sha256:[0-9a-f]{12}$`, got[0])
}

// TestKeyInputsRoundTripByRef persists key inputs beside a step's attempts and reads
// them back through every ref shape that names the step.
func TestKeyInputsRoundTripByRef(t *testing.T) {
	s := NewOutputStore(t.TempDir())
	const key = "feedfacefeedfacefeedface"
	lines := []string{"keyVersion:3", "projectPath:pkg/a", "src:pkg/a/main.go:abc123:0", "env:GOFLAGS=-trimpath"}

	ref := mustPersist(t, s, key, []byte("ok\n"), OutputDescriptor{Project: "pkg/a", Target: "build"})
	require.NoError(t, s.PersistKeyInputs(context.Background(), key, lines))

	// What comes back is the MASKED form (env values never reach disk); every other
	// class round-trips verbatim.
	want := MaskKeyInputs(lines)
	got, err := s.KeyInputsByRef(ref)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	attempts, err := s.Attempts(ref)
	require.NoError(t, err)
	got, err = s.KeyInputsByRef(attempts[0].Attempt) // an attempt id addresses the same step
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// TestKeyInputsAbsent: a step persisted before key input persistence resolves but has no
// lines - fs.ErrNotExist, distinct from an unresolvable ref.
func TestKeyInputsAbsent(t *testing.T) {
	s := NewOutputStore(t.TempDir())
	ref := mustPersist(t, s, "cafebabecafebabe", []byte("ok\n"), OutputDescriptor{Project: "p"})
	_, err := s.KeyInputsByRef(ref)
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

// TestKeyInputsSidecarInvisibleToAttemptScanners: the key-inputs sidecar must never be
// mistaken for an execution record by the stem-scanning paths.
func TestKeyInputsSidecarInvisibleToAttemptScanners(t *testing.T) {
	s := NewOutputStore(t.TempDir())
	const key = "beefbeefbeefbeef"
	ref := mustPersist(t, s, key, []byte("ok\n"), OutputDescriptor{Project: "p", Target: "build"})
	require.NoError(t, s.PersistKeyInputs(context.Background(), key, []string{"keyVersion:3"}))

	attempts, err := s.Attempts(ref)
	require.NoError(t, err)
	require.Len(t, attempts, 1, "the sidecar is not an attempt")
	assert.Len(t, s.ListDescriptors(), 1, "the sidecar is not a descriptor")
}

// TestClassDigests: lines fold into one digest per label class, first-appearance
// order, and a changed line changes ONLY its class's digest.
func TestClassDigests(t *testing.T) {
	lines := []string{
		"keyVersion:3",
		"projectPath:pkg/a",
		"src:pkg/a/main.go:abc:0",
		"src:pkg/a/util.go:def:0",
		"env:GOFLAGS=-trimpath",
		"tool:go1.25.0",
	}
	d := ClassDigests(lines)
	classes := make([]string, len(d))
	for i, c := range d {
		classes[i] = c.Class
	}
	assert.Equal(t, []string{"keyVersion", "projectPath", "src", "env", "tool"}, classes)
	assert.Equal(t, 2, d[2].Count, "src contributes two lines")

	changed := append([]string(nil), lines...)
	changed[2] = "src:pkg/a/main.go:CHANGED:0"
	d2 := ClassDigests(changed)
	for i := range d {
		if d[i].Class == "src" {
			assert.NotEqual(t, d[i].Digest, d2[i].Digest, "src digest must change")
		} else {
			assert.Equal(t, d[i].Digest, d2[i].Digest, "%s digest must not change", d[i].Class)
		}
	}
}

// TestDiffKeyInputs: a changed line appears under its class as stored-only + live-only;
// matching classes are absent; an added env line is live-only.
func TestDiffKeyInputs(t *testing.T) {
	stored := []string{
		"keyVersion:3",
		"src:pkg/a/main.go:abc:0",
		"env:GOFLAGS=-trimpath",
	}
	live := []string{
		"keyVersion:3",
		"src:pkg/a/main.go:xyz:0",
		"env:GOFLAGS=-trimpath",
		"env:CGO_ENABLED=0",
	}
	diff := DiffKeyInputs(stored, live)
	require.Len(t, diff, 2, "keyVersion matched; src and env disagree")

	assert.Equal(t, KeyInputDiff{
		Class:      "src",
		StoredOnly: []string{"src:pkg/a/main.go:abc:0"},
		LiveOnly:   []string{"src:pkg/a/main.go:xyz:0"},
	}, diff[0])
	assert.Equal(t, KeyInputDiff{
		Class:    "env",
		LiveOnly: []string{"env:CGO_ENABLED=0"},
	}, diff[1])

	assert.Empty(t, DiffKeyInputs(stored, stored), "identical keys diff to nothing")
}

// TestHashStepLinesMatchesHash: the collected lines are the exact pre-hash input -
// re-hashing them reproduces the cache key - and the nil path returns the same key.
func TestHashStepLinesMatchesHash(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	step := makeStep(root)
	step.Target = "build"

	var lines []string
	withLines, err := c.hashStepInputs(context.Background(), &step, &lines)
	require.NoError(t, err)
	require.NotEmpty(t, lines)

	plain, err := c.hashStep(context.Background(), &step)
	require.NoError(t, err)
	assert.Equal(t, plain, withLines, "collecting lines must not change the key")

	// Against the constant, not a literal: this test pins that keyVersion LEADS the
	// key inputs, which is what the diff reader relies on; the value itself is
	// pinned once, by TestHashStep_KeyVersionIsHashed.
	assert.Equal(t, fmt.Sprintf("keyVersion:%d", KeyVersion), lines[0])
	assert.Equal(t, withLines, hashOfLines(lines), "lines are byte-identical to what the hash consumed")
}

// TestRunPersistsKeyInputsAndAgainstDiffNamesTheDrift is Phase 2's acceptance check:
// a real Run persists the key's pre-hash lines beside its output, and diffing a
// LATER live key against them names exactly what drifted - the edited source file
// as a src line, the changed allowlisted env var as an env line.
func TestRunPersistsKeyInputsAndAgainstDiffNamesTheDrift(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	t.Setenv("MAGUS_KEYLINE_PROBE", "before")
	step := makeStep(root)
	step.Target = "build"
	step.EnvAllow = []string{"MAGUS_KEYLINE_PROBE"}

	r, err := c.Run(context.Background(), step, func(ctx context.Context) error { return nil })
	require.NoError(t, err)

	stored, err := c.outputs.KeyInputsByRef(r.Ref)
	require.NoError(t, err, "a run must persist its key inputs beside the output")

	// Drift both inputs, then ask the live key what changed. The live lines are
	// masked exactly as the stored ones were, so the two sides stay comparable.
	writeMain(t, root, "package main // edited")
	t.Setenv("MAGUS_KEYLINE_PROBE", "after")
	_, raw, err := c.StepKey(context.Background(), &step)
	require.NoError(t, err)
	live := MaskKeyInputs(raw)

	diff := DiffKeyInputs(stored, live)
	classes := make(map[string]KeyInputDiff, len(diff))
	for _, d := range diff {
		classes[d.Class] = d
	}
	require.Len(t, diff, 2, "exactly the two drifted classes appear: %+v", diff)

	src := classes["src"]
	require.Len(t, src.StoredOnly, 1)
	require.Len(t, src.LiveOnly, 1)
	assert.Contains(t, src.StoredOnly[0], "src:test/pkg/main.go:", "the diff names the exact edited file")
	assert.Contains(t, src.LiveOnly[0], "src:test/pkg/main.go:")
	assert.NotEqual(t, src.StoredOnly[0], src.LiveOnly[0], "old and new content hashes both shown")

	env := classes["env"]
	require.Len(t, env.StoredOnly, 1)
	require.Len(t, env.LiveOnly, 1)
	assert.Contains(t, env.StoredOnly[0], "env:MAGUS_KEYLINE_PROBE=", "the diff names the exact variable")
	assert.Contains(t, env.LiveOnly[0], "env:MAGUS_KEYLINE_PROBE=")
	assert.NotEqual(t, env.StoredOnly[0], env.LiveOnly[0], "its value digest changed")
	assert.NotContains(t, strings.Join(append(env.StoredOnly, env.LiveOnly...), " "), "before", "values stay masked on both sides")
}

// TestCompareKeyInputsIdentical: nothing differs when both sides hashed the same
// inputs, and First stays nil so a caller can branch on it alone.
func TestCompareKeyInputsIdentical(t *testing.T) {
	t.Parallel()
	lines := []string{
		"keyVersion:3",
		"target:build",
		"src:svc/main.go:abc123:0",
		"env:GOFLAGS=sha256:0123456789ab",
		"tool:go:version=1.25.0",
	}
	got := CompareKeyInputs(lines, append([]string(nil), lines...))
	assert.Equal(t, 0, got.Differences)
	assert.Nil(t, got.First)
}

// TestCompareKeyInputsPairsInputsByIdentity: a value that moved must read as ONE input
// that changed, per class, rather than as a line removed and a line added.
func TestCompareKeyInputsPairsInputsByIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		recorded, live []string
		want           KeyInputChange
	}{
		{
			name:     "source content",
			recorded: []string{"src:svc/main.go:abc123:0"},
			live:     []string{"src:svc/main.go:def456:0"},
			want:     KeyInputChange{Class: "src", Input: "src:svc/main.go", Recorded: "abc123:0", Live: "def456:0"},
		},
		{
			name:     "source exec bit",
			recorded: []string{"src:svc/run.sh:abc123:0"},
			live:     []string{"src:svc/run.sh:abc123:1"},
			want:     KeyInputChange{Class: "src", Input: "src:svc/run.sh", Recorded: "abc123:0", Live: "abc123:1"},
		},
		{
			name:     "env value",
			recorded: []string{"env:TOKEN=sha256:aaaaaaaaaaaa"},
			live:     []string{"env:TOKEN=sha256:bbbbbbbbbbbb"},
			want:     KeyInputChange{Class: "env", Input: "env:TOKEN", Recorded: "sha256:aaaaaaaaaaaa", Live: "sha256:bbbbbbbbbbbb"},
		},
		{
			name:     "env became set",
			recorded: []string{"env:TOKEN:unset"},
			live:     []string{"env:TOKEN=sha256:bbbbbbbbbbbb"},
			want:     KeyInputChange{Class: "env", Input: "env:TOKEN", Recorded: "unset", Live: "sha256:bbbbbbbbbbbb"},
		},
		{
			name:     "tool version",
			recorded: []string{"tool:go:version=1.25.0"},
			live:     []string{"tool:go:version=1.26.0"},
			want:     KeyInputChange{Class: "tool", Input: "tool:go:version", Recorded: "1.25.0", Live: "1.26.0"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := CompareKeyInputs(tc.recorded, tc.live)
			assert.Equal(t, 1, got.Differences, "one input moved, so one difference")
			require.NotNil(t, got.First)
			assert.Equal(t, tc.want, *got.First)
		})
	}
}

// TestCompareKeyInputsReportsLiveOrderFirst: "first" is the earliest class
// hashStepInputs writes, so a changed target line is named ahead of the source hashes
// it explains.
func TestCompareKeyInputsReportsLiveOrderFirst(t *testing.T) {
	t.Parallel()
	recorded := []string{"target:build", "src:svc/a.go:aaa:0", "src:svc/b.go:bbb:0", "dep:libs/x"}
	live := []string{"target:build:rw", "src:svc/a.go:zzz:0", "src:svc/b.go:bbb:0"}

	got := CompareKeyInputs(recorded, live)
	require.NotNil(t, got.First)
	assert.Equal(t, "target:build:rw", got.First.Input, "the live key's earliest differing line leads")
	assert.True(t, got.First.RecordedAbsent)
	// The two target lines, the edited source, and the dropped dep.
	assert.Equal(t, 4, got.Differences)
}

// TestCompareKeyInputsMarksAbsence: a class with no value slot can only appear or
// disappear, and the *Absent flags are what say which - an empty value does not.
func TestCompareKeyInputsMarksAbsence(t *testing.T) {
	t.Parallel()
	got := CompareKeyInputs(nil, []string{"charm:rw"})
	require.NotNil(t, got.First)
	assert.Equal(t, KeyInputChange{Class: "charm", Input: "charm:rw", RecordedAbsent: true}, *got.First)

	got = CompareKeyInputs([]string{"charm:rw"}, nil)
	require.NotNil(t, got.First)
	assert.Equal(t, KeyInputChange{Class: "charm", Input: "charm:rw", LiveAbsent: true}, *got.First)
}

// hashOfLines re-derives the cache key from collected key inputs, independently of
// hashStep's buffer plumbing.
func hashOfLines(lines []string) string {
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}
