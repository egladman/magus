package proofread

import (
	"maps"
	"slices"
)

// Format names how a reader turns a text into the lines the rules judge.
type Format string

const (
	// FormatMarkdown reads Markdown. Front matter, code and tables are not
	// prose, nor is HTML or what a platform wrote.
	FormatMarkdown Format = "markdown"
	// FormatLines reads each non-blank line or list item as one unit, with no
	// title and no headings, as a list of standalone entries is written.
	FormatLines Format = "lines"
	// FormatPlain reads plain text: no Markdown is stripped, only backticks
	// mark a literal, and a URL is not prose.
	FormatPlain Format = "plain"
	// FormatDocComment reads a symbol's doc comment the way go/doc/comment
	// reads one.
	FormatDocComment Format = "doc-comment"
	// FormatStylesheet reads a stylesheet and judges its comments beside the
	// code each sits on.
	FormatStylesheet Format = "stylesheet"
	// FormatTemplate renders a skill's text/template body into its short and
	// full forms and judges both.
	FormatTemplate Format = "template"
	// FormatMessage reads one text a program prints, whole, held to a rune
	// cap.
	FormatMessage Format = "message"
)

// formats is every format, in the order the docs list them.
var formats = []Format{
	FormatMarkdown, FormatLines, FormatPlain, FormatDocComment, FormatStylesheet, FormatTemplate, FormatMessage,
}

// Reader is how a kind's text becomes the input its rules read. Every field
// past Format applies to [FormatMarkdown] alone.
type Reader struct {
	Format Format `json:"format"`
	// Titled reads the first line as a title, a paragraph of its own.
	Titled bool `json:"titled,omitempty"`
	// Headings reads an ATX heading as a heading rather than a line of prose.
	Headings bool `json:"headings,omitempty"`
	// Sections gives headed sections their own rules, as a description's are
	// held to name their subject rather than carry a stock label.
	Sections bool `json:"sections,omitempty"`
	// FrontMatter reads a YAML block opening the text as front matter, all
	// but its description dropped.
	FrontMatter bool `json:"front_matter,omitempty"`
	// Quotes reads a block quote as another author's words, never the
	// writer's own; elsewhere a block quote is the page's own callout.
	Quotes bool `json:"quotes,omitempty"`
	// Subject reads the title as a subject line, measured by its length in a
	// one-line log rather than by windows of prose.
	Subject bool `json:"subject,omitempty"`
}

// Groups says how a kind's headings name the groups of a change list.
type Groups int

const (
	// GroupsNone holds no heading to a group name.
	GroupsNone Groups = iota
	// GroupsKeepAChangelog holds every level-3 heading to a Keep a Changelog
	// group name.
	GroupsKeepAChangelog
	// GroupsSynonyms holds a level-2 or level-3 heading that spells a group
	// another way ("New features") to the group's own name.
	GroupsSynonyms
)

