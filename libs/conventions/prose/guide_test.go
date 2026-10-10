package prose

import (
	"slices"
	"testing"
)

func TestSecondPersonReportsTheProjectVoiceInAGuide(t *testing.T) {
	runTextCases(t, RuleSecondPerson, []textCase{
		// docs/guides/integrations/agents/any-host.md, before the guide rules.
		{"us", KindGuide, "A codec per host would cost us upkeep.", []string{"1:us"}},
		{"we, our and ours", KindGuide, "We keep our cache; the cost is ours.", []string{"1:We", "1:our", "1:ours"}},
		{"let's", KindGuide, "Let's add a target.", []string{"1:Let's"}},
		{"a concept page keeps the project voice", KindReference, "We believe a cache is a contract.", nil},
		{"US is a locale", KindGuide, "Set the locale to en-US.", nil},
		{"code and quotes are mentions", KindGuide, "Pass `--ours`, or the side git calls \"ours\".", nil},
		// tense reports these, so the guide does not report them again.
		{"we as the actor of a change", KindGuide, "We added a flag.", nil},
		{"we'll", KindGuide, "Next we'll add a target.", nil},
	})
}

// With tense off, as it ships, nothing reports the "we" tense would have, so
// second-person does.
func TestSecondPersonReportsTheWeTenseWouldWhereTenseIsOff(t *testing.T) {
	var got []string

	for _, f := range JudgeText("We added a flag.", KindGuide) {
		got = append(got, string(f.Rule)+":"+f.Match)
	}

	if want := []string{"second-person:We"}; !slices.Equal(got, want) {
		t.Errorf("findings: got %q, want %q", got, want)
	}
}

func TestStepVerbReportsAStepThatDoesNotOpenWithItsAction(t *testing.T) {
	runTextCases(t, RuleStepVerb, []textCase{
		{"imperative steps", KindGuide, "1. Run `magus init`.\n2. **Add** a target.\n3. Open the page.", nil},
		{
			"an article, a pronoun and You", KindGuide,
			"1. Run `magus init`.\n2. The target builds.\n3. It prints a ref.\n4. You commit the lock.",
			[]string{"2:The", "3:It", "4:You"},
		},
		{
			"a code span or a link with no verb before it", KindGuide,
			"1. Save the key.\n2. `magus ls` lists them.\n3. [The guide](g.md) explains it.",
			[]string{"2:`magus ls`", "3:[The guide]"},
		},
		{"a code span after its verb", KindGuide, "1. Run `magus ls`.\n2. Open [the guide](g.md).", nil},
		// docs/guides/getting-started.md: a recap of what the page did.
		{
			"a list with no imperative is not a procedure", KindGuide,
			"1. `magus init` bootstrapped `magus.yaml`.\n2. A `ci` target runs the pipeline.", nil,
		},
		// docs/guides/migrating/breaking-changes.md: a sequence of events.
		{
			"a sequence of events", KindGuide,
			"1. The drift gate fails.\n2. You regenerate.\n3. A reviewer reads it.", nil,
		},
		{
			"code between steps does not end the procedure", KindGuide,
			"1. Save the key.\n\n   ```sh\n   cp a b\n   ```\n\n2. The signature verifies.",
			[]string{"7:The"},
		},
		{
			"a paragraph ends the procedure", KindGuide,
			"1. Save the key.\n\nThen read on.\n\n1. The drift gate fails.", nil,
		},
		{
			"a nested list is its own", KindGuide,
			"1. Save the key.\n   1. The key is PEM.\n2. Verify it.", nil,
		},
		{"bullets are not steps", KindGuide, "- Run it.\n- The cache fills.", nil},
		{"only a guide", KindReference, "1. Run `magus init`.\n2. The target builds.", nil},
	})
}

func TestCondescensionReportsWordsThatTellTheReaderAStepIsEasy(t *testing.T) {
	runTextCases(t, RuleCondescension, []textCase{
		{"each word", KindGuide, "It is easy: easily done, obviously, of course, clearly. Please wait.",
			[]string{"1:easy", "1:easily", "1:obviously", "1:of course", "1:clearly", "1:Please"}},
		{"simple", KindGuide, "A simple target builds it.", []string{"1:simple"}},
		{"just before a step", KindGuide, "Then you just run `magus init`.", []string{"1:just"}},
		// docs/guides/setup/linux.md, docs/guides/setup.md, docs/guides/integrations/agents.md.
		{"just as only, recency and contrast", KindGuide,
			"It extracts just the binary you just downloaded, not just the hunks.", nil},
		// docs/guides/releasing.md, docs/guides/integrations/agents/skills.md.
		{"an easy mistake and a denied ease", KindGuide,
			"The order is easy to get\nwrong, and you cannot easily audit it.", nil},
		{"only a guide", KindReference, "It is easy.", nil},
		{"a quoted word is a mention", KindGuide, `The rule refuses "easy".`, nil},
	})
}

// A word both filler and condescension name is reported once, by filler.
func TestFillerAndCondescensionReportAWordOnce(t *testing.T) {
	got := JudgeText("Simply run it. Just add a target. It is just files. Please note the key.", KindGuide)
	want := []Finding{
		{Rule: RuleFiller, Message: "Drop 'Simply': state the fact.", Match: "Simply", Line: 1},
		{Rule: RuleFiller, Message: "Drop 'Just': state the fact.", Match: "Just", Line: 1},
		{Rule: RuleFiller, Message: "Drop 'just': state the fact.", Match: "just", Line: 1},
		{Rule: RuleFiller, Message: "Drop 'Please note': state the fact.", Match: "Please note", Line: 1},
	}

	assertFindings(t, got, want)
}
