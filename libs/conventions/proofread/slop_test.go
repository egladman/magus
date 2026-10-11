package proofread

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestSlopChecksRunInOrder(t *testing.T) {
	want := []Rule{
		RuleSignpost, RuleChatbot, RuleLeak, RuleBuzzword, RuleBuzzwordWeak, RuleContrast, RuleVague,
		RuleCloser, RuleIngTail, RuleStaccato, RuleDash, RuleASCII, RuleHeadingCase,
	}

	if got := ruleNames(slopChecks); !reflect.DeepEqual(got, want) {
		t.Errorf("slopChecks = %q, want %q", got, want)
	}
}

// decisions lists a rule's findings for text by the kind judged, so one case
// pins which kinds judge a tell and what each costs.
func decisions(rule Rule, text string, opts []Option, kinds ...Kind) map[Kind]Decision {
	out := map[Kind]Decision{}

	for _, kind := range kinds {
		for _, f := range JudgeText(text, kind, opts...) {
			if f.Rule == rule {
				out[kind] = f.Decision
			}
		}
	}

	return out
}

func TestSlopRulesJudgeTheKindsAndDecisionsTheyOwn(t *testing.T) {
	every := []Kind{
		KindDocComment, KindReference, KindGuide, KindChangeDescription, KindAgentInstructions, KindReviewReply,
	}
	houseDash := []Option{WithDecisions(map[Rule]Decision{RuleDash: DecisionDeny})}

	cases := []struct {
		name string
		rule Rule
		text string
		opts []Option
		want map[Kind]Decision
	}{
		{"a tell a reply carries too", RuleSignpost, "Make no mistake, the key sorts.", nil, map[Kind]Decision{
			KindReference: DecisionDeny, KindGuide: DecisionDeny, KindChangeDescription: DecisionDeny,
			KindAgentInstructions: DecisionDeny, KindReviewReply: DecisionDeny,
		}},
		{"residue is judged in a doc comment too", RuleLeak, "The key sorts. TBD.", nil, map[Kind]Decision{
			KindDocComment: DecisionDeny, KindReference: DecisionDeny, KindGuide: DecisionDeny,
			KindChangeDescription: DecisionDeny, KindAgentInstructions: DecisionDeny, KindReviewReply: DecisionDeny,
		}},
		{"a contrast denies only in a change description", RuleContrast,
			"It is not just a cache; it is the ledger.", nil, map[Kind]Decision{
				KindReference: DecisionAdvise, KindGuide: DecisionAdvise, KindChangeDescription: DecisionDeny,
				KindAgentInstructions: DecisionAdvise,
			}},
		{"staccato leaves a guide and agent instructions their short imperatives", RuleStaccato,
			"It is fast. It is small. It is stable.", nil,
			map[Kind]Decision{KindReference: DecisionAdvise, KindChangeDescription: DecisionDeny}},
		{"a weak buzzword always advises", RuleBuzzwordWeak, "The landscape is stable.", nil, map[Kind]Decision{
			KindReference: DecisionAdvise, KindGuide: DecisionAdvise, KindChangeDescription: DecisionAdvise,
			KindAgentInstructions: DecisionAdvise, KindReviewReply: DecisionAdvise,
		}},
		{"a participle tail always advises", RuleIngTail, "The cache stores blobs, showcasing the key.", nil,
			map[Kind]Decision{
				KindReference: DecisionAdvise, KindGuide: DecisionAdvise, KindChangeDescription: DecisionAdvise,
				KindAgentInstructions: DecisionAdvise,
			}},
		{"a dash is house style, off with no table", RuleDash, "The key sorts — then hashes.", nil,
			map[Kind]Decision{}},
		{"a dash a table turns on is judged wherever it is prose", RuleDash, "The key sorts — then hashes.",
			houseDash, map[Kind]Decision{
				KindReference: DecisionDeny, KindGuide: DecisionDeny, KindChangeDescription: DecisionDeny,
				KindAgentInstructions: DecisionDeny, KindReviewReply: DecisionDeny,
			}},
		{"a heading has no change description", RuleHeadingCase, "## Cache Key Derivation Rules", nil,
			map[Kind]Decision{
				KindReference: DecisionAdvise, KindGuide: DecisionAdvise, KindAgentInstructions: DecisionAdvise,
			}},
		{"a letter is a change description's alone", RuleChatbot, "I am writing to propose a change.", nil,
			map[Kind]Decision{KindChangeDescription: DecisionDeny}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := decisions(tc.rule, tc.text, tc.opts, every...); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%s judged %v, want %v", tc.rule, got, tc.want)
			}
		})
	}
}

