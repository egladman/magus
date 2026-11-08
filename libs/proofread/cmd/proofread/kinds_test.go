package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const entryTable = `{"kinds": {"entry": {
	"extends": "release-notes",
	"reader": {"format": "lines"},
	"rules": {"tense": "deny", "claim": "deny", "buzzword": "deny"},
	"minimum": 90,
	"terms": {"utilize": "use", "spearheaded": "led"}
}}}`

func TestADeclaredKindIsASubcommand(t *testing.T) {
	dir := t.TempDir()
	decisions := writeFile(t, dir, "decisions.json", entryTable)
	entries := writeFile(t, dir, "entries.txt", "Spearheaded the cache rewrite\n")

	code, stdout, stderr := runArgs(t, []string{"entry", "-decisions", decisions, entries}, "")
	if code != exitFindings {
		t.Fatalf("code %d, want %d: %s", code, exitFindings, stderr)
	}

	var found []finding
	if err := json.Unmarshal([]byte(stdout), &found); err != nil {
		t.Fatal(err)
	}

	if len(found) == 0 || found[0].Kind != "entry" || found[0].Rule != "terms" || found[0].Replacements[0] != "Led" {
		t.Errorf("findings = %+v, want a terms finding on the entry kind", found)
	}
}

func TestADeclaredKindReadsStdin(t *testing.T) {
	decisions := writeFile(t, t.TempDir(), "decisions.json", entryTable)

	if code, _, stderr := runArgs(t, []string{"entry", "-decisions", decisions, "-stdin"}, "Fixed the cache\n"); code != exitOK {
		t.Errorf("code %d, want %d: %s", code, exitOK, stderr)
	}
}

func TestANameNoTableDeclaresIsAnUnknownSubcommand(t *testing.T) {
	decisions := writeFile(t, t.TempDir(), "decisions.json", entryTable)

	_, _, stderr := runArgs(t, []string{"entries", "-decisions", decisions, "-stdin"}, "")
	if !strings.HasPrefix(stderr, "proofread: unknown subcommand \"entries\"\n\nusage: proofread") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestATableDeclaringABadKindNamesTheField(t *testing.T) {
	decisions := writeFile(t, t.TempDir(), "decisions.json", `{"kinds": {"entry": {"extends": "notes"}}}`)

	code, _, stderr := runArgs(t, []string{"reference", "-decisions", decisions, "-minimum", "0", "page.md"}, "")
	if code != exitError || !strings.Contains(stderr, `kinds.entry.extends: unknown kind "notes"`) {
		t.Errorf("code %d, stderr %q", code, stderr)
	}
}

func TestATableRefusesAnUnknownFieldInAKind(t *testing.T) {
	decisions := writeFile(t, t.TempDir(), "decisions.json", `{"kinds": {"entry": {"extend": "changelog"}}}`)

	code, _, stderr := runArgs(t, []string{"entry", "-decisions", decisions, "-stdin"}, "")
	if code != exitError || !strings.Contains(stderr, `unknown field "extend"`) {
		t.Errorf("code %d, stderr %q", code, stderr)
	}
}

func TestATableRefusesTwoMinimumsForADeclaredKind(t *testing.T) {
	decisions := writeFile(t, t.TempDir(), "decisions.json",
		`{"minimums": {"entry": 80}, "kinds": {"entry": {"extends": "changelog", "minimum": 90}}}`)

	_, _, stderr := runArgs(t, []string{"entry", "-decisions", decisions, "-stdin"}, "")
	if !strings.Contains(stderr, "minimums: entry: kinds.entry.minimum sets it too: keep one") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestATableMinimumGradesADeclaredKind(t *testing.T) {
	decisions := writeFile(t, t.TempDir(), "decisions.json",
		`{"minimums": {"entry": 100}, "kinds": {"entry": {"extends": "changelog"}}}`)

	code, _, stderr := runArgs(t, []string{"entry", "-decisions", decisions, "-stdin"}, "It basically works.\n")
	if code != exitFindings {
		t.Errorf("code %d, want %d: %s", code, exitFindings, stderr)
	}
}
