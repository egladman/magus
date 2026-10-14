package unlinked_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// This binary links nothing from buzzengine on purpose: it is the program the typed error
// speaks to. Every other test binary that opens a magusfile imports the engine.

func TestMain(m *testing.M) { testkit.Main(m) }

func TestEngineIsNotLinkedHere(t *testing.T) {
	require.False(t, interp.Available(),
		"something in this test binary imports the engine, so the tests below prove nothing")
}

func TestOpenMagusfileWithoutEngine(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Magusfile("import \"magus\";\n")

	for name, load := range map[string]func(context.Context, string) error{
		"Open": func(ctx context.Context, root string) error {
			m, err := magus.Open(ctx, root)
			if err == nil {
				_ = m.Close()
			}
			return err
		},
		"Inspect": func(ctx context.Context, root string) error {
			_, err := magus.Inspect(ctx, root)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := load(context.Background(), w.Root())

			var notLinked *types.EngineNotLinkedError
			require.True(t, errors.As(err, &notLinked), "want *types.EngineNotLinkedError, got %v", err)
			assert.True(t, strings.HasPrefix(notLinked.Op, "evaluate "), notLinked.Op)
			assert.True(t, strings.HasSuffix(notLinked.Op, "magusfile.buzz"), notLinked.Op)
			assert.ErrorContains(t, err, string(types.EngineNotLinked))
			assert.ErrorContains(t, err, `_ "github.com/egladman/magus/buzzengine"`)
		})
	}
}

func TestInspectWorkspaceWithoutMagusfileWithoutEngine(t *testing.T) {
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell("go",
		spells.WithSources("**/*.go", "go.mod"),
		spells.WithManifests("go.mod"),
	))
	w := testkit.NewWorkspace(t)
	w.Write("go.mod", "module example.test/app\n")

	ws, err := magus.Inspect(context.Background(), w.Root())

	require.NoError(t, err)
	assert.NotNil(t, ws.Get("."), "the go.mod project is discovered without the engine")
}
