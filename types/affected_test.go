package types

import (
	"errors"
	"testing"
)

func TestErrAffectedFallback(t *testing.T) {
	if ErrAffectedFallback == nil {
		t.Fatal("ErrAffectedFallback is nil")
	}
	if ErrAffectedFallback.Error() == "" {
		t.Fatal("ErrAffectedFallback.Error() is empty")
	}
	if !errors.Is(ErrAffectedFallback, ErrAffectedFallback) {
		t.Fatal("errors.Is identity check failed")
	}
}

func TestAffectedResult(t *testing.T) {
	r := AffectedResult{
		Base:        "main",
		Changed:     []string{"api/main.go", "api/handler.go"},
		Seed:        []string{"api/"},
		FilesBySeed: map[string][]string{"api/": {"api/main.go", "api/handler.go"}},
		Affected:    []string{"api/", "gateway/"},
	}
	if r.Base != "main" {
		t.Errorf("Base = %q, want %q", r.Base, "main")
	}
	if len(r.Changed) != 2 {
		t.Errorf("len(Changed) = %d, want 2", len(r.Changed))
	}
	if len(r.FilesBySeed["api/"]) != 2 {
		t.Errorf("FilesBySeed[api/] len = %d, want 2", len(r.FilesBySeed["api/"]))
	}
	if len(r.Affected) != 2 {
		t.Errorf("len(Affected) = %d, want 2", len(r.Affected))
	}
}
