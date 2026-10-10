package prose

import (
	"reflect"
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

// severities lists a rule's findings for text by the kind judged, so one case
// pins which kinds judge a tell and what each costs.
func severities(rule Rule, text string, kinds ...Kind) map[Kind]Severity {
	out := map[Kind]Severity{}

	for _, kind := range kinds {
		for _, f := range JudgeText(text, kind) {
			if f.Rule == rule {
				out[kind] = f.Severity
			}
		}
	}

	return out
}

func TestSlopRulesJudgeTheKindsAndSeveritiesTheyOwn(t *testing.T) {
	every := []Kind{KindDoc, KindMarkdown, KindGuide, KindPullRequest, KindSkill, KindReply}

	cases := []struct {
		name string
		rule Rule
		text string
		want map[Kind]Severity
	}{
		{"a tell a reply carries too", RuleSignpost, "Make no mistake, the key sorts.", map[Kind]Severity{
			KindMarkdown: SeverityError, KindGuide: SeverityError, KindPullRequest: SeverityError,
			KindSkill: SeverityError, KindReply: SeverityError,
		}},
		{"residue is judged in a doc comment too", RuleLeak, "The key sorts. TBD.", map[Kind]Severity{
			KindDoc: SeverityError, KindMarkdown: SeverityError, KindGuide: SeverityError,
			KindPullRequest: SeverityError, KindSkill: SeverityError, KindReply: SeverityError,
		}},
		{"a contrast is an error only in a pull request", RuleContrast, "It is not just a cache; it is the ledger.",
			map[Kind]Severity{
				KindMarkdown: SeverityAdvisory, KindGuide: SeverityAdvisory, KindPullRequest: SeverityError,
				KindSkill: SeverityAdvisory,
			}},
		{"staccato leaves a guide and a skill their short imperatives", RuleStaccato,
			"It is fast. It is small. It is stable.",
			map[Kind]Severity{KindMarkdown: SeverityAdvisory, KindPullRequest: SeverityError}},
		{"a weak buzzword is always advisory", RuleBuzzwordWeak, "The landscape is stable.", map[Kind]Severity{
			KindMarkdown: SeverityAdvisory, KindGuide: SeverityAdvisory, KindPullRequest: SeverityAdvisory,
			KindSkill: SeverityAdvisory, KindReply: SeverityAdvisory,
		}},
		{"a participle tail is always advisory", RuleIngTail, "The cache stores blobs, showcasing the key.",
			map[Kind]Severity{
				KindMarkdown: SeverityAdvisory, KindGuide: SeverityAdvisory, KindPullRequest: SeverityAdvisory,
				KindSkill: SeverityAdvisory,
			}},
		{"a dash is an error wherever it is prose", RuleDash, "The key sorts — then hashes.", map[Kind]Severity{
			KindMarkdown: SeverityError, KindGuide: SeverityError, KindPullRequest: SeverityError,
			KindSkill: SeverityError, KindReply: SeverityError,
		}},
		{"a heading has no pull request", RuleHeadingCase, "## Cache Key Derivation Rules", map[Kind]Severity{
			KindMarkdown: SeverityAdvisory, KindGuide: SeverityAdvisory, KindSkill: SeverityAdvisory,
		}},
		{"a letter is a pull request's alone", RuleChatbot, "I am writing to propose a change.", map[Kind]Severity{
			KindPullRequest: SeverityError,
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := severities(tc.rule, tc.text, every...); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%s judged %v, want %v", tc.rule, got, tc.want)
			}
		})
	}
}

