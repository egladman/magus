package evidence

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// describe job reads the graph as built and job wait may build it. On a current index the
// two grade a row alike; on a stale one describe builds nothing and leaves the symbol postcondition
// ungraded, naming the rebuild.
func TestDescribeAndWaitGradeSymbolsAlike(t *testing.T) {
	t.Parallel()

	g := deltaGraph(map[string]string{"Present": "code.go:3"})
	row := jobtypes.Job{ID: "wave/sym", Paths: jobtypes.Paths{Write: []string{"code.go"}}, Postconditions: []jobtypes.Postcondition{
		{ID: "here", Kind: jobtypes.PostconditionKindSymbol, Expect: jobtypes.ExpectPresent, Symbols: []string{"Present"}},
		{ID: "gone", Kind: jobtypes.PostconditionKindSymbol, Expect: jobtypes.ExpectAbsent, Symbols: []string{"Removed"}},
	}}
	read := func(rebuild bool, stale []types.KnowledgeStaleIndex) *Graph {
		return &Graph{
			rebuild: rebuild,
			stale:   func(context.Context) []types.KnowledgeStaleIndex { return stale },
			build: func(context.Context) (*knowledge.Graph, error) {
				if !rebuild && len(stale) > 0 {
					t.Error("describe built the graph over a stale index")
				}
				return g, nil
			},
		}
	}
	grade := func(graph *Graph) jobtypes.Status {
		seen, err := Observer("", func(string) *Graph { return graph }, Env{})(t.Context(), row)
		require.NoError(t, err)
		return job.VerifyGates(row, jobtypes.Result{Job: row.ID}, jobtypes.CapturedRun{}, nil, []jobtypes.Job{row}, seen)
	}

	described, waited := grade(read(false, nil)), grade(read(true, nil))
	assert.Equal(t, waited, described, "on a current index describe and wait agree")
	require.Len(t, described.Gates, 2)
	for _, gate := range described.Gates {
		assert.True(t, gate.Verified, "%+v", gate)
	}

	stale := []types.KnowledgeStaleIndex{{Project: "pkg", Language: "go"}}
	described = grade(read(false, stale))
	require.Len(t, described.Gates, 2)
	for _, gate := range described.Gates {
		assert.False(t, gate.Verified, gate.ID)
		assert.Contains(t, strings.Join(gate.Violations, " "), hint.GraphBuild.String(), gate.ID)
	}
	assert.Equal(t, waited, grade(read(true, stale)), "wait builds whatever the index says")
}

func TestJobObserverGradesSymbolsInTheJobsOwnTree(t *testing.T) {
	t.Parallel()

	root, checkout := t.TempDir(), t.TempDir()
	row := jobtypes.Job{ID: "wave/sym", CheckoutRoot: checkout, Postconditions: []jobtypes.Postcondition{
		{ID: "here", Kind: jobtypes.PostconditionKindSymbol, Expect: jobtypes.ExpectPresent, Symbols: []string{"Present"}},
	}}
	var trees []string
	graphIn := func(tree string) *Graph {
		trees = append(trees, tree)
		return &Graph{
			rebuild: true,
			stale:   func(context.Context) []types.KnowledgeStaleIndex { return nil },
			build: func(context.Context) (*knowledge.Graph, error) {
				return deltaGraph(map[string]string{"Present": "code.go:3"}), nil
			},
		}
	}

	seen, err := Observer(root, graphIn, Env{})(t.Context(), row)

	require.NoError(t, err)
	assert.Equal(t, []string{checkout}, trees, "the worker's checkout holds the symbols its branch defines, not the waiter's")
	assert.True(t, seen.SymbolsKnown)
}

// The cache hashes a settled key after a `:rw` run, and ComputeTargetKey mints a key for
// the committed tree. They must be one key, so the run that rewrote a file stays current
// until a hand edit moves that file again.
func TestARunThatEditsInPlaceIsCurrentAtTheKeyItLeft(t *testing.T) {
	repo := testkit.NewRepository(t, map[string]string{"a.txt": "unformatted\n"})
	repo.Magusfile("")
	repo.Commit("magusfile", nil)
	src := filepath.Join(repo.Root(), "a.txt")
	name := register.Spell(t, "zzz-settled-key-spell",
		spells.WithTargets("fmt"),
		spells.WithSources("*.txt"),
		spells.WithOps(map[string]spells.Op{"fmt": {Command: spells.Command{
			Bin: "true", Edits: []string{"*.txt"}, Charms: map[string]spells.Charm{types.CharmReadWrite: {}},
		}}}),
		spells.WithInvoker(func(ctx context.Context, _ spells.InvokeRequest) (any, error) {
			if !types.HasCharm(ctx, types.CharmReadWrite) {
				return nil, nil
			}
			return nil, os.WriteFile(src, []byte("formatted\n"), 0o644)
		}),
	)
	reg := magus.NewWorkspaceRegistry()
	reg.RegisterProject(".", magus.WithSpell(name))
	m, err := magus.Open(t.Context(), repo.Root(), magus.WithWorkspaceRegistry(reg), magus.WithSpawnRule(tier.Ceiling))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	require.NoError(t, m.Run(t.Context(), []types.Target{{Path: ".", Name: "fmt"}}, magus.WithWrite()))
	dir, err := magus.ResolveCacheDir(repo.Root())
	require.NoError(t, err)
	runs, err := cache.NewOutputStore(dir).ListDescriptors()
	require.NoError(t, err)
	require.Len(t, runs, 1)
	desc := runs[0]
	require.NotEmpty(t, desc.SettledKey, "the run rewrote a.txt, its declared edit")
	run := desc.JobAttempt(desc.Attempt)

	repo.Commit("fmt", nil)
	current := runsAtCurrentKey(func(string) *Graph {
		return &Graph{workspace: func(context.Context) (*magus.Magus, error) { return m, nil }}
	}, Env{})
	key, _, err := m.ComputeTargetKey(t.Context(), ".", "fmt", []string{types.CharmReadWrite})
	require.NoError(t, err)
	assert.NotEqual(t, desc.Key, key, "the run's own key names the tree before its rewrite")
	assert.Equal(t, desc.SettledKey, key, "the cache's post-run hash and ComputeTargetKey agree on one tree")
	assert.True(t, current(t.Context(), repo.Root(), run), "the run is current at the tree it left")

	require.NoError(t, os.WriteFile(src, []byte("hand edited\n"), 0o644))
	assert.False(t, current(t.Context(), repo.Root(), run), "a hand edit after the run moves the key past both")
}
