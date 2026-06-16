package filesystem

import (
	"testing"
)

func TestExpandUserRule_AbsPath(t *testing.T) {
	r, err := ExpandUserRule("/tmp/mydir", true, false)
	if err != nil {
		t.Fatalf("ExpandUserRule: %v", err)
	}
	if r.Path != "/tmp/mydir" {
		t.Errorf("Path = %q, want %q", r.Path, "/tmp/mydir")
	}
	if !r.Read {
		t.Error("Read = false, want true")
	}
	if r.Write {
		t.Error("Write = true, want false")
	}
	if !r.Exec {
		t.Error("Exec = false; user-allow paths should default to Exec=true")
	}
}

func TestExpandUserRule_EnvVar(t *testing.T) {
	t.Setenv("TEST_EXPAND_PATH", "/var/test")
	r, err := ExpandUserRule("$TEST_EXPAND_PATH", true, false)
	if err != nil {
		t.Fatalf("ExpandUserRule: %v", err)
	}
	if r.Path != "/var/test" {
		t.Errorf("Path = %q, want %q", r.Path, "/var/test")
	}
}
