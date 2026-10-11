package proofread

var (
	suppressChecks []check
	suppressTexts  = map[Rule]ruleText{}
)

// suppress drops the findings an inline suppression in the judged text covers.
func (in input) suppress(found []Finding) []Finding { return found }