func TestSignpostReportsAnAnnouncementInPlaceOfThePoint(t *testing.T) {
	runTextCases(t, RuleSignpost, []textCase{
		{"the phrase mentioned in code", KindReference, "The `Here's the thing` opener is the phrase the rule refuses.", nil},
		// docs/concepts/compatibility.md: the page is the antecedent, not an essay.
		{"the rest of this page", KindReference, "The rest of this page names the flags. Let's Encrypt certificates are cached.", nil},
		{"a quoted phrase", KindReference, "The reviewer wrote \"Make no mistake\" and the rule left the quote alone.", nil},
		{"here's the thing", KindReference, "Here's the thing: the cache races on the key.", []string{"1:Here's the thing"}},
		{"let's dive", KindReference, "Let's dive into how the key is derived.", []string{"1:Let's dive"}},
		{"make no mistake", KindChangeDescription, pr("- Make no mistake. The key sorts its inputs."), []string{"4:Make no mistake"}},
		{"mid-sentence", KindReference, "The key sorts. Honestly, it hashes. It then stops, and I promise it keeps.",
			[]string{"1:Honestly,", "1:I promise"}},
		{"after emphasis", KindReference, "**Look,** the key sorts.", []string{"1:Look,"}},
		{"a reply", KindReviewReply, "It turns out the key races.", []string{"1:It turns out"}},
	})
}

func TestChatbotReportsTextAnsweringAChatTheReaderNeverSaw(t *testing.T) {
	runTextCases(t, RuleChatbot, []textCase{
		{"the phrase as data", KindReference, "The model's answer is cached under `I hope this helps`, a literal key.", nil},
		{"let the holder know", KindReference, "Ask the caller to let the lock holder know when the run ends.", nil},
		{"a quoted reply", KindReference, "The reviewer wrote \"you're absolutely right\" and closed the thread.", nil},
		{"an offer", KindChangeDescription, pr("I hope this helps! Let me know if you want me to expand."),
			[]string{"4:I hope this helps", "4:Let me know if", "4:want me to"}},
		{"an opener", KindReference, "Certainly! The key sorts its inputs.", []string{"1:Certainly!"}},
		{"a disclaimer", KindReference, "As an AI language model, I cannot run the suite.", []string{"1:As an AI language model"}},
		{"flattery in a reply", KindReviewReply, "Great question, the key sorts.", []string{"1:Great question"}},
		{"a cutoff", KindReference, "As of my last training the cache was off.", []string{"1:As of my last training"}},
		// A letter is a legitimate subject on a page.
		{"a salutation on a page", KindReference, "Dear Maintainers: the page opens a letter.", nil},
		{"a terse bullet", KindChangeDescription, pr("- Pins the key."), nil},
		{"a salutation in code", KindChangeDescription, pr("- Documents the `Dear X:` salutation the template used."), nil},
		{"i am writing to", KindChangeDescription, pr("I am writing to propose a change to the key."), []string{"4:I am writing to"}},
		{"dear", KindChangeDescription, pr("Dear Reviewers: please see below."), []string{"4:Dear Reviewers:"}},
		{"i understand the concern", KindChangeDescription, pr("I understand the concern about the cap."),
			[]string{"4:I understand the concern"}},
	})
}

