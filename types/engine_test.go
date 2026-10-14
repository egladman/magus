package types

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEngineNotLinkedErrorNamesTheLinkPackage(t *testing.T) {
	err := fmt.Errorf("magus: %w", &EngineNotLinkedError{Op: "evaluate /repo/magusfile.buzz"})

	var got *EngineNotLinkedError
	require.True(t, errors.As(err, &got))
	assert.Equal(t, "evaluate /repo/magusfile.buzz", got.Op)
	assert.Contains(t, err.Error(), string(EngineNotLinked))
	assert.Contains(t, err.Error(), "cannot evaluate /repo/magusfile.buzz")
	assert.Contains(t, err.Error(), `_ "github.com/egladman/magus/buzzengine"`)
}
