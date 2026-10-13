package proofread

import (
	"slices"
	"strings"
	"testing"
)

func TestToolLeadNamesWhatTheToolDoes(t *testing.T) {
	cases := []struct {
		name, text string
		hit        bool
	}{
		{"a verb and an object", "Run a Buzz program against the magus client and return its value.", false},
		{"a trigger", "Use BEFORE typing go test, go build or any raw language tool in a repo.", false},
		{"opens on the tool", "This tool runs a Buzz program and returns its value.", true},
		{"opens on a skill", "The skill helps with builds. Use it to run targets.", true},
		{"opens on the name", "magus-run is a skill for running targets. Use it before go test.", true},
		{"three words", "Run things. Use it whenever you want to run things or other stuff.", true},
		{"a lead over 40 words", strings.Repeat("word ", 41) + "ends. Use it later.", true},
		{"40 words", strings.Repeat("word ", 39) + "ends. Use it later.", false},
		{"a code span is one word", "Run `" + strings.Repeat("word ", 60) + "` now and see.", false},
		{"a verb that begins with a tool word", "Tool calls are logged. Use the log to read them.", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := JudgeText(tc.text, KindToolDescription, WithOnly(RuleToolLead))
			if (len(got) > 0) != tc.hit {
				t.Errorf("findings = %v, want a hit: %v", got, tc.hit)
			}
		})
	}
}

func TestToolBoundarySaysWhenNotToUse(t *testing.T) {
	cases := []struct {
		name, text string
		hit        bool
	}{
		{"a refusal", "Run a Buzz program. Do NOT use it to edit files.", false},
		{"another tool", "Transform JSON with Buzz. For workspace queries, use the client tool.", false},
		{"a code-span tool", "Transform JSON with Buzz. For workspace queries, use the `client` tool.", false},
		{"instead", "Review a diff. Use /simplify instead when you want fixes applied.", false},
		{"last resort", "Last resort: read the source at the commit of the binary in use.", false},
		{"read-only", "Return the resolved workspace configuration as JSON. Read-only.", false},
		{"only a trigger", "Report the workspace's configured telemetry and live pool state.", true},
		{"a trigger clause is not a boundary", "Run builds. Use when asked to run, build or test.", true},
		{"a boundary only in a code span", "Run builds. See `do not use` in the docs.", true},
		{"empty", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := JudgeText(tc.text, KindToolDescription, WithOnly(RuleToolBoundary))
			if (len(got) > 0) != tc.hit {
				t.Errorf("findings = %v, want a hit: %v", got, tc.hit)
			}
		})
	}
}

func TestToolLengthCapsTheDescriptionAt1024Runes(t *testing.T) {
	fits := strings.Repeat("a", 1023) + "."
	over := strings.Repeat("a", 1024) + "."

	if got := JudgeText(fits, KindToolDescription, WithOnly(RuleToolLength)); len(got) != 0 {
		t.Errorf("1024 runes: %v", got)
	}

	got := JudgeText(over, KindToolDescription, WithOnly(RuleToolLength))
	if len(got) != 1 || !strings.Contains(got[0].Message, "this has 1025") {
		t.Errorf("1025 runes: %v", got)
	}

	multi := JudgeText("é"+strings.Repeat("é", 1023), KindToolDescription, WithOnly(RuleToolLength))
	if len(multi) != 0 {
		t.Errorf("1024 two-byte runes: %v", multi)
	}
}

func TestToolSellingRefusesWordsThatSell(t *testing.T) {
	cases := []struct {
		name, text, match string
	}{
		{"a plain description", "Run a Buzz program and return its value.", ""},
		{"powerful", "A powerful way to run a Buzz program.", "powerful"},
		{"seamless", "Run a Buzz program with seamless caching.", "seamless"},
		{"a buzzword the other kinds refuse", "Delve into the workspace graph.", "Delve"},
		{"state of the art", "A state-of-the-art graph query.", "state-of-the-art"},
		{"in a code span", "Run `powerful` targets.", ""},
		{"in a quote", "Refuses the word \"seamless\" in a name.", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := JudgeText(tc.text, KindToolDescription, WithOnly(RuleToolSelling))
			if tc.match == "" {
				if len(got) != 0 {
					t.Errorf("findings = %v, want none", got)
				}

				return
			}

			if len(got) != 1 || got[0].Match != tc.match {
				t.Errorf("findings = %v, want one on %q", got, tc.match)
			}
		})
	}
}

func TestToolDescriptionIsJudgedByItsOwnRules(t *testing.T) {
	got := KindRules(KindToolDescription)
	for _, r := range []Rule{RuleToolLead, RuleToolBoundary, RuleToolLength, RuleToolSelling} {
		if !slices.Contains(got, r) {
			t.Errorf("%s does not judge tool-description; rules: %v", r, got)
		}
	}

	for _, r := range []Rule{RuleLeadContext, RuleMessageLength, RuleHelpLength, RuleStaccato} {
		if slices.Contains(got, r) {
			t.Errorf("%s judges tool-description", r)
		}
	}
}

func TestToolRulesAdviseExceptSelling(t *testing.T) {
	want := map[Rule]Decision{
		RuleToolLead: DecisionAdvise, RuleToolBoundary: DecisionAdvise, RuleToolLength: DecisionAdvise,
		RuleToolSelling: DecisionDeny,
	}

	for _, doc := range Catalog() {
		w, ok := want[doc.Name]
		if !ok {
			continue
		}

		if got := doc.Decisions[KindToolDescription]; got != w {
			t.Errorf("%s: default %q, want %q", doc.Name, got, w)
		}

		if doc.Dimension == "" {
			t.Errorf("%s: no dimension", doc.Name)
		}
	}
}