func TestLeakReportsResidueOfAToolOrATemplate(t *testing.T) {
	runTextCases(t, RuleLeak, []textCase{
		{"a field named after a marker", KindReference, "A `contentReference` field names the citation.", nil},
		// docs/guides/integrations/git.md: a link reference label, not a placeholder.
		{"a link label", KindReference, "Read [Your own hooks, in Buzz] first.", nil},
		{"a real date", KindReference, "Set the date to 2025-10-09.", nil},
		{"a citation marker", KindChangeDescription, pr("- Pins the key.【4:0†source】"),
			[]string{"4:【4:0†source】"}},
		{"an unfilled placeholder", KindChangeDescription, pr("- Owner: [insert team name]"), []string{"4:[insert team name]"}},
		{"an unfilled date", KindReference, "Released on 2025-xx-xx.", []string{"1:2025-xx-xx"}},
		{"a turn id", KindReference, "See turn0search1 for the source.", []string{"1:turn0search1"}},
		{"a doc comment", KindDocComment, "Pins the key. TBD.", []string{"1:TBD"}},
		{"a reply", KindReviewReply, "Done, citeturn0search0.", []string{"1:citeturn"}},
	})
}

func TestBuzzwordReportsAWordChosenToSoundSignificant(t *testing.T) {
	runTextCases(t, RuleBuzzword, []textCase{
		{"a noun meaning interaction", KindReference, "Caching interplay between workers is described below.", nil},
		{"a verb in the plain sense", KindReference, "Pivot the table on the first column.", nil},
		{"a quoted word", KindReference, "The reviewer wrote \"delve\" and the rule left it alone.", nil},
		{"delves", KindReference, "This change delves into the cache.", []string{"1:delves"}},
		{"two in a bullet", KindChangeDescription, pr("- The key is a vibrant tapestry of inputs."), []string{"4:vibrant", "4:tapestry"}},
		{"moving forward", KindReference, "Moving forward, the key sorts its inputs.", []string{"1:Moving forward"}},
		{"a phrase read whole", KindReference, "The cache plays a vital role. It serves as a testament to the key.",
			[]string{"1:plays a vital role", "1:serves as a testament"}},
		{"a reply", KindReviewReply, "Let me delve into the race.", []string{"1:delve"}},
		{"commendable", KindChangeDescription, pr("- A commendable fix to the key."), []string{"4:commendable"}},
		{"underscore as a verb", KindReference, "This underscores the need for a stable key.",
			[]string{"1:underscores the"}},
		{"underscore after a modal", KindReviewReply, "It would underscore how the key races.",
			[]string{"1:would underscore how"}},
		{"underscore the character", KindReference, "A leading underscore marks the field private.", nil},
		{"underscores replaced", KindReference, "Replace underscores with hyphens in the name.", nil},
		{"an underscore before a determiner", KindReference, "Join the words with an underscore the way Go does.", nil},
		{"an identifier", KindReference, "Set `underscores_the_key` or underscore_the_value.", nil},
	})
}

func TestBuzzwordWeakReportsWordsThatAlsoHaveAPlainSense(t *testing.T) {
	runTextCases(t, RuleBuzzwordWeak, []textCase{
		{"the cache key", KindReference, "The cache key sorts its inputs, and a target's key is stable.", nil},
		{"a tolerance", KindReference, "The valid range is 1 to 5.", nil},
		{"quoted", KindReference, "The word \"crucial\" is quoted.", nil},
		{"crucial", KindReference, "The key is crucial here.", []string{"1:crucial"}},
		{"a key factor", KindChangeDescription, pr("- Names the key factor in the cap."), []string{"4:key factor"}},
		{"landscape and valuable", KindReference, "A valuable report of the landscape.", []string{"1:valuable", "1:landscape"}},
		{"additionally", KindReference, "The key sorts. Additionally, it hashes.", []string{"1:Additionally"}},
		{"mid-sentence additionally", KindReference, "It is, additionally, stable.", nil},
		{"left to the error tier", KindReference, "It plays a key role.", nil},
		{"notably", KindChangeDescription, pr("- Notably, the key sorts."), []string{"4:Notably"}},
		{"the realm of", KindReference, "It is in the realm of caching.", []string{"1:in the realm of"}},
		{"an authentication realm", KindReference, "Set the realm to EXAMPLE.COM; each realm holds its users.", nil},
		{"a realm in code", KindReference, "Read `realm_of_user` from `notably`.", nil},
	})
}