func TestSignpostReportsAnAnnouncementInPlaceOfThePoint(t *testing.T) {
	runTextCases(t, RuleSignpost, []textCase{
		{"the phrase mentioned in code", KindMarkdown, "The `Here's the thing` opener is the phrase the rule refuses.", nil},
		// docs/concepts/compatibility.md: the page is the antecedent, not an essay.
		{"the rest of this page", KindMarkdown, "The rest of this page names the flags. Let's Encrypt certificates are cached.", nil},
		{"a quoted phrase", KindMarkdown, "The reviewer wrote \"Make no mistake\" and the rule left the quote alone.", nil},
		{"here's the thing", KindMarkdown, "Here's the thing: the cache races on the key.", []string{"1:Here's the thing"}},
		{"let's dive", KindMarkdown, "Let's dive into how the key is derived.", []string{"1:Let's dive"}},
		{"make no mistake", KindPullRequest, pr("- Make no mistake. The key sorts its inputs."), []string{"4:Make no mistake"}},
		{"mid-sentence", KindMarkdown, "The key sorts. Honestly, it hashes. It then stops, and I promise it keeps.",
			[]string{"1:Honestly,", "1:I promise"}},
		{"after emphasis", KindMarkdown, "**Look,** the key sorts.", []string{"1:Look,"}},
		{"a reply", KindReply, "It turns out the key races.", []string{"1:It turns out"}},
	})
}

func TestChatbotReportsTextAnsweringAChatTheReaderNeverSaw(t *testing.T) {
	runTextCases(t, RuleChatbot, []textCase{
		{"the phrase as data", KindMarkdown, "The model's answer is cached under `I hope this helps`, a literal key.", nil},
		{"let the holder know", KindMarkdown, "Ask the caller to let the lock holder know when the run ends.", nil},
		{"a quoted reply", KindMarkdown, "The reviewer wrote \"you're absolutely right\" and closed the thread.", nil},
		{"an offer", KindPullRequest, pr("I hope this helps! Let me know if you want me to expand."),
			[]string{"4:I hope this helps", "4:Let me know if", "4:want me to"}},
		{"an opener", KindMarkdown, "Certainly! The key sorts its inputs.", []string{"1:Certainly!"}},
		{"a disclaimer", KindMarkdown, "As an AI language model, I cannot run the suite.", []string{"1:As an AI language model"}},
		{"flattery in a reply", KindReply, "Great question, the key sorts.", []string{"1:Great question"}},
		{"a cutoff", KindMarkdown, "As of my last training the cache was off.", []string{"1:As of my last training"}},
		// A letter is a legitimate subject on a page.
		{"a salutation on a page", KindMarkdown, "Dear Maintainers: the page opens a letter.", nil},
		{"a terse bullet", KindPullRequest, pr("- Pins the key."), nil},
		{"a salutation in code", KindPullRequest, pr("- Documents the `Dear X:` salutation the template used."), nil},
		{"i am writing to", KindPullRequest, pr("I am writing to propose a change to the key."), []string{"4:I am writing to"}},
		{"dear", KindPullRequest, pr("Dear Reviewers: please see below."), []string{"4:Dear Reviewers:"}},
		{"i understand the concern", KindPullRequest, pr("I understand the concern about the cap."),
			[]string{"4:I understand the concern"}},
	})
}

func TestLeakReportsResidueOfAToolOrATemplate(t *testing.T) {
	runTextCases(t, RuleLeak, []textCase{
		{"a field named after a marker", KindMarkdown, "A `contentReference` field names the citation.", nil},
		// docs/guides/integrations/git.md: a link reference label, not a placeholder.
		{"a link label", KindMarkdown, "Read [Your own hooks, in Buzz] first.", nil},
		{"a real date", KindMarkdown, "Set the date to 2025-10-09.", nil},
		{"a citation marker", KindPullRequest, pr("- Pins the key.【4:0†source】"),
			[]string{"4:【4:0†source】"}},
		{"an unfilled placeholder", KindPullRequest, pr("- Owner: [insert team name]"), []string{"4:[insert team name]"}},
		{"an unfilled date", KindMarkdown, "Released on 2025-xx-xx.", []string{"1:2025-xx-xx"}},
		{"a turn id", KindMarkdown, "See turn0search1 for the source.", []string{"1:turn0search1"}},
		{"a doc comment", KindDoc, "Pins the key. TBD.", []string{"1:TBD"}},
		{"a reply", KindReply, "Done, citeturn0search0.", []string{"1:citeturn"}},
	})
}

