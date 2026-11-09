package main

import (
	"encoding/json"
	"testing"

	"github.com/egladman/magus/libs/proofread"
)

func TestJSONNamesTheSpecificsAFindingHolds(t *testing.T) {
	text := "fix(cache): read the key once\n\n" +
		"The loader reads the cache, the graph and the log. We moved the docs, rewrote the parser for Sydney " +
		"and shipped the fix.\n"

	_, stdout, stderr := runVoiceArgs(t, text, "change-description", "--stdin", "-only", "triad", "-o", "json",
		"-decisions", houseTable(t, proofread.RuleTriad))

	var got []finding
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("decode %q: %v (stderr %q)", stdout, err, stderr)
	}

	var protected int

	for _, f := range got {
		for _, p := range f.Protected {
			if p.Text != "Sydney" || p.Reason != "a name" || p.Line != 3 {
				t.Errorf("protected = %+v, want Sydney on line 3", p)
			}

			protected++
		}
	}

	if protected != 1 {
		t.Errorf("findings = %+v, want one protected span (stderr %q)", got, stderr)
	}
}