func TestContrastReportsAClaimMadeByDenyingItsOpposite(t *testing.T) {
	runTextCases(t, RuleContrast, []textCase{
		{"a plain claim", KindChangeDescription, pr("- Pins the key so a replay hits."), nil},
		{"not every", KindReference, "Not every key is stable.", nil},
		{"a negation with its reason", KindReference, "The sandbox is not a container; the guard page says why.", nil},
		{"not just; it is", KindChangeDescription, pr("- It is not just a cache; it is the ledger."),
			[]string{"4:It is not just a cache; it is"}},
		{"not because", KindChangeDescription, pr("- Not because the test is slow. Because the key races."),
			[]string{"4:Not because the test is slow. Because"}},
		{"isn't; it's", KindChangeDescription, pr("- The run isn't flaky, it's racy."), []string{"4:isn't flaky, it's"}},
		{"not only but also", KindReference, "It is not only a cache but also a ledger.", []string{"1:not only a cache but also"}},
		{"across sentences", KindReference, "This is not a loophole. It is the point of the version.",
			[]string{"1:This is not a loophole. It is"}},
		{"no, no, just", KindReference, "It has no flags, no config, just a path.", []string{"1:no flags, no config, just"}},
		{"a tailing negation", KindReference, "It hashes the inputs, no guessing.", []string{"1:, no guessing."}},
		{"a quoted pair", KindReference, "The rule refuses \"it is not just X; it is Y\".", nil},
	})
}

func TestVagueReportsWeightOrConsensusWithNothingNamed(t *testing.T) {
	runTextCases(t, RuleVague, []textCase{
		{"a measured claim", KindReference, "The benchmark shows a 12% speedup on the Go benchmarks.", nil},
		{"stakes that are named", KindReference, "The stakes are a lost cache entry and a rebuild.", nil},
		{"reviewers", KindReference, "Reviewers argue about names in the thread; the page records the winner.", nil},
		{"the stakes", KindReference, "The stakes are high.", []string{"1:The stakes are high"}},
		{"experts", KindReference, "Experts argue the key should sort.", []string{"1:Experts argue"}},
		{"the future", KindReference, "The future looks bright for the cache.", []string{"1:The future looks bright"}},
		{"despite challenges", KindChangeDescription, pr("- Despite these challenges the key holds."),
			[]string{"4:Despite these challenges"}},
		{"people tend to", KindReviewReply, "People tend to forget the lock.", []string{"1:People tend to"}},
	})
}

func TestCloserReportsASentenceAnnouncingItRestatesTheText(t *testing.T) {
	runTextCases(t, RuleCloser, []textCase{
		{"a summary as a noun", KindReference, "A summary of the flags follows.", nil},
		{"a conclusion as a noun", KindReference, "The conclusion of the run is a verdict.", nil},
		{"overall", KindReference, "Overall, the lint passes.", nil},
		{"in conclusion", KindReference, "In conclusion, the key sorts its inputs.", []string{"1:In conclusion,"}},
		{"to summarize", KindReference, "The key sorts. To summarize, it hashes.", []string{"1:To summarize,"}},
		{"in summary", KindChangeDescription, pr("- Sorts the key. In summary, replays hit."), []string{"4:In summary,"}},
		{"a reply", KindReviewReply, "All in all, the key sorts.", []string{"1:All in all,"}},
	})
}

func TestIngTailReportsAParticipleClaimingSignificance(t *testing.T) {
	runTextCases(t, RuleIngTail, []textCase{
		{"a following action", KindReference, "The key sorts its inputs, then hashes them.", nil},
		{"a progressive", KindReference, "The sorter is reflecting the input order back to the caller in a test.", nil},
		// internal/render/target_graph.go: the participle states a postcondition.
		{"ensuring", KindReference, "Hash the inputs, ensuring the lock is released.", nil},
		{"highlighting", KindReference, "The cache stores blobs, highlighting the importance of the key.", []string{"1:, highlighting"}},
		{"underscoring", KindReference, "The key sorts, underscoring the design.", []string{"1:, underscoring"}},
		{"showcasing", KindChangeDescription, pr("- Sorts the inputs, showcasing the new hasher."), []string{"4:, showcasing"}},
	})
}

