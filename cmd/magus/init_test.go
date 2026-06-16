package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteMagusfileStub(t *testing.T) {
	dir := t.TempDir()
	if err := writeMagusfileStub(dir); err != nil {
		t.Fatalf("writeMagusfileStub: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "magusfile.buzz"))
	if err != nil {
		t.Fatalf("expected magusfile.buzz: %v", err)
	}
	body := string(data)
	for _, want := range []string{
		`import "magus"`,
		"magus.project.register(",
		`export fun preflight`,
		`export fun test`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("magusfile.buzz missing %q", want)
		}
	}
}

func TestMagusfilePresent(t *testing.T) {
	for _, name := range []string{"magusfile.buzz"} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !magusfilePresent(dir) {
			t.Errorf("magusfilePresent should detect %s", name)
		}
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "magusfiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !magusfilePresent(dir) {
		t.Error("magusfilePresent should detect magusfiles/ directory")
	}
	if magusfilePresent(t.TempDir()) {
		t.Error("magusfilePresent should be false for an empty directory")
	}
}

// An existing magusfile must not be clobbered by a stub write.
func TestWriteMagusfileStubSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "magusfile.buzz")
	if err := os.WriteFile(existing, []byte("// mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeMagusfileStub(dir); err != nil {
		t.Fatalf("writeMagusfileStub: %v", err)
	}
	data, _ := os.ReadFile(existing)
	if string(data) != "// mine\n" {
		t.Errorf("existing magusfile.buzz was modified: %q", string(data))
	}
}
