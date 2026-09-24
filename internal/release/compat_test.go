package release

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/libs/diagnostics"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func TestBumpString(t *testing.T) {
	assert.Equal(t, []string{"patch", "minor", "major", "Bump(7)"},
		[]string{BumpPatch.String(), BumpMinor.String(), BumpMajor.String(), Bump(7).String()})
}

// TestReportAdd_RecordsTheRootCauseOfEachBranch pins the walk: every joined branch
// counts, the innermost code wins over a wrapper's, a code wrapping a plain error
// keeps its own code, and a leaf reached only through As still counts.
func TestReportAdd_RecordsTheRootCauseOfEachBranch(t *testing.T) {
	missing := types.DiagnosticErrorf(types.MagusNotImported, "a/magusfile.buzz")
	inner := &diagnostics.Error{Code: "BZZ1002", Msg: "undefined type"}
	stale := types.WrapDiagnostic(types.WorkspaceNeedsNewerMagus, inner, "stale")
	ownCode := types.WrapDiagnostic(types.SpellShadowed, errors.New("plain cause"), "shadow")
	err := errors.Join(
		fmt.Errorf("magus: a: %w", missing),
		fmt.Errorf("magus: b: %w", missing),
		stale,
		ownCode,
		fmt.Errorf("magus: c: %w", errors.New("unexpected '}'")),
		asOnly{},
	)

	var r CompatReport
	r.add(err)
	assert.Equal(t, CompatReport{
		Codes: []diagnostics.Code{"BZZ1002", "BZZ9999", types.SpellShadowed, types.MagusNotImported, types.MagusNotImported},
		// The outer text survives, since it names the project the leaf is about.
		Uncoded: []string{"magus: c: unexpected '}'"},
	}, r.sorted())
}

// asOnly mimics the Buzz checker's error: no Unwrap, a code only through As.
type asOnly struct{}

func (asOnly) Error() string { return "checker" }

func (asOnly) As(target any) bool {
	p, ok := target.(**diagnostics.Error)
	if ok {
		*p = &diagnostics.Error{Code: "BZZ9999"}
	}
	return ok
}

func TestJudge(t *testing.T) {
	codes := []diagnostics.Code{types.MagusNotImported, types.MagusNotImported, types.SpellShadowed}
	for _, tc := range []struct {
		name      string
		report    CompatReport
		bump      Bump
		announced []diagnostics.Code
		want      string
	}{
		{name: "clean patch", report: CompatReport{Base: "v1.0.0"}, bump: BumpPatch},
		{
			name: "patch refuses any code", report: CompatReport{Base: "v1.0.0", Codes: codes}, bump: BumpPatch,
			announced: codes,
			want:      "release: a patch release must load v1.0.0, but it fails:\n  MGS1002 x1\n  MGS1039 x2",
		},
		{
			name: "minor allows announced codes", report: CompatReport{Base: "v1.0.0", Codes: codes}, bump: BumpMinor,
			announced: []diagnostics.Code{types.MagusNotImported, types.SpellShadowed},
		},
		{
			name: "minor refuses an unannounced code", report: CompatReport{Base: "v1.0.0", Codes: codes}, bump: BumpMinor,
			announced: []diagnostics.Code{types.MagusNotImported},
			want:      "release: a minor release must load v1.0.0, but it fails:\n  MGS1002 x1",
		},
		{
			name: "minor refuses uncoded", report: CompatReport{Base: "v1.0.0", Uncoded: []string{"boom"}}, bump: BumpMinor,
			want: "release: a minor release must load v1.0.0, but it fails:\n  uncoded: boom",
		},
		{
			name: "major never refuses", report: CompatReport{Base: "v1.0.0", Codes: codes, Uncoded: []string{"boom"}},
			bump: BumpMajor,
		},
		{name: "unknown bump", report: CompatReport{Base: "v1.0.0"}, bump: Bump(9), want: "release: unknown bump Bump(9)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.report.Judge(tc.bump, tc.announced)
			if tc.want == "" {
				assert.NoError(t, err)
				return
			}
			assert.EqualError(t, err, tc.want)
		})
	}
}

// TestCheckCompat_LoadsTheTaggedTree runs the whole path against a real tag: the
// working tree has moved on and loads cleanly, so every finding must come from the
// archived revision, and both broken projects and the broken spell must be reported.
func TestCheckCompat_LoadsTheTaggedTree(t *testing.T) {
	testkit.Isolate(t)
	repo := t.TempDir()
	broken := "magus.project({});\n"
	write(t, repo, "magus.yaml", "")
	write(t, repo, "a/magusfile.buzz", broken)
	write(t, repo, "b/magusfile.buzz", broken)
	// Nothing imports this spell, so only the spell walk can find it.
	write(t, repo, "spells/fixture/spell.buzz", `export fun mgs_getName() > str { return "fixture"; }

fun unused() > void { magus.project({}); }
`)
	// A plain library is not a spell, and not a failure.
	write(t, repo, "spells/lib/spell.buzz", "export fun helper() > int { return 1; }\n")
	git(t, repo, "init", "-q")
	git(t, repo, "add", ".")
	git(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "base")
	git(t, repo, "tag", "v1.0.0")
	fixed := "import \"magus\";\n\n" + broken
	write(t, repo, "a/magusfile.buzz", fixed)
	write(t, repo, "b/magusfile.buzz", fixed)

	got, err := CheckCompat(t.Context(), repo, "v1.0.0")
	require.NoError(t, err)
	assert.Equal(t, CompatReport{
		Base:  "v1.0.0",
		Codes: []diagnostics.Code{types.MagusNotImported, types.MagusNotImported, types.MagusNotImported},
	}, got)
}

func TestCheckCompat_UnknownTagIsAnError(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	_, err := CheckCompat(t.Context(), repo, "v9.9.9")
	assert.ErrorContains(t, err, "release: export v9.9.9")
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoError(t, err, "%s", out)
}