func TestStaccatoReportsARunOfShortSentences(t *testing.T) {
	runTextCases(t, RuleStaccato, []textCase{
		{"two short then long", KindReference,
			"It reads the key. It sorts it. Then the hash runs over the sorted bytes in one pass.", nil},
		{"a bullet of fragments", KindReference, "- Fast. Small. Stable.", nil},
		{"one short for emphasis", KindReference,
			"Speed matters here, so the key sorts its inputs before hashing them, and the replay hits.", nil},
		{"a run of six, three and five", KindReference,
			"It had no preference for symmetry. No aesthetic prior. No nostalgia for human taste.", []string{"1:"}},
		{"four short", KindReference, "The cache is fast. The key is stable. Replays hit. Nothing else changed.", []string{"1:"}},
		{"docs/scope.md", KindReference, "magus does not decide for you. It answers questions. You decide.", []string{"1:"}},
		{"reported once, at the run", KindReference,
			"The sorter walks every input and keeps the order it found them in on disk.\nIt is fast. It is small. " +
				"It is stable. It is done.\n", []string{"2:"}},
		{"a code span is one word", KindReference, "Run `magus run go-build .` now. Then `magus affected ci` too. It passes.",
			[]string{"1:"}},
		{"a heading", KindReference, "## Fast. Small. Stable.", nil},
		// docs/guides/setup.md: a bold step number opens the item.
		{"a step number", KindReference, "**3. List.** See what a repository holds.", nil},
		{"a guide", KindGuide, "Run it. Add a target. Check the key.", nil},
		{"agent instructions", KindAgentInstructions, "Run it. Add a target. Check the key.", nil},
	})
}

func TestDashReportsADashBetweenWords(t *testing.T) {
	runTextCases(t, RuleDash, []textCase{
		{"a semicolon", KindReference, "Sort the inputs; then hash them.", nil},
		{"a flag in code", KindReference, "Run `magus run -- -run TestX` to narrow the test.", nil},
		{"a plain range", KindReference, "The range is 10 to 20.", nil},
		{"a hyphenated word", KindReference, "A cache-key is stable - see below.", nil},
		{"an em dash", KindReference, "The key sorts its inputs — then it hashes them.", []string{"1: — "}},
		{"a double hyphen", KindChangeDescription, pr("- Sorts the inputs -- and hashes them."), []string{"4:inputs -- and"}},
		{"an en dash", KindReference, "Pages 10–20 cover it.", []string{"1:–"}},
		{"a reply", KindReviewReply, "Done — pushed.", []string{"1: — "}},
	})
}

// fixes renders each finding of rule as `match=>replacements`, the
// replacements joined by "|".
func fixes(text string, kind Kind, rule Rule) []string {
	var out []string

	for _, f := range JudgeText(text, kind, houseOn) {
		if f.Rule == rule {
			out = append(out, f.Match+"=>"+strings.Join(f.Replacements, "|"))
		}
	}

	return out
}

func TestSubstitutionRulesOfferTheirReplacement(t *testing.T) {
	cases := []struct {
		name string
		rule Rule
		text string
		want []string
	}{
		{"a dash before a joined clause", RuleDash, "The key sorts its inputs — then it hashes them.",
			[]string{" — =>, "}},
		{"a dash introducing", RuleDash, "Done — pushed.", []string{" — =>: "}},
		{"a pair of dashes", RuleDash, "The key—sorted once—is stable.", []string{"—=>, ", "—=>, "}},
		{"a range", RuleDash, "Pages 10–20 cover it.", []string{"–=>-"}},
		{"a double hyphen", RuleDash, "Sort the inputs -- and hash them.", []string{"inputs -- and=>inputs, and"}},
		{"curly quotes", RuleASCII, "He said “stable” and it’s so…", []string{"“=>\"", "”=>\"", "’=>'", "…=>..."}},
		{"an emoji has no ASCII", RuleASCII, "Launch \U0001F680 now.", []string{"\U0001F680=>"}},
		{"a title-cased heading", RuleHeadingCase, "## Strategic Negotiations And Global Partnerships",
			[]string{"Strategic Negotiations And Global Partnerships=>Strategic negotiations and global partnerships"}},
		{"names keep their case", RuleHeadingCase, "## Running The Command On GitHub With API Keys",
			[]string{"Running The Command On GitHub With API Keys=>Running the command on GitHub with API keys"}},
		{"a filler word is deleted", RuleFiller, "The key is truly stable.", []string{"truly=>"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fixes(tc.text, KindReference, tc.rule); !slices.Equal(got, tc.want) {
				t.Errorf("%s fixes:\n got %q\nwant %q", tc.rule, got, tc.want)
			}
		})
	}
}