func TestBuzzwordReportsAWordChosenToSoundSignificant(t *testing.T) {
	runTextCases(t, RuleBuzzword, []textCase{
		{"a noun meaning interaction", KindMarkdown, "Caching interplay between workers is described below.", nil},
		{"a verb in the plain sense", KindMarkdown, "Pivot the table on the first column.", nil},
		{"a quoted word", KindMarkdown, "The reviewer wrote \"delve\" and the rule left it alone.", nil},
		{"delves", KindMarkdown, "This change delves into the cache.", []string{"1:delves"}},
		{"two in a bullet", KindPullRequest, pr("- The key is a vibrant tapestry of inputs."), []string{"4:vibrant", "4:tapestry"}},
		{"moving forward", KindMarkdown, "Moving forward, the key sorts its inputs.", []string{"1:Moving forward"}},
		{"a phrase read whole", KindMarkdown, "The cache plays a vital role. It serves as a testament to the key.",
			[]string{"1:plays a vital role", "1:serves as a testament"}},
		{"a reply", KindReply, "Let me delve into the race.", []string{"1:delve"}},
	})
}

func TestBuzzwordWeakReportsWordsThatAlsoHaveAPlainSense(t *testing.T) {
	runTextCases(t, RuleBuzzwordWeak, []textCase{
		{"the cache key", KindMarkdown, "The cache key sorts its inputs, and a target's key is stable.", nil},
		{"a tolerance", KindMarkdown, "The valid range is 1 to 5.", nil},
		{"quoted", KindMarkdown, "The word \"crucial\" is quoted.", nil},
		{"crucial", KindMarkdown, "The key is crucial here.", []string{"1:crucial"}},
		{"a key factor", KindPullRequest, pr("- Names the key factor in the cap."), []string{"4:key factor"}},
		{"landscape and valuable", KindMarkdown, "A valuable report of the landscape.", []string{"1:valuable", "1:landscape"}},
		{"additionally", KindMarkdown, "The key sorts. Additionally, it hashes.", []string{"1:Additionally"}},
		{"mid-sentence additionally", KindMarkdown, "It is, additionally, stable.", nil},
		{"left to the error tier", KindMarkdown, "It plays a key role.", nil},
	})
}

func TestContrastReportsAClaimMadeByDenyingItsOpposite(t *testing.T) {
	runTextCases(t, RuleContrast, []textCase{
		{"a plain claim", KindPullRequest, pr("- Pins the key so a replay hits."), nil},
		{"not every", KindMarkdown, "Not every key is stable.", nil},
		{"a negation with its reason", KindMarkdown, "The sandbox is not a container; the guard page says why.", nil},
		{"not just; it is", KindPullRequest, pr("- It is not just a cache; it is the ledger."),
			[]string{"4:It is not just a cache; it is"}},
		{"not because", KindPullRequest, pr("- Not because the test is slow. Because the key races."),
			[]string{"4:Not because the test is slow. Because"}},
		{"isn't; it's", KindPullRequest, pr("- The run isn't flaky, it's racy."), []string{"4:isn't flaky, it's"}},
		{"not only but also", KindMarkdown, "It is not only a cache but also a ledger.", []string{"1:not only a cache but also"}},
		{"across sentences", KindMarkdown, "This is not a loophole. It is the point of the version.",
			[]string{"1:This is not a loophole. It is"}},
		{"no, no, just", KindMarkdown, "It has no flags, no config, just a path.", []string{"1:no flags, no config, just"}},
		{"a tailing negation", KindMarkdown, "It hashes the inputs, no guessing.", []string{"1:, no guessing."}},
		{"a quoted pair", KindMarkdown, "The rule refuses \"it is not just X; it is Y\".", nil},
	})
}

func TestVagueReportsWeightOrConsensusWithNothingNamed(t *testing.T) {
	runTextCases(t, RuleVague, []textCase{
		{"a measured claim", KindMarkdown, "The benchmark shows a 12% speedup on the Go corpus.", nil},
		{"stakes that are named", KindMarkdown, "The stakes are a lost cache entry and a rebuild.", nil},
		{"reviewers", KindMarkdown, "Reviewers argue about names in the thread; the page records the winner.", nil},
		{"the stakes", KindMarkdown, "The stakes are high.", []string{"1:The stakes are high"}},
		{"experts", KindMarkdown, "Experts argue the key should sort.", []string{"1:Experts argue"}},
		{"the future", KindMarkdown, "The future looks bright for the cache.", []string{"1:The future looks bright"}},
		{"despite challenges", KindPullRequest, pr("- Despite these challenges the key holds."),
			[]string{"4:Despite these challenges"}},
		{"people tend to", KindReply, "People tend to forget the lock.", []string{"1:People tend to"}},
	})
}

