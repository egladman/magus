package config_test

import (
	"errors"
	"testing"

	"github.com/egladman/magus/internal/config"
)

func TestValidate_ValidMinimal(t *testing.T) {
	// ci.max_shards must be -1 (unlimited) or in [1,256]; use -1 for a minimal valid config.
	cfg := config.Config{CI: config.CI{MaxShards: -1}}
	if err := config.Validate(cfg); err != nil {
		t.Errorf("Validate(minimal valid Config): unexpected error: %v", err)
	}
}

func TestValidate_InvalidConcurrency(t *testing.T) {
	cfg := config.Config{Concurrency: -1}
	if err := config.Validate(cfg); err == nil {
		t.Error("Validate(Concurrency=-1): expected error, got nil")
	}
}

func TestValidationError_Error(t *testing.T) {
	cfg := config.Config{Concurrency: -5}
	err := config.Validate(cfg)
	if err == nil {
		t.Skip("need a validation error to test ValidationError type")
	}
	var ve *config.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("error is %T, not *ValidationError", err)
	}
	if ve.Error() == "" {
		t.Error("ValidationError.Error() is empty")
	}
}
