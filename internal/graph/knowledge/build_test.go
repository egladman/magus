package knowledge

import (
	"context"
	"log/slog"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildFailsOnUnknownMarkerFamily(t *testing.T) {
	in := markerTree(t, map[string]string{"x.go": "package x\n\n// magus:bogus x\n"})

	g, err := Build(context.Background(), t.TempDir(), BuildOptions{}, in, slog.New(slog.DiscardHandler))

	require.ErrorIs(t, err, &types.DiagnosticError{Code: types.UnknownMarkerFamily})
	assert.ErrorContains(t, err, "x.go:3: magus:bogus")
	assert.Nil(t, g)
}
