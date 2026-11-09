package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// pairedFiles writes 25 owner replies and a rewrite of each that joins its
// clauses with a semicolon, plus one rewrite whose original is missing.
func pairedFiles(t *testing.T) (owner, other string) {
	t.Helper()

	dir := t.TempDir()

	var owners, others strings.Builder

	for i := range 25 {
		id := fmt.Sprintf("r%d", i)
		fmt.Fprintf(&owners, `{"id":%q,"kind":"review-reply","text":"Case %d builds, the cache holds it."}`+"\n", id, i)
		fmt.Fprintf(&others, `{"id":%q,"kind":"review-reply","text":"Case %d builds; the cache holds it."}`+"\n", id, i)
	}

	others.WriteString(`{"id":"orphan","kind":"review-reply","text":"No original."}` + "\n")

	return writeFile(t, dir, "owner.jsonl", owners.String()), writeFile(t, dir, "other.jsonl", others.String())
}

func TestVoiceEvalPairedPrintsHowEachFeatureMoves(t *testing.T) {
	owner, other := pairedFiles(t)

	code, stdout, stderr := runVoiceArgs(t, "", "voice", "eval", "-paired", "-owner", owner, "-other", other)
	if code != exitOK || stderr != "" {
		t.Fatalf("voice eval -paired: code %d stderr %q", code, stderr)
	}

	lines := strings.Split(stdout, "\n")
	if lines[0] != "review-reply: 25 pairs, 1 texts unpaired; cut at 5% owner false alarms" {
		t.Errorf("header = %q", lines[0])
	}

	for _, line := range lines[2:] {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == "punctuation.semicolon" {
			if want := []string{"25", "1.000", "0", "0.000", "1.000", "yes"}; strings.Join(fields[1:], " ") != strings.Join(want, " ") {
				t.Errorf("semicolon row = %q, want %q", fields[1:], want)
			}

			return
		}
	}

	t.Errorf("no semicolon row in %q", stdout)
}

func TestVoiceEvalPairedWritesJSON(t *testing.T) {
	owner, other := pairedFiles(t)

	_, stdout, _ := runVoiceArgs(t, "", "voice", "eval", "-paired", "-owner", owner, "-other", other, "-o", "json")

	var got map[string][]struct {
		Feature   string `json:"feature"`
		Separates bool   `json:"separates"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil || len(got["review-reply"]) < 3 {
		t.Fatalf("voice eval -paired -o json = %s, %v", stdout, err)
	}

	// The comma falls as the semicolon rises, and nothing else moves.
	for i, s := range got["review-reply"] {
		if want := i < 2; s.Separates != want {
			t.Errorf("row %d %s separates = %v, want %v", i, s.Feature, s.Separates, want)
		}
	}
}

func TestVoiceEvalPairedRefusesAnIDTwice(t *testing.T) {
	dir := t.TempDir()
	twice := writeFile(t, dir, "twice.jsonl", `{"id":"a","kind":"issue","text":"One."}`+"\n"+`{"id":"a","kind":"issue","text":"Two."}`+"\n")

	code, _, stderr := runVoiceArgs(t, "", "voice", "eval", "-paired", "-owner", twice, "-other", twice)
	if code != exitError || !strings.Contains(stderr, `issue "a" appears twice`) {
		t.Errorf("an id twice: code %d stderr %q", code, stderr)
	}
}

func TestVoiceEvalPairedRefusesALineWithNoID(t *testing.T) {
	dir := t.TempDir()
	bare := writeFile(t, dir, "bare.jsonl", `{"kind":"issue","text":"One."}`+"\n")

	code, _, stderr := runVoiceArgs(t, "", "voice", "eval", "-paired", "-owner", bare, "-other", bare)
	if code != exitError || !strings.Contains(stderr, `no string field "id" holds an id`) {
		t.Errorf("a line with no id: code %d stderr %q", code, stderr)
	}
}

func TestVoiceEvalPairedPoolsPairsTogether(t *testing.T) {
	owner, other := pairedFiles(t)

	_, stdout, _ := runVoiceArgs(t, "", "voice", "eval", "-paired", "-pool", "20", "-owner", owner, "-other", other)
	if !strings.HasPrefix(stdout, "review-reply: 8 chunk pairs of 20+ owner words, 1 texts unpaired") {
		t.Errorf("pooled header = %q", strings.SplitN(stdout, "\n", 2)[0])
	}
}
