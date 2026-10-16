package job

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const configMagusfile = `import "magus";

magus\project({
    "name": "demo",
    "targets": {
        "test": {"timeout": "5m"},
    },
});

export fun build(ctx: magus\Context, args: [str]) > void {
}
`

// configFiles is a workspace of two projects under the buzz and yaml diff drivers, with a
// Buzz file no project loads and a magus.yaml.
var configFiles = map[string]string{
	".gitattributes":          "*.buzz diff=buzz\n*.yaml diff=yaml\n",
	"magus.yaml":              "jobs:\n  stale_after: 2h\n",
	"magusfile.buzz":          configMagusfile,
	"libs/lib/magusfile.buzz": "import \"magus\";\n\nmagus\\project({\n    \"name\": \"lib\",\n});\n",
	"notes.buzz":              "fun note() > void {\n}\n",
}

func TestRefuseWholeFileConfigClaims(t *testing.T) {
	t.Parallel()

	root := testkit.NewRepo(t, configFiles).Root()
	for _, tc := range []struct {
		name  string
		paths []string
		want  string
	}{
		{"the root magusfile whole", []string{"magusfile.buzz"},
			"instead, such as `magusfile.buzz#project.name`, `magusfile.buzz#project.targets.test`, `magusfile.buzz#build`"},
		{"a project's magusfile whole", []string{"libs/lib/magusfile.buzz"}, "\"libs/lib/magusfile.buzz\" holds a project's configuration whole"},
		{"a key claim", []string{"magusfile.buzz#project.targets.test"}, ""},
		{"a key not written yet", []string{"magusfile.buzz#project.targets.lint"}, ""},
		{"a pattern", []string{"**/*.buzz"}, ""},
		{"magus.yaml whole", []string{"magus.yaml"}, "instead, such as `magus.yaml#jobs.stale_after`"},
		{"a magus.yaml key claim", []string{"magus.yaml#jobs.stale_after"}, ""},
		{"a Buzz file no project loads", []string{"notes.buzz"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fork := NewStore(tmpLoc(t, root))
			_, forkErr := ForkMerge(t.Context(), fork, "wave/job", func(u *types.Job) {
				u.Check, u.State, u.WritePaths = forkCheck(), types.StateDeclared, tc.paths
			}, config.Jobs{}, nil)
			apply := NewStore(tmpLoc(t, root))
			_, applyErr := Apply(t.Context(), apply, []types.Declaration{{ID: "wave/job", WritePaths: tc.paths, Check: forkCheck()}}, config.Jobs{}, nil, nil, false)
			for door, err := range map[string]error{"fork": forkErr, "apply": applyErr} {
				if tc.want == "" {
					require.NoError(t, err, door)
					continue
				}
				require.ErrorIs(t, err, types.WritePathConfigWholeFile, door)
				assert.Contains(t, err.Error(), tc.want, door)
			}
			if tc.want != "" {
				for _, s := range []*Store{fork, apply} {
					rows, err := s.List()
					require.NoError(t, err)
					assert.Empty(t, rows, "a refused fork writes no row")
				}
			}
		})
	}

	t.Run("an apply adding it to a live job", func(t *testing.T) {
		t.Parallel()
		s := NewStore(tmpLoc(t, root))
		seed(t, s, types.Job{ID: "wave/job", State: types.StateRunning, WritePaths: []string{"notes.buzz"}, Check: forkCheck()})
		_, err := Apply(t.Context(), s, []types.Declaration{{ID: "wave/job", WritePaths: []string{"notes.buzz", "magusfile.buzz"}, Check: forkCheck()}}, config.Jobs{}, nil, nil, false)
		require.ErrorIs(t, err, types.WritePathConfigWholeFile)
	})

	t.Run("a checkout that places no line of either", func(t *testing.T) {
		t.Parallel()
		bare := testkit.NewRepo(t, map[string]string{"magusfile.buzz": configMagusfile, "magus.yaml": "jobs:\n  stale_after: 2h\n"}).Root()
		assert.Empty(t, wholeFileConfigClaimProblems(t.Context(), NewStore(tmpLoc(t, bare)), types.Job{ID: "wave/job", WritePaths: []string{"magusfile.buzz", "magus.yaml"}}))
	})

	t.Run("a magus.yaml that declares nothing yet", func(t *testing.T) {
		t.Parallel()
		empty := testkit.NewRepo(t, map[string]string{".gitattributes": "*.yaml diff=yaml\n", "magus.yaml": "{}\n"}).Root()
		assert.Empty(t, wholeFileConfigClaimProblems(t.Context(), NewStore(tmpLoc(t, empty)), types.Job{ID: "wave/job", WritePaths: []string{"magus.yaml"}}))
	})
}