func TestCloserReportsASentenceAnnouncingItRestatesTheText(t *testing.T) {
	runTextCases(t, RuleCloser, []textCase{
		{"a summary as a noun", KindMarkdown, "A summary of the flags follows.", nil},
		{"a conclusion as a noun", KindMarkdown, "The conclusion of the run is a verdict.", nil},
		{"overall", KindMarkdown, "Overall, the lint passes.", nil},
		{"in conclusion", KindMarkdown, "In conclusion, the key sorts its inputs.", []string{"1:In conclusion,"}},
		{"to summarize", KindMarkdown, "The key sorts. To summarize, it hashes.", []string{"1:To summarize,"}},
		{"in summary", KindPullRequest, pr("- Sorts the key. In summary, replays hit."), []string{"4:In summary,"}},
		{"a reply", KindReply, "All in all, the key sorts.", []string{"1:All in all,"}},
	})
}

func TestIngTailReportsAParticipleClaimingSignificance(t *testing.T) {
	runTextCases(t, RuleIngTail, []textCase{
		{"a following action", KindMarkdown, "The key sorts its inputs, then hashes them.", nil},
		{"a progressive", KindMarkdown, "The sorter is reflecting the input order back to the caller in a test.", nil},
		// internal/render/target_graph.go: the participle states a postcondition.
		{"ensuring", KindMarkdown, "Hash the inputs, ensuring the lock is released.", nil},
		{"highlighting", KindMarkdown, "The cache stores blobs, highlighting the importance of the key.", []string{"1:, highlighting"}},
		{"underscoring", KindMarkdown, "The key sorts, underscoring the design.", []string{"1:, underscoring"}},
		{"showcasing", KindPullRequest, pr("- Sorts the inputs, showcasing the new hasher."), []string{"4:, showcasing"}},
	})
}

func TestStaccatoReportsARunOfShortSentences(t *testing.T) {
	runTextCases(t, RuleStaccato, []textCase{
		{"two short then long", KindMarkdown,
			"It reads the key. It sorts it. Then the hash runs over the sorted bytes in one pass.", nil},
		{"a bullet of fragments", KindMarkdown, "- Fast. Small. Stable.", nil},
		{"one short for emphasis", KindMarkdown,
			"Speed matters here, so the key sorts its inputs before hashing them, and the replay hits.", nil},
		{"a run of six, three and five", KindMarkdown,
			"It had no preference for symmetry. No aesthetic prior. No nostalgia for human taste.", []string{"1:"}},
		{"four short", KindMarkdown, "The cache is fast. The key is stable. Replays hit. Nothing else changed.", []string{"1:"}},
		{"docs/scope.md", KindMarkdown, "magus does not decide for you. It answers questions. You decide.", []string{"1:"}},
		{"reported once, at the run", KindMarkdown,
			"The sorter walks every input and keeps the order it found them in on disk.\nIt is fast. It is small. " +
				"It is stable. It is done.\n", []string{"2:"}},
		{"a code span is one word", KindMarkdown, "Run `magus run go-build .` now. Then `magus affected ci` too. It passes.",
			[]string{"1:"}},
		{"a heading", KindMarkdown, "## Fast. Small. Stable.", nil},
		// docs/guides/setup.md: a bold step number opens the item.
		{"a step number", KindMarkdown, "**3. List.** See what a repository holds.", nil},
		{"a guide", KindGuide, "Run it. Add a target. Check the key.", nil},
		{"a skill", KindSkill, "Run it. Add a target. Check the key.", nil},
	})
}

