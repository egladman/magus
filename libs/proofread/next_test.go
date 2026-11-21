package proofread

import (
	"slices"
	"testing"
)

const nextLead = "feat: share the recorder\n\nMove capture rendering into the shared library so both callers produce the same frames.\n\n"

func TestNextHousekeepingBlocksPullRequestAdministration(t *testing.T) {
	for _, item := range []string{
		"Review GitHub checks after the prerequisite repair lands.",
		"Verify the new GitHub checks.",
		"Check the CI results.",
		"Monitor GitLab pipelines.",
		"Wait for all checks to pass.",
		"Run the test suite before merging.",
		"Rerun regression tests.",
		"Address reviewer comments.",
		"Obtain review approval.",
		"Merge this pull request.",
		"Merge after CI passes.",
		"Extend the typed API and verify GitHub checks.",
		"Extend `Pace` and verify GitHub checks.",
	} {
		t.Run(item, func(t *testing.T) {
			text := nextLead + "## Next\n\n- [ ] " + item + "\n"
			found := JudgeText(text, KindChangeDescription)
			got := matches(found, RuleNextHousekeeping)
			if !slices.Equal(got, []string{"7:[ ] " + item}) {
				t.Fatalf("findings = %q, want the administrative item on line 7", got)
			}
			for _, f := range found {
				if f.Rule == RuleNextHousekeeping && (f.Decision != DecisionDeny || f.Code != "PRF1006") {
					t.Fatalf("finding = %+v, want a PRF1006 denial", f)
				}
			}
		})
	}
}

func TestNextHousekeepingAllowsSubstantiveFollowUps(t *testing.T) {
	for _, item := range []string{
		"Extend record arguments and returns with typed Buzz declarations.",
		"Generate shared Result returns for fallible library calls.",
		"Route the CLI through the shared recorder and remove its duplicate renderer.",
		"Add CI coverage for a Windows backend.",
		"Replace GitHub checks with a provider-independent validation API.",
		"Implement review approval persistence across server restarts.",
		"Review the architecture for a streaming renderer.",
		"Review the approval persistence design before adding delegation.",
		"Review GitHub checks architecture for custom providers.",
		"Verify CI behavior against the proposed executor model.",
		"Compare encoder designs with upstream implementations.",
		"Add a helper named `verify CI results` for the status client.",
	} {
		t.Run(item, func(t *testing.T) {
			got := matches(JudgeText(nextLead+"## Next\n\n- "+item+"\n", KindChangeDescription), RuleNextHousekeeping)
			if len(got) != 0 {
				t.Fatalf("substantive follow-up reported: %q", got)
			}
		})
	}
}

func TestNextHousekeepingUsesSectionAndProseBoundaries(t *testing.T) {
	for _, heading := range []string{"Next", "Next steps", "Later", "Follow-up", "Follow-ups", "NEXT"} {
		t.Run(heading, func(t *testing.T) {
			got := matches(JudgeText(nextLead+"## "+heading+"\n\n- Verify CI results.\n", KindChangeDescription), RuleNextHousekeeping)
			if !slices.Equal(got, []string{"7:Verify CI results."}) {
				t.Fatalf("findings = %q", got)
			}
		})
	}
	for name, body := range map[string]string{
		"no follow-up section": "- Verify CI results.\n",
		"validation section":   "## Validation\n\n- Verify CI results.\n",
		"section ended":        "## Next\n\n- Extend the typed API.\n\n## Validation\n\n- Verify CI results.\n",
		"fenced example":       "```markdown\n## Next\n\n- Verify CI results.\n```\n",
		"quoted example":       "> ## Next\n>\n> - Verify CI results.\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got := matches(JudgeText(nextLead+body, KindChangeDescription), RuleNextHousekeeping); len(got) != 0 {
				t.Fatalf("findings = %q", got)
			}
		})
	}
	got := matches(JudgeText(nextLead+"## Next\n\n### Before publishing\n\n- Verify CI results.\n", KindChangeDescription), RuleNextHousekeeping)
	if !slices.Equal(got, []string{"9:Verify CI results."}) {
		t.Fatalf("nested section findings = %q", got)
	}
}