func TestNarrowConfigHold(t *testing.T) {
	t.Parallel()

	root := testkit.NewRepo(t, configFiles).Root()
	loc := tmpLoc(t, root)
	s := NewStore(loc)
	seed(t, s, types.Job{ID: "cfg", State: types.StateRunning, WritePaths: []string{"**/*.buzz"}, Check: forkCheck()})
	seed(t, s, types.Job{ID: "whole", State: types.StateRunning, WritePaths: []string{"libs/lib/magusfile.buzz"}, Check: forkCheck()})
	peer := types.Job{ID: "peer", State: types.StateDeclared, WritePaths: []string{"magusfile.buzz#project.name"}}
	rows, err := s.List()
	require.NoError(t, err)
	assert.Equal(t, types.WriteProofOverlapping, s.WriteProof(rows, peer), "the pattern covers the magusfile whole")

	// The holder narrows its own row: the claims sit inside what its pattern covered.
	got, err := NarrowConfigHold(t.Context(), boundStore(loc, "cfg"), "cfg", "magusfile.buzz", []string{"project.targets.test", types.Preamble})
	require.NoError(t, err)
	body, err := os.ReadFile(filepath.Join(root, "magusfile.buzz"))
	require.NoError(t, err)
	sum := sha256.Sum256(body)
	assert.Equal(t, []string{"**/*.buzz", "magusfile.buzz#project.targets.test"}, got.WritePaths)
	require.Len(t, got.Releases, 1)
	assert.NotZero(t, got.Releases[0].ReleasedAt)
	assert.Equal(t, types.JobRelease{Path: "magusfile.buzz", Digest: "sha256:" + hex.EncodeToString(sum[:]), ReleasedAt: got.Releases[0].ReleasedAt}, got.Releases[0])
	assert.Equal(t, []string{"project.targets.test"}, ClaimedDeclarations(got, "magusfile.buzz"))
	assert.Nil(t, ClaimedDeclarations(got, "notes.buzz"), "the pattern still covers every other file whole")

	rows, err = s.List()
	require.NoError(t, err)
	assert.Equal(t, types.WriteProofDisjoint, s.WriteProof(rows, peer), "a released file is out of the pattern's reach")

	again, err := NarrowConfigHold(t.Context(), s, "cfg", "magusfile.buzz",
		[]string{"project.targets.test", "export fun build(ctx: magus\\Context, args: [str]) > void {"})
	require.NoError(t, err)
	assert.Equal(t, []string{"**/*.buzz", "magusfile.buzz#project.targets.test", "magusfile.buzz#build"}, again.WritePaths)
	assert.Len(t, again.Releases, 1)

	for _, tc := range []struct {
		name, id, file, want string
	}{
		{"a file no project loads", "cfg", "notes.buzz", "notes.buzz is not a project's configuration"},
		{"a row holding the file by its name", "whole", "libs/lib/magusfile.buzz", "no pattern of whole covers libs/lib/magusfile.buzz"},
		{"an unknown row", "gone", "magusfile.buzz", "unknown lease"},
	} {
		_, err := NarrowConfigHold(t.Context(), s, tc.id, tc.file, []string{"project.name"})
		require.Error(t, err, tc.name)
		assert.Contains(t, err.Error(), tc.want, tc.name)
	}
}

// A fork refused over a magusfile another live job holds files one release request per
// holder and file, however often it is refused; a dry run files none.
func TestConfigRefusalRequestsOneRelease(t *testing.T) {
	t.Parallel()

	root := testkit.NewRepo(t, configFiles).Root()
	elsewhere := testkit.NewRepo(t, configFiles)
	s := NewStore(tmpLoc(t, root))
	seed(t, s, types.Job{ID: "cfg", State: types.StateRunning, WritePaths: []string{"**/*.buzz"}, Checkpoint: elsewhere.Head(), CheckoutRoot: elsewhere.Root()})
	fork := func(id string, paths ...string) error {
		_, err := ForkMerge(t.Context(), s, id, func(u *types.Job) {
			u.Check, u.State, u.WritePaths = forkCheck(), types.StateDeclared, paths
		}, config.Jobs{}, nil)
		return err
	}

	_, err := Apply(t.Context(), s, []types.Declaration{{ID: "slots", WritePaths: []string{"magusfile.buzz"}, Check: forkCheck()}}, config.Jobs{}, nil, nil, true)
	require.ErrorIs(t, err, types.WritePathConfigWholeFile)
	assert.Empty(t, releaseRequests(t, root), "a dry run is refused the same way and files nothing")

	for range 2 {
		require.ErrorIs(t, fork("slots", "magusfile.buzz"), types.WritePathConfigWholeFile)
	}
	requests := releaseRequests(t, root)
	require.Len(t, requests, 1)
	assert.Equal(t, string(types.OutcomeWaiting), requests[0].Outcome)
	assert.Equal(t, "magusfile.buzz", requests[0].Where)
	assert.Equal(t, []types.FileRef{{Value: "magusfile.buzz"}}, requests[0].Files)
	assert.Contains(t, requests[0].Message, "job cfg holds magusfile.buzz")
	assert.Contains(t, requests[0].Message, "magus session dispose")

	// MGS3032: a holder in another checkout already claims the key the fork wants.
	seed(t, s, types.Job{ID: "keys", State: types.StateRunning, WritePaths: []string{"magusfile.buzz#project.name"}, Checkpoint: elsewhere.Head(), CheckoutRoot: elsewhere.Root()})
	require.ErrorIs(t, fork("name", "magusfile.buzz#project.name"), types.WritePathFileShared)
	requests = releaseRequests(t, root)
	require.Len(t, requests, 2)
	i := slices.IndexFunc(requests, func(r sessions.AttentionRequest) bool { return r.Files[0].Value != "magusfile.buzz" })
	require.GreaterOrEqual(t, i, 0)
	assert.Equal(t, []types.FileRef{{Value: "magusfile.buzz#project.name"}}, requests[i].Files)
	assert.Contains(t, requests[i].Message, "job keys holds magusfile.buzz")
}

// releaseRequests are the open release requests filed for the repository at root.
func releaseRequests(t *testing.T, root string) []sessions.AttentionRequest {
	t.Helper()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)
	return slices.DeleteFunc(sessions.AttentionQueue(fold), func(r sessions.AttentionRequest) bool { return r.Source != releaseSource })
}
