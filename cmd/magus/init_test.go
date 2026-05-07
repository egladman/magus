package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteMagusfileStubLang(t *testing.T) {
	cases := []struct {
		lang    string
		file    string
		wantAll []string
	}{
		{"teal", "magusfile.tl", []string{
			`require("spells.hello")`,
			"magus.project.register(",
			`global function preflight`,
			`global function test`,
		}},
		{"buzz", "magusfile.bzz", []string{
			`import "spells/hello"`,
			"magus.project.register(",
			`export fun preflight`,
			`export fun test`,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.lang, func(t *testing.T) {
			dir := t.TempDir()
			if err := writeMagusfileStub(dir, tc.lang); err != nil {
				t.Fatalf("writeMagusfileStub: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(dir, tc.file))
			if err != nil {
				t.Fatalf("expected %s: %v", tc.file, err)
			}
			body := string(data)
			for _, want := range tc.wantAll {
				if !strings.Contains(body, want) {
					t.Errorf("%s missing %q", tc.file, want)
				}
			}

			// Both languages scaffold the same workspace-local Teal spell, under the
			// directory convention (spells/<name>/spell.tl).
			spell, err := os.ReadFile(filepath.Join(dir, "spells", "hello", "spell.tl"))
			if err != nil {
				t.Fatalf("expected spells/hello/spell.tl: %v", err)
			}
			for _, want := range []string{"mgs_getName", "echo", "mgs_listTargets"} {
				if !strings.Contains(string(spell), want) {
					t.Errorf("spells/hello/spell.tl missing %q", want)
				}
			}
		})
	}
}

func TestMagusfilePresent(t *testing.T) {
	for _, name := range []string{"magusfile.tl", "magusfile.bzz"} {
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

// An existing magusfile of any dialect must not be clobbered by a stub write.
func TestWriteMagusfileStubSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "magusfile.tl")
	if err := os.WriteFile(existing, []byte("-- mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeMagusfileStub(dir, "buzz"); err != nil {
		t.Fatalf("writeMagusfileStub: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "magusfile.bzz")); err == nil {
		t.Error("writeMagusfileStub wrote magusfile.bzz over an existing magusfile.tl")
	}
	data, _ := os.ReadFile(existing)
	if string(data) != "-- mine\n" {
		t.Errorf("existing magusfile.tl was modified: %q", string(data))
	}
}
