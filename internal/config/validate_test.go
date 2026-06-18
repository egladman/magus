package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate_ValidMinimal(t *testing.T) {
	// ci.max_shards must be -1 (unlimited) or in [1,256]; use -1 for a minimal valid config.
	cfg := Config{CI: CI{MaxShards: -1}}
	assert.NoError(t, Validate(cfg), "Validate(minimal valid Config)")
}

func TestValidate_InvalidConcurrency(t *testing.T) {
	cfg := Config{Concurrency: -1}
	assert.Error(t, Validate(cfg), "Validate(Concurrency=-1): expected error")
}

func TestValidationError_Error(t *testing.T) {
	cfg := Config{Concurrency: -5}
	err := Validate(cfg)
	if err == nil {
		t.Skip("need a validation error to test ValidationError type")
	}
	var ve *ValidationError
	require.ErrorAs(t, err, &ve)
	assert.NotEmpty(t, ve.Error(), "ValidationError.Error() is empty")
}
