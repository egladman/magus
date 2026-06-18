package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrAffectedFallback(t *testing.T) {
	require.NotNil(t, ErrAffectedFallback)
	assert.NotEmpty(t, ErrAffectedFallback.Error())
	assert.ErrorIs(t, ErrAffectedFallback, ErrAffectedFallback)
}

func TestAffectedResult(t *testing.T) {
	r := AffectedResult{
		Base:        "main",
		Changed:     []string{"api/main.go", "api/handler.go"},
		Seed:        []string{"api/"},
		FilesBySeed: map[string][]string{"api/": {"api/main.go", "api/handler.go"}},
		Affected:    []string{"api/", "gateway/"},
	}
	assert.Equal(t, AffectedResult{
		Base:        "main",
		Changed:     []string{"api/main.go", "api/handler.go"},
		Seed:        []string{"api/"},
		FilesBySeed: map[string][]string{"api/": {"api/main.go", "api/handler.go"}},
		Affected:    []string{"api/", "gateway/"},
	}, r)
}