// KindSpec is a kind: how its text is read, the rules that judge it with each
// rule's default decision, and the facts about it those rules read. The
// built-in kinds are KindSpecs, and so is every kind a decisions table
// declares ([DeclareKinds]).
type KindSpec struct {
	Name Kind
	// Extends is the kind a declared kind starts from, empty on a built-in.
	Extends Kind
	Reader  Reader
	// Rules holds each rule this kind judges and its default decision. A rule
	// it lacks never runs on it. One at [DecisionOff] runs where a table names
	// it.
	Rules map[Rule]Decision
	// Weights holds a grade weight a team set in place of a rule's measured
	// one. A finding weighed by one carries [Finding.WeightSet].
	Weights map[Rule]float64
	// Minimum is the grade a text of this kind must reach, nil for none.
	Minimum *int
	// Terms holds spellings this kind replaces, each with its replacement, on
	// top of the built-in glossary. [RuleTerms] reports them.
	Terms map[string]string
	// Triads is how many series of exactly three [RuleTriad] waits for before
	// it reports them, 0 for its default of 2.
	Triads int

	// Voice is the kind whose measures in a [Voice] hold this one, empty for
	// none. Every review text shares the review-reply voice.
	Voice Kind
	// TenseByVoice lets a voice whose author writes in the first person and
	// the future tense hold tense to advice ([Voice.RelaxesTense]).
	TenseByVoice bool
	// Review marks a text a review posts: its body, an inline comment or a
	// reply, read in a thread the writer and the reader share.
	Review bool
	// ToReviewers marks text written to the people working on the change.
	// There a sentence about past work names someone the reader knows.
	ToReviewers bool
	// Anchored marks a text posted on a line of the diff, which is its
	// referent.
	Anchored bool
	// Prompted marks text a writer may have produced from a chat the reader
	// never saw. A salutation or "you asked" there answers that chat.
	Prompted bool
	// Procedure marks steps the reader follows with a terminal open, where
	// words that grade a step's difficulty are condescension.
	Procedure bool
	// Terse marks text an agent loads in every session, where every word
	// costs context, so the shorter word list for wordy applies.
	Terse bool
	// Compact marks text only an agent reads, written below grammar, whose
	// markup the compact rules judge.
	Compact bool
	// Groups is how the kind's headings name the groups of a change list.
	Groups Groups
	// ReviewWords is the word count review-length holds the text to, 0 for
	// none.
	ReviewWords int

	// declared marks a kind a decisions table declared, whose decisions are
	// the team's own: [unmeasured] turns none of them off.
	declared bool
}

// proseFormats are the formats that read prose by Markdown's rules.
var proseFormats = []Format{FormatMarkdown, FormatLines}

// decided is a list of rules that share one default decision.
type decided struct {
	decision Decision
	rules    []Rule
}

func deny(rules ...Rule) decided   { return decided{DecisionDeny, rules} }
func advise(rules ...Rule) decided { return decided{DecisionAdvise, rules} }

// house is rules that encode one repository's conventions rather than plain
// writing, off until a decisions table names them.
func house(rules ...Rule) decided { return decided{DecisionOff, rules} }

func rules(sets ...decided) map[Rule]Decision {
	out := map[Rule]Decision{}

	for _, s := range sets {
		for _, r := range s.rules {
			out[r] = s.decision
		}
	}

	return out
}

// markdownPage is how a hand-written Markdown page is read.
var markdownPage = Reader{Format: FormatMarkdown, Headings: true, FrontMatter: true}

// reviewReader is how every text a review posts is read.
var reviewReader = Reader{Format: FormatMarkdown, Headings: true, Sections: true, Quotes: true}

