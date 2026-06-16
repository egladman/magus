package sandbox

import (
	"testing"
)

func TestBuildPolicy_NonNil(t *testing.T) {
	p := BuildPolicy("", nil, nil, nil, nil)
	if p == nil {
		t.Fatal("BuildPolicy returned nil")
	}
}

func TestBuildPolicy_WithWorkspace(t *testing.T) {
	dir := t.TempDir()
	p := BuildPolicy(dir, nil, nil, nil, nil)
	if p == nil {
		t.Fatal("BuildPolicy returned nil")
	}
	// Workspace is always readable and writable.
	if err := p.CheckRead(dir); err != nil {
		t.Errorf("CheckRead workspace: %v", err)
	}
	if err := p.CheckWrite(dir); err != nil {
		t.Errorf("CheckWrite workspace: %v", err)
	}
}
