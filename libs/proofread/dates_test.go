package proofread

import (
	"slices"
	"testing"
)

func TestDateFormatReportsTheDatesOutsideTheTextsStyle(t *testing.T) {
	runTextCases(t, RuleDateFormat, []textCase{
		{"one style", KindReference, "Shipped Jan 2022, then Mar 2023 and Dec 2024.", nil},
		{"ISO at two precisions is one style", KindChangelog, "## 0.5.0 - 2026-10-13\n\n- Ships the 2026-09 plan.", nil},
		{"a name with and without its day", KindReference, "On October 13, 2026 and in September 2026.", nil},
		// docs/decisions/0001: the in-repo mix.
		{"a name among ISO dates", KindReference,
			"Decided 2026-09-19.\n\nRevisited 2026-10-01 after the Sept 2026 audit.",
			[]string{"3:Sept 2026"}},
		{"a numeric date", KindReference, "From Jan 2022 to Mar 2023, then 10/13/2026.", []string{"1:10/13/2026"}},
		{"full names among abbreviations", KindReference, "Jan 2022, Mar 2023, October 2024.", []string{"1:October 2024"}},
		{"May takes no style", KindReference, "May 2022, then October 2024.", nil},
		{"Sep beside Sept", KindReference, "Sept 2022, Sept 2023, Sep 2024.", []string{"1:Sep 2024"}},
		{"code is not read", KindReference, "Jan 2022 and `2024-01-02`.", nil},
		{"a year range is no date", KindReference, "Jan 2022, then 2019-2021.", nil},
		{"one date", KindReference, "Since 2026-10-13.", nil},
	})
}

func TestDashLetsADateRangeStand(t *testing.T) {
	runTextCases(t, RuleDash, []textCase{
		{"a hyphen", KindReference, "Acme, Jan 2020 - Present.", nil},
		{"years", KindReference, "Beta, 2019-2021.", nil},
		{"a double hyphen", KindReference, "Acme, Jan 2020 -- Present.", nil},
		{"a double hyphen between years", KindReference, "Beta, 2019 -- 2021.", nil},
		{"a double hyphen after a number is an aside", KindReference, "Run step 2 -- now it builds.",
			[]string{"1:2 -- now"}},
		{"an en dash is still not ASCII", KindReference, "Acme, Jan 2020 – Present.", []string{"1: – "}},
	})

	got := fixes("Acme, Jan 2020 – Present.", KindReference, RuleDash)
	if want := []string{" – => - "}; !slices.Equal(got, want) {
		t.Errorf("dash fixes: got %q, want %q", got, want)
	}
}