// builtinKinds are the kinds proofread ships, in the order [ParseKind] and the
// docs list them.
var builtinKinds = []KindSpec{
	{
		Name:   KindDocComment,
		Reader: Reader{Format: FormatDocComment},
		Rules: rules(
			deny(RuleSuppressionUnused, RuleGrade),
			advise(RuleFiller, RuleLeak),
			house(RuleCommentBlock, RuleCommentSentence, RuleTerms, RuleAside, RuleHistory, RuleDocStub),
		),
	},
	{
		Name:   KindReference,
		Reader: markdownPage,
		Rules: rules(
			deny(RuleCondescension, RuleChatbot, RuleBuzzword, RuleVague, RuleCloser, RuleSuppressionUnused, RuleGrade),
			advise(RuleFiller, RuleReplyVoice, RuleHedge, RuleWordy, RuleSignpost, RuleLeak, RuleBuzzwordWeak,
				RuleContrast, RuleIngTail, RuleStaccato, RuleHeadingCase, RuleParticiples, RuleNominalizations,
				RuleListMarks, RuleListParallel, RuleDateFormat),
			house(RuleTerms, RuleTense, RuleAttribution, RuleFirstPerson, RuleDash, RuleASCII),
		),
	},
	{
		// A principle is argued in its author's voice, so tense leaves it.
		Name:   KindPrinciple,
		Reader: markdownPage,
		Rules: rules(
			deny(RuleCondescension, RuleChatbot, RuleBuzzword, RuleVague, RuleCloser, RuleSuppressionUnused, RuleGrade),
			advise(RuleFiller, RuleReplyVoice, RuleHedge, RuleWordy, RuleSignpost, RuleLeak, RuleBuzzwordWeak,
				RuleContrast, RuleIngTail, RuleStaccato, RuleHeadingCase, RuleParticiples, RuleNominalizations,
				RuleListMarks, RuleListParallel, RuleDateFormat),
			house(RuleTerms, RuleAttribution, RuleDash, RuleASCII),
		),
	},
	{
		Name: KindChangeDescription,
		Reader: Reader{
			Format: FormatMarkdown, Titled: true, Headings: true, Sections: true, Quotes: true,
		},
		Rules: rules(
			deny(RuleLeadContext, RuleCondescension, RuleChatbot, RuleBuzzword, RuleVague, RuleCloser,
				RuleSuppressionUnused, RuleGrade),
			advise(RuleFiller, RuleReplyVoice, RuleHedge, RuleWordy, RuleBlame, RuleVerdict, RuleAbsolute, RuleIntent,
				RuleCredit, RuleClaim, RuleSignpost, RuleLeak, RuleBuzzwordWeak, RuleContrast, RuleIngTail, RuleStaccato,
				RuleParticiples, RuleNominalizations, RuleUnbackedDone, RuleListMarks, RuleListParallel, RuleDateFormat,
				RuleVoiceDrift, RuleTriad, RuleStackedColon, RuleRestatesDiff),
			house(RuleTerms, RuleTense, RuleAttribution, RuleDash, RuleASCII, RuleLocalPath, RuleLocalBranch,
				RuleEmphasis, RuleBareSymbol),
		),
		Voice: KindChangeDescription, TenseByVoice: true, ToReviewers: true, Prompted: true,
	},
	{
		Name:   KindAgentBrief,
		Reader: markdownPage,
		Rules: rules(
			deny(RuleCondescension, RuleChatbot, RuleBuzzword, RuleVague, RuleCloser, RuleSuppressionUnused, RuleGrade),
			advise(RuleFiller, RuleReplyVoice, RuleHedge, RuleWordy, RuleSignpost, RuleLeak, RuleBuzzwordWeak,
				RuleContrast, RuleIngTail, RuleHeadingCase, RuleListMarks, RuleListParallel, RuleDateFormat),
			house(RuleTerms, RuleTense, RuleAttribution, RuleTerseSentence, RuleTerseParagraph, RuleBareRule, RuleDash,
				RuleASCII),
		),
		Terse: true,
	},
	{
		Name:   KindAgentCompact,
		Reader: markdownPage,
		Rules: rules(
			deny(RuleCondescension, RuleChatbot, RuleBuzzword, RuleVague, RuleCloser, RuleSuppressionUnused, RuleGrade),
			advise(RuleFiller, RuleReplyVoice, RuleHedge, RuleSignpost, RuleLeak, RuleBuzzwordWeak, RuleContrast,
				RuleCompactFiller, RuleCompactMarkup, RuleCompactCostlyAbbrev, RuleCompactSymbolNegation,
				RuleCompactLine, RuleCompactDuplicate),
			house(RuleTerms, RuleTense, RuleAttribution, RuleBareRule, RuleDash, RuleASCII),
		),
		Compact: true,
	},
	{
		Name:   KindSkillTemplate,
		Reader: Reader{Format: FormatTemplate},
		Rules: rules(
			deny(RuleSuppressionUnused, RuleGrade),
			advise(RuleSkillDescriptionBudget, RuleSkillSetDescriptionBudget, RuleInstructionPosition),
			house(RuleTemplate),
		),
	},
	{
		Name:   KindGuide,
		Reader: markdownPage,
		Rules: rules(
			deny(RuleSecondPerson, RuleStepVerb, RuleCondescension, RuleChatbot, RuleBuzzword, RuleVague, RuleCloser,
				RuleSuppressionUnused, RuleGrade),
			advise(RuleFiller, RuleReplyVoice, RuleHedge, RuleWordy, RuleSignpost, RuleLeak, RuleBuzzwordWeak,
				RuleContrast, RuleIngTail, RuleHeadingCase, RuleParticiples, RuleNominalizations, RuleListMarks,
				RuleListParallel, RuleDateFormat),
			house(RuleTerms, RuleTense, RuleAttribution, RuleFirstPerson, RuleDash, RuleASCII),
		),
		Procedure: true,
	},
	{
		Name:   KindReviewSummary,
		Reader: reviewReader,
		Rules: rules(
			deny(RuleCondescension, RuleChatbot, RuleBuzzword, RuleVague, RuleCloser, RuleSuppressionUnused, RuleGrade),
			advise(RuleFiller, RuleReplyVoice, RuleBlame, RuleVerdict, RuleAbsolute, RuleIntent, RuleClaim,
				RuleReplyOpener, RuleJudgmentAsFact, RuleStackedHedge, RuleLongThread, RuleSignpost, RuleLeak,
				RuleBuzzwordWeak, RuleContrast, RuleNonspecific, RuleWhyOpener, RuleBareImperative, RuleAllCaps,
				RuleRepeatedMarks, RuleEmptyRequestChanges, RuleApproveBlocker, RuleBareLGTM, RuleReviewLength,
				RuleListMarks, RuleListParallel, RuleDateFormat, RuleVoiceDrift, RuleTriad, RuleStackedColon,
				RuleRestatesDiff),
			house(RuleTerms, RuleAttribution, RuleDash, RuleASCII, RuleLocalPath, RuleLocalBranch, RuleEmphasis,
				RuleBareSymbol),
		),
		Voice: KindReviewReply, Review: true, ToReviewers: true, ReviewWords: 100,
	},
	{
		Name:   KindReviewInline,
		Reader: reviewReader,
		Rules: rules(
			deny(RuleCondescension, RuleChatbot, RuleBuzzword, RuleVague, RuleCloser, RuleSuppressionUnused, RuleGrade),
			advise(RuleFiller, RuleReplyVoice, RuleBlame, RuleVerdict, RuleAbsolute, RuleIntent, RuleClaim,
				RuleReplyOpener, RuleJudgmentAsFact, RuleStackedHedge, RuleLongThread, RuleSignpost, RuleLeak,
				RuleBuzzwordWeak, RuleContrast, RuleNonspecific, RuleWhyOpener, RuleBareImperative, RuleAllCaps,
				RuleRepeatedMarks, RuleNoReason, RuleNitLabel, RuleReviewLength, RuleListMarks, RuleListParallel,
				RuleDateFormat, RuleVoiceDrift, RuleTriad, RuleStackedColon, RuleRestatesDiff),
			house(RuleTerms, RuleAttribution, RuleDash, RuleASCII, RuleLocalPath, RuleLocalBranch, RuleEmphasis,
				RuleBareSymbol),
		),
		Voice: KindReviewReply, Review: true, ToReviewers: true, Anchored: true, ReviewWords: 60,
	},
	{
		Name:   KindReviewReply,
		Reader: reviewReader,
		Rules: rules(
			deny(RuleCondescension, RuleChatbot, RuleBuzzword, RuleVague, RuleCloser, RuleSuppressionUnused, RuleGrade),
			advise(RuleFiller, RuleReplyVoice, RuleBlame, RuleVerdict, RuleAbsolute, RuleIntent, RuleClaim,
				RuleReplyOpener, RuleJudgmentAsFact, RuleStackedHedge, RuleLongThread, RuleSignpost, RuleLeak,
				RuleBuzzwordWeak, RuleContrast, RuleNonspecific, RuleWhyOpener, RuleBareImperative, RuleAllCaps,
				RuleRepeatedMarks, RuleDeferralNoLink, RuleDeclineNoReason, RuleImpatience, RuleReviewLength,
				RuleListMarks, RuleListParallel, RuleDateFormat, RuleVoiceDrift, RuleTriad, RuleStackedColon,
				RuleRestatesDiff),
			house(RuleTerms, RuleAttribution, RuleDash, RuleASCII, RuleLocalPath, RuleLocalBranch, RuleEmphasis,
				RuleBareSymbol),
		),
		Voice: KindReviewReply, Review: true, ToReviewers: true, ReviewWords: 80,
	},
	{
		// Every message rule denies, so a diagnostic has no grade.
		Name:   KindDiagnostic,
		Reader: Reader{Format: FormatMessage},
		Rules: rules(
			deny(RuleMessageLength, RuleMessageRationale, RuleMessageCommands, RuleMessageTag, RuleSuppressionUnused),
		),
	},
	{
		Name:   KindCommitMessage,
		Reader: Reader{Format: FormatMarkdown, Titled: true, Quotes: true, Subject: true},
		Rules: rules(
			deny(RuleCondescension, RuleChatbot, RuleBuzzword, RuleVague, RuleCloser, RuleSubjectLength,
				RuleSubjectPeriod, RuleBodySeparator, RuleSuppressionUnused, RuleGrade),
			advise(RuleFiller, RuleReplyVoice, RuleHedge, RuleWordy, RuleBlame, RuleVerdict, RuleAbsolute, RuleIntent,
				RuleClaim, RuleSignpost, RuleLeak, RuleBuzzwordWeak, RuleContrast, RuleIngTail, RuleSubjectMood,
				RuleListMarks, RuleListParallel, RuleDateFormat, RuleVoiceDrift, RuleTriad, RuleStackedColon,
				RuleRestatesDiff),
			house(RuleTerms, RuleTense, RuleAttribution, RuleDash, RuleASCII, RuleLocalPath, RuleLocalBranch,
				RuleSubjectCase, RuleSubjectPunct, RuleNoBody),
		),
		Voice: KindCommitMessage,
	},
	{
		Name:   KindCLIHelp,
		Reader: Reader{Format: FormatPlain},
		Rules: rules(
			deny(RuleCondescension, RuleHelpSentence, RuleHelpLength, RuleSuppressionUnused, RuleGrade),
			advise(RuleFiller, RuleLeak),
			house(RuleTerms, RuleAside),
		),
	},
	{
		Name:   KindIssue,
		Reader: Reader{Format: FormatMarkdown, Titled: true, Headings: true, Quotes: true},
		Rules: rules(
			deny(RuleCondescension, RuleChatbot, RuleBuzzword, RuleVague, RuleCloser, RuleSuppressionUnused, RuleGrade),
			advise(RuleFiller, RuleReplyVoice, RuleHedge, RuleWordy, RuleBlame, RuleVerdict, RuleAbsolute, RuleIntent,
				RuleClaim, RuleSignpost, RuleLeak, RuleBuzzwordWeak, RuleContrast, RuleIngTail, RuleStaccato,
				RuleHeadingCase, RuleIssueRepro, RuleParticiples, RuleNominalizations, RuleListMarks, RuleListParallel,
				RuleDateFormat, RuleVoiceDrift),
			house(RuleTerms, RuleTense, RuleAttribution, RuleDash, RuleASCII, RuleLocalPath, RuleLocalBranch),
		),
		Voice: KindIssue,
	},
	{
		Name:   KindReleaseNotes,
		Reader: markdownPage,
		Rules: rules(
			deny(RuleCondescension, RuleChatbot, RuleBuzzword, RuleVague, RuleCloser, RuleSuppressionUnused, RuleGrade),
			advise(RuleFiller, RuleReplyVoice, RuleHedge, RuleWordy, RuleSignpost, RuleLeak, RuleBuzzwordWeak,
				RuleContrast, RuleIngTail, RuleStaccato, RuleHeadingCase, RuleChangelogGroup, RuleChangelogEntry,
				RuleListMarks, RuleListParallel, RuleDateFormat),
			house(RuleTerms, RuleTense, RuleAttribution, RuleDash, RuleASCII, RuleLocalPath, RuleLocalBranch),
		),
		Groups: GroupsSynonyms,
	},
	{
		Name:   KindChangelog,
		Reader: markdownPage,
		Rules: rules(
			deny(RuleCondescension, RuleChatbot, RuleBuzzword, RuleVague, RuleCloser, RuleChangelogHeading,
				RuleSuppressionUnused, RuleGrade),
			advise(RuleFiller, RuleReplyVoice, RuleHedge, RuleWordy, RuleSignpost, RuleLeak, RuleBuzzwordWeak,
				RuleContrast, RuleIngTail, RuleChangelogGroup, RuleChangelogEntry, RuleListMarks, RuleListParallel,
				RuleDateFormat),
			house(RuleTerms, RuleTense, RuleAttribution, RuleDash, RuleASCII, RuleLocalPath, RuleLocalBranch),
		),
		Groups: GroupsKeepAChangelog,
	},
	{
		Name:   KindAgentReply,
		Reader: Reader{Format: FormatMarkdown, Headings: true, FrontMatter: true, Quotes: true},
		Rules: rules(
			deny(RuleCondescension, RuleBuzzword, RuleVague, RuleCloser, RuleSuppressionUnused, RuleGrade),
			advise(RuleFiller, RuleHedge, RuleWordy, RuleBlame, RuleIntent, RuleClaim, RuleSignpost, RuleLeak,
				RuleBuzzwordWeak, RuleContrast, RuleIngTail, RuleBoldLabel, RuleClosingOffer, RuleRequestRecap,
				RuleOptionList, RuleUnbackedDone, RuleShortReplyHeading, RuleAgreementOpener, RuleListMarks,
				RuleListParallel, RuleDateFormat),
			house(RuleTerms, RuleDash, RuleASCII),
		),
	},
	{
		Name:   KindToolDescription,
		Reader: Reader{Format: FormatPlain},
		Rules: rules(
			deny(RuleSuppressionUnused, RuleToolSelling, RuleGrade),
			advise(RuleToolLead, RuleToolBoundary, RuleToolLength),
		),
	},
	{
		Name:   KindStylesheet,
		Reader: Reader{Format: FormatStylesheet},
		Rules: rules(
			deny(RuleSuppressionUnused, RuleGrade),
			advise(RuleFiller, RuleReplyVoice, RuleHedge),
			house(RuleCommentBlock, RuleCommentSentence, RuleTerms, RuleAside, RuleHistory, RuleTense, RuleAttribution,
				RuleRestates, RuleCommentBudget, RuleBlockBudget, RuleBannedWord),
		),
	},
}

