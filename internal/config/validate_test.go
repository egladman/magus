package config

import (
	"errors"
	"testing"
)

func TestValidate_ValidMinimal(t *testing.T) {
	// ci.max_shards must be -1 (unlimited) or in [1,256]; use -1 for a minimal valid config.
	cfg := Config{CI: CI{MaxShards: -1}}
	if err := Validate(cfg); err != nil {
		t.Errorf("Validate(minimal valid Config): unexpected error: %v", err)
	}
}

func TestValidate_InvalidConcurrency(t *testing.T) {
	cfg := Config{Concurrency: -1}
	if err := Validate(cfg); err == nil {
		t.Error("Validate(Concurrency=-1): expected error, got nil")
	}
}

func TestValidationError_Error(t *testing.T) {
	cfg := Config{Concurrency: -5}
	err := Validate(cfg)
	if err == nil {
		t.Skip("need a validation error to test ValidationError type")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("error is %T, not *ValidationError", err)
	}
	if ve.Error() == "" {
		t.Error("ValidationError.Error() is empty")
	}
}