func TestDashReportsADashBetweenWords(t *testing.T) {
	runTextCases(t, RuleDash, []textCase{
		{"a semicolon", KindMarkdown, "Sort the inputs; then hash them.", nil},
		{"a flag in code", KindMarkdown, "Run `magus run -- -run TestX` to narrow the test.", nil},
		{"a plain range", KindMarkdown, "The range is 10 to 20.", nil},
		{"a hyphenated word", KindMarkdown, "A cache-key is stable - see below.", nil},
		{"an em dash", KindMarkdown, "The key sorts its inputs — then it hashes them.", []string{"1:—"}},
		{"a double hyphen", KindPullRequest, pr("- Sorts the inputs -- and hashes them."), []string{"4:inputs -- and"}},
		{"an en dash", KindMarkdown, "Pages 10–20 cover it.", []string{"1:–"}},
		{"a reply", KindReply, "Done — pushed.", []string{"1:—"}},
	})
}

func TestASCIIReportsACurlyMarkAnEllipsisOrAnEmoji(t *testing.T) {
	runTextCases(t, RuleASCII, []textCase{
		{"straight marks", KindMarkdown, "Quote the value with straight marks and write three dots as ...", nil},
		{"curly in code", KindMarkdown, "Use `“quoted”` in a code span.", nil},
		// docs/concepts/spells.md: an arrow has no plain spelling.
		{"an arrow", KindMarkdown, "The arrow → stays: it has no plain spelling.", nil},
		{"curly quotes", KindMarkdown, "He said “the key is stable”.", []string{"1:“", "1:”"}},
		{"an ellipsis", KindMarkdown, "Pins the key … and hashes it.", []string{"1:…"}},
		{"an emoji", KindMarkdown, "Launch phase \U0001F680: the key sorts.", []string{"1:\U0001F680"}},
		{"a curly apostrophe", KindPullRequest, pr("- Pins the key’s inputs."), []string{"4:’"}},
		{"a check mark", KindReply, "Done ✅", []string{"1:✅"}},
	})
}

func TestHeadingCaseReportsATitleCasedHeading(t *testing.T) {
	runTextCases(t, RuleHeadingCase, []textCase{
		{"sentence case", KindMarkdown, "## Strategic negotiations and global partnerships", nil},
		{"three words", KindMarkdown, "## Cache key derivation", nil},
		{"a proper noun among lower case", KindMarkdown, "## Run the Buzz formatter", nil},
		{"a code span", KindMarkdown, "## The `Magus Run` Command", nil},
		{"title case", KindMarkdown, "## Strategic Negotiations And Global Partnerships",
			[]string{"1:Strategic Negotiations And Global Partnerships"}},
		{"four words", KindMarkdown, "## Cache Key Derivation Rules", []string{"1:Cache Key Derivation Rules"}},
		{"in a guide", KindGuide, "## Handling Stale Cache Entries", []string{"1:Handling Stale Cache Entries"}},
		{"not a heading", KindMarkdown, "Handling Stale Cache Entries", nil},
	})
}

func TestFillerReportsTheSlopPhrases(t *testing.T) {
	runTextCases(t, RuleFiller, []textCase{
		{"a literal flag", KindMarkdown, "The `deeply` flag is a literal name.", nil},
		{"a size", KindMarkdown, "At this size the cache holds 4 entries.", nil},
		{"truncate", KindMarkdown, "Hash the inputs, truncate to 12 bytes.", nil},
		{"truly", KindMarkdown, "The key is truly stable.", []string{"1:truly"}},
		{"at its core", KindMarkdown, "At its core, the key sorts its inputs.", []string{"1:At its core"}},
		{"critical to remember", KindPullRequest, pr("- The cap is critical to remember."), []string{"4:critical to remember"}},
		{"selling words", KindMarkdown, "A powerful, elegant, cutting-edge and effortless cache.",
			[]string{"1:powerful", "1:elegant", "1:cutting-edge", "1:effortless"}},
		{"state of the art", KindMarkdown, "A state-of-the-art, world-class hasher.", []string{"1:state-of-the-art", "1:world-class"}},
		{"when it comes to", KindPullRequest, pr("- When it comes to hashing, the key sorts."), []string{"4:When it comes to"}},
		{"a doc keeps its list", KindDoc, "At its core, a powerful cache.", nil},
	})
}