// kinds is every built-in kind's name, in [builtinKinds] order.
var kinds = func() []Kind {
	out := make([]Kind, len(builtinKinds))
	for i, s := range builtinKinds {
		out[i] = s.Name
	}

	return out
}()

// builtins indexes [builtinKinds] by name.
var builtins = func() map[Kind]*KindSpec {
	out := make(map[Kind]*KindSpec, len(builtinKinds))
	for i := range builtinKinds {
		out[builtinKinds[i].Name] = &builtinKinds[i]
	}

	return out
}()

// BuiltinKind returns the spec of the built-in kind named kind, a former name
// included, and false for any other name. The spec is a copy: changing it
// changes no built-in.
func BuiltinKind(kind Kind) (KindSpec, bool) {
	k, ok := ParseKind(string(kind))
	if !ok {
		return KindSpec{}, false
	}

	return builtins[k].clone(), true
}

func (s KindSpec) clone() KindSpec {
	s.Rules, s.Weights, s.Terms = maps.Clone(s.Rules), maps.Clone(s.Weights), maps.Clone(s.Terms)
	if s.Minimum != nil {
		m := *s.Minimum
		s.Minimum = &m
	}

	return s
}

// judges reports whether r judges s.
func (s *KindSpec) judges(r Rule) bool {
	_, ok := s.Rules[r]

	return ok
}

// spec returns the spec of the built-in kind, which a caller in this package
// names by its constant.
func spec(kind Kind) *KindSpec {
	if s, ok := builtins[kind]; ok {
		return s
	}

	panic("proofread: no built-in kind " + string(kind))
}

// canRead reports whether c's judge can read text of format f.
func (c check) canRead(f Format) bool { return slices.Contains(c.reads, f) }
