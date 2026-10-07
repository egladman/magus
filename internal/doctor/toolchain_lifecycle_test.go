package doctor

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// lifecycleWorkspace answers the facts the check reads off a *magus.Magus beyond its
// projects: the wired provider, the root and the resolved cache dir.
type lifecycleWorkspace struct {
	types.WorkspaceReader
	provider, root string
}

func (w lifecycleWorkspace) LifecycleProvider() string { return w.provider }
func (w lifecycleWorkspace) Root() string              { return w.root }
func (w lifecycleWorkspace) CacheDir() string          { return filepath.Join(w.root, ".magus") }

func goProjects(bounds spells.VersionBounds) []*types.Project {
	sp := spells.NewSpell("go", spells.WithTools(map[string]spells.Tool{
		"go": {Probe: spells.Command{Bin: "go"}, Supported: spells.VersionBounds{Min: "1.21"}, Lifecycle: "go"},
	}))
	return []*types.Project{{Path: ".", ResolvedSpells: []*spells.Spell{sp}, ToolBounds: map[string]spells.VersionBounds{"go": bounds}}}
}

func TestCheckToolchainLifecycleNeverFetches(t *testing.T) {
	t.Run("no provider wired", func(t *testing.T) {
		got := (&runner{ws: lifecycleWorkspace{root: t.TempDir()}}).checkToolchainLifecycle(goProjects(spells.VersionBounds{}))
		assert.Equal(t, types.CheckOK, got.Status)
		assert.Contains(t, got.Message, "no lifecycle provider wired")
	})

	t.Run("no spell names a product", func(t *testing.T) {
		got := (&runner{ws: lifecycleWorkspace{provider: "endoflife-date", root: t.TempDir()}}).checkToolchainLifecycle(nil)
		assert.Equal(t, types.Check{
			Name: "toolchain-lifecycle", Status: types.CheckOK,
			Message: "no spell names a lifecycle product",
		}, got)
	})

	// Nothing stored is unknown, not fine, and the remedy is the command that asks.
	t.Run("no stored answer", func(t *testing.T) {
		got := (&runner{ws: lifecycleWorkspace{provider: "endoflife-date", root: t.TempDir()}}).checkToolchainLifecycle(goProjects(spells.VersionBounds{}))
		assert.Equal(t, types.Check{
			Name: "toolchain-lifecycle", Status: types.CheckOK, Evidence: types.EvidenceUnknown,
			Message: "no stored answer from endoflife-date for this workspace's spells; `magus describe tools` asks it",
			Fix:     []string{"describe", "tools"},
		}, got)
	})
}

// storeLifecycles writes the answer `magus describe tools` would have stored, through the
// real cache, so the check is read against the same fingerprint the CLI writes. This test
// binary does not link the bindings layer, so the runner slot is free to fill once.
var registerStub sync.Once

func storeLifecycles(t *testing.T, ws lifecycleWorkspace, projects []*types.Project, answer []spells.Lifecycle, installed []workspace.InstalledTool) {
	t.Helper()
	registerStub.Do(func() {
		workspace.RegisterLifecycleRunner(func(context.Context, string, string, []string) ([]spells.Lifecycle, error) {
			return stubbedLifecycles, nil
		})
	})
	stubbedLifecycles = answer
	_, err := workspace.AskLifecycles(t.Context(), workspace.ProviderCache{Dir: ws.CacheDir()}, ws.root, ws.provider,
		workspace.LifecycleKeys(projects), installed)
	require.NoError(t, err)
}

var stubbedLifecycles []spells.Lifecycle

// The check reads the answer stored under the workspace's resolved root and cache dir,
// not the --root override doctor was started with, which is usually empty.
func TestCheckToolchainLifecycleReadsTheStoredAnswer(t *testing.T) {
	ws := lifecycleWorkspace{provider: "endoflife-date", root: t.TempDir()}
	projects := goProjects(spells.VersionBounds{})
	storeLifecycles(t, ws, projects,
		[]spells.Lifecycle{{Key: "go", Source: "https://endoflife.date/api/v1/products/go", AsOf: "2026-09-24T07:44:41Z",
			Cycles: []spells.ReleaseCycle{{Cycle: "1.20", EOL: "2024-02-06"}}}},
		[]workspace.InstalledTool{{Project: ".", Bin: "go", Lifecycle: "go", Version: "v1.20.14"}})

	got := (&runner{ws: ws, root: ""}).checkToolchainLifecycle(projects)
	assert.Equal(t, types.Check{
		Status:  types.CheckAdvice,
		Details: []string{"./go: installed v1.20.14 is in go 1.20, past end of life since 2024-02-06"},
	}, withoutWording(got))
	assert.Contains(t, got.Message, "1 past end of life, per endoflife-date as fetched ")
}

func TestLifecycleFindings(t *testing.T) {
	now := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	answer := workspace.LifecycleAnswer{
		Lifecycles: []spells.Lifecycle{{Key: "go", Cycles: []spells.ReleaseCycle{
			{Cycle: "1.24", EOL: "2026-02-10"},
			{Cycle: "1.25", EOL: "2026-08-19"},
			{Cycle: "1.26"},
		}}},
		Installed: []workspace.InstalledTool{
			{Project: ".", Bin: "go", Lifecycle: "go", Version: "v1.25.3"},
			{Project: "libs/a", Bin: "go", Lifecycle: "go", Version: "v1.26.6"},
		},
	}

	got := lifecycleFindings(goProjects(spells.VersionBounds{Min: "1.24"}), answer, now)
	assert.Equal(t, []string{
		"./go: installed v1.25.3 is in go 1.25, past end of life since 2026-08-19",
		"./go: window min 1.24 admits go 1.24, past end of life since 2026-02-10",
	}, got)

	// The spell's own floor (1.21) is what its ops need, not a policy this workspace set,
	// so with no workspace floor only the installed version is reported.
	got = lifecycleFindings(goProjects(spells.VersionBounds{}), answer, now)
	assert.Equal(t, []string{"./go: installed v1.25.3 is in go 1.25, past end of life since 2026-08-19"}, got)

	assert.Empty(t, lifecycleFindings(goProjects(spells.VersionBounds{Min: "1.26"}), workspace.LifecycleAnswer{Lifecycles: answer.Lifecycles}, now),
		"a supported floor and nothing installed past its end is no finding")
}