func TestOnlySubstitutionRulesOfferReplacements(t *testing.T) {
	substitutes := []Rule{RuleWordy, RuleFiller, RuleTerms, RuleDash, RuleASCII, RuleHeadingCase}
	text := pr("- Whoever wrote this was sloppy; it is crucial, and it has never fired.\n" +
		"- It plays a vital role. Here's the thing: it is not just a cache, it is a ledger.\n" +
		"- Note that it is able to sort — then hash.\n")

	for _, kind := range []Kind{KindChangeDescription, KindReference, KindReviewReply} {
		for _, f := range JudgeText(text, kind, houseOn) {
			if f.Replacements != nil && !slices.Contains(substitutes, f.Rule) {
				t.Errorf("%s offers %q on %s", f.Rule, f.Replacements, kind)
			}
		}
	}
}

func TestASCIIReportsACurlyMarkAnEllipsisOrAnEmoji(t *testing.T) {
	runTextCases(t, RuleASCII, []textCase{
		{"straight marks", KindReference, "Quote the value with straight marks and write three dots as ...", nil},
		{"curly in code", KindReference, "Use `“quoted”` in a code span.", nil},
		// docs/concepts/spells.md: an arrow has no plain spelling.
		{"an arrow", KindReference, "The arrow → stays: it has no plain spelling.", nil},
		{"curly quotes", KindReference, "He said “the key is stable”.", []string{"1:“", "1:”"}},
		{"an ellipsis", KindReference, "Pins the key … and hashes it.", []string{"1:…"}},
		{"an emoji", KindReference, "Launch phase \U0001F680: the key sorts.", []string{"1:\U0001F680"}},
		{"a curly apostrophe", KindChangeDescription, pr("- Pins the key’s inputs."), []string{"4:’"}},
		{"a check mark", KindReviewReply, "Done ✅", []string{"1:✅"}},
	})
}

func TestHeadingCaseReportsATitleCasedHeading(t *testing.T) {
	runTextCases(t, RuleHeadingCase, []textCase{
		{"sentence case", KindReference, "## Strategic negotiations and global partnerships", nil},
		{"three words", KindReference, "## Cache key derivation", nil},
		{"a proper noun among lower case", KindReference, "## Run the Buzz formatter", nil},
		{"a code span", KindReference, "## The `Magus Run` Command", nil},
		{"title case", KindReference, "## Strategic Negotiations And Global Partnerships",
			[]string{"1:Strategic Negotiations And Global Partnerships"}},
		{"four words", KindReference, "## Cache Key Derivation Rules", []string{"1:Cache Key Derivation Rules"}},
		{"in a guide", KindGuide, "## Handling Stale Cache Entries", []string{"1:Handling Stale Cache Entries"}},
		{"not a heading", KindReference, "Handling Stale Cache Entries", nil},
	})
}

func TestFillerReportsTheSlopPhrases(t *testing.T) {
	runTextCases(t, RuleFiller, []textCase{
		{"a literal flag", KindReference, "The `deeply` flag is a literal name.", nil},
		{"a size", KindReference, "At this size the cache holds 4 entries.", nil},
		{"truncate", KindReference, "Hash the inputs, truncate to 12 bytes.", nil},
		{"truly", KindReference, "The key is truly stable.", []string{"1:truly"}},
		{"at its core", KindReference, "At its core, the key sorts its inputs.", []string{"1:At its core"}},
		{"critical to remember", KindChangeDescription, pr("- The cap is critical to remember."), []string{"4:critical to remember"}},
		{"selling words", KindReference, "A powerful, elegant, cutting-edge and effortless cache.",
			[]string{"1:powerful", "1:elegant", "1:cutting-edge", "1:effortless"}},
		{"state of the art", KindReference, "A state-of-the-art, world-class hasher.", []string{"1:state-of-the-art", "1:world-class"}},
		{"when it comes to", KindChangeDescription, pr("- When it comes to hashing, the key sorts."), []string{"4:When it comes to"}},
		{"a doc keeps its list", KindDocComment, "At its core, a powerful cache.", nil},
	})
}
