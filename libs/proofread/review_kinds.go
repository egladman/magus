package proofread

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// RuleNoReason reports an inline review comment that asks for a change, as
	// a command or with should, must or needs to, and gives no reason.
	RuleNoReason Rule = "no-reason"
	// RuleNitLabel reports an inline review comment whose label contradicts
	// its text: "nit:" on a blocking demand, "blocking:" on an optional one.
	RuleNitLabel Rule = "nit-label"
	// RuleEmptyRequestChanges reports a review summary posted with
	// [EventRequestChanges] that names nothing blocking and points at no
	// inline comment.
	RuleEmptyRequestChanges Rule = "empty-request-changes"
	// RuleApproveBlocker reports a review summary posted with [EventApprove]
	// whose body names a blocker.
	RuleApproveBlocker Rule = "approve-blocker"
	// RuleBareLGTM reports a review summary that only approves: "LGTM",
	// "ship it", "looks good".
	RuleBareLGTM Rule = "bare-lgtm"
	// RuleDeferralNoLink reports a review reply that defers the change to
	// later, a follow-up or a TODO and links no issue.
	RuleDeferralNoLink Rule = "deferral-no-link"
	// RuleDeclineNoReason reports a review reply that declines the change
	// ("won't fix", "No, ...") and gives no reason.
	RuleDeclineNoReason Rule = "decline-no-reason"
	// RuleImpatience reports a review reply that reads as impatient: "as I
	// said", "for the last time", "obviously".
	RuleImpatience Rule = "impatience"
)

var (
	inlineOnly  = []Kind{KindReviewInline}
	summaryOnly = []Kind{KindReviewSummary}
	replyOnly   = []Kind{KindReviewReply}
)

// reviewKindChecks are the rules for one review text in particular: an inline
// comment, a summary or a reply. Each advises until a labeling round measures
// it.
var reviewKindChecks = []check{
	{rule: RuleNoReason, on: inlineOnly, advise: inlineOnly, judge: ownWords(unexplainedRequest)},
	{rule: RuleNitLabel, on: inlineOnly, advise: inlineOnly, judge: nitLabel},
	{rule: RuleEmptyRequestChanges, on: summaryOnly, advise: summaryOnly, judge: emptyRequestChanges},
	{rule: RuleApproveBlocker, on: summaryOnly, advise: summaryOnly, judge: ownWords(approveBlocker)},
	{rule: RuleBareLGTM, on: summaryOnly, advise: summaryOnly, judge: bareLGTM},
	{rule: RuleDeferralNoLink, on: replyOnly, advise: replyOnly, judge: ownWords(deferralNoLink)},
	{rule: RuleDeclineNoReason, on: replyOnly, advise: replyOnly, judge: ownWords(declineNoReason)},
	{rule: RuleImpatience, on: replyOnly, advise: replyOnly, judge: ownWords(impatience)},
}

var reviewKindTexts = map[Rule]ruleText{
	RuleNoReason: {
		code:      "PRF8030",
		dimension: DimensionEvidence,
		catches:   "an inline review comment that asks for a change and gives no reason",
		why: "Widyasari et al. (arXiv 2311.09020) labeled 793 Gerrit review comments and found 42% gave a " +
			"suggestion with no explanation; Ebert et al. (SANER 2019) found missing rationale the most " +
			"frequent reason for confusion in code review, which delays the merge. Only a comment of one " +
			"sentence is judged, one opening with a command or holding should, must or needs to: a second " +
			"sentence is read as the reason, and so is a reason marker (because, since, so, otherwise, " +
			"which, a purpose such as \"to avoid\"). A question, and a comment labeled nit, typo or " +
			"optional, is left alone. No firing has been labeled, so it advises.",
	},
	RuleNitLabel: {
		code:      "PRF8031",
		dimension: DimensionStance,
		catches:   "an inline review comment labeled nit on a blocking demand, or blocking on an optional one",
		why: "Conventional Comments (conventionalcomments.org) holds a nitpick non-blocking by definition, " +
			"and Google's review guide (eng-practices, \"How to write code review comments\") labels " +
			"optional points \"Nit:\" so the author knows what must change. A label the text contradicts " +
			"leaves the author to guess which one the reviewer meant. It is a convention, not a study, so " +
			"it advises.",
	},
	RuleEmptyRequestChanges: {
		code:      "PRF8032",
		dimension: DimensionEvidence,
		catches:   "a review requesting changes whose body names nothing blocking and points at no inline comment",
		why: "A review that requests changes holds the merge; GitHub's review API requires its body. " +
			"Google's review guide (eng-practices, \"The Standard of Code Review\") asks the reviewer to " +
			"say what blocks, and Ebert et al. (SANER 2019) found missing rationale the most frequent cause " +
			"of confusion. A body naming code, a path, a line, a failure, a list of items or the inline " +
			"comments passes. It needs the review's event (-event), and judges nothing without it. It is " +
			"inferred from those sources rather than measured, so it advises.",
	},
	RuleApproveBlocker: {
		code:      "PRF8033",
		dimension: DimensionStance,
		catches:   "an approving review whose body names a blocker (\"must fix\", \"blocking\", \"before merge\")",
		why: "Google's review guide (eng-practices, \"The Standard of Code Review\") approves a change once " +
			"it improves code health and labels what is left as optional. An approval that names a blocker " +
			"lets the change merge with the blocker in it, or leaves the author unsure the approval holds. " +
			"\"non-blocking\" and \"no blockers\" are left alone. It needs the review's event (-event), and " +
			"judges nothing without it. It is a convention, not a study, so it advises.",
	},
	RuleBareLGTM: {
		code:      "PRF8034",
		dimension: DimensionEvidence,
		catches:   "a review summary that only approves: \"LGTM\", \"ship it\", \"looks good\"",
		why: "Ebert et al. (SANER 2019) found confusion can end in an approval given without checking, and " +
			"Bosu et al. (MSR 2015) found the share of useful comments falls as a change grows. A bare " +
			"approval says nothing of what the reviewer checked. A small change legitimately takes a bare " +
			"approval, and the rule cannot see the diff's size, so it advises on any size; a caller that " +
			"knows the size gates it.",
	},
	RuleDeferralNoLink: {
		code:      "PRF8035",
		dimension: DimensionEvidence,
		catches:   "a review reply deferring to later, a follow-up or a TODO, with no issue link or number",
		why: "Google's review guide (eng-practices, \"Handling pushback\") does not accept \"I'll clean it " +
			"up later\": follow-ups rarely happen, so the author files a bug and may add a TODO naming it. " +
			"A reply holding a URL, an issue number (#123) or a tracker key (ABC-123) passes; a standard's " +
			"name written the same way (UTF-8, SHA-256, ISO-8601) is no tracker key. It is a " +
			"convention, not a study, so it advises.",
	},
	RuleDeclineNoReason: {
		code:      "PRF8036",
		dimension: DimensionStance,
		catches:   "a short review reply declining the change (\"won't fix\", \"No, ...\") with no reason",
		why: "Google's review guide (eng-practices, \"Handling pushback\") asks whoever disagrees to explain, " +
			"and acknowledge the other side's point. Only a short decline is judged: a reply of one paragraph " +
			"whose words after the decline number five or fewer and hold no reason marker (because, since, " +
			"so, a colon). A longer \"No, the caller already sorts\" carries its reason without a marker. " +
			"It is a convention, not a study, so it advises.",
	},
	RuleImpatience: {
		code:      "PRF8037",
		dimension: DimensionStance,
		catches:   "a review reply that reads as impatient: \"as I said\", \"for the last time\", \"obviously\"",
		why: "Ferreira et al. (CSCW 2021) found uncivil features in 66.66% of the non-technical review " +
			"emails tied to rejected Linux kernel changes, impatience among the most frequent. A phrase that " +
			"points back at what the writer already said tells the reader they failed to listen. Legitimate " +
			"urgency reads alike, so it advises.",
	},
}

var (
	// changeAsked is a request for a change: a sentence opening with a
	// command, or one that holds should, must or needs to.
	changeAsked = regexp.MustCompile(`^(?:[Pp]lease,? )?(?:[Ff]ix|[Rr]emove|[Cc]hange|[Dd]elete|[Rr]ename|` +
		`[Mm]ove|[Uu]se|[Aa]dd|[Rr]evert|[Dd]rop|[Uu]pdate|[Mm]ake|[Dd]on't|[Dd]o not|[Ss]top|[Rr]ewrite|` +
		`[Rr]eplace|[Aa]void|[Ee]xtract|[Ii]nline|[Ss]plit|[Ww]rap|[Rr]eturn|[Hh]andle|[Kk]eep|[Pp]ut|` +
		`[Ss]witch|[Cc]onvert|[Gg]uard)\b|\b(?:should|must|needs? to|has to|have to)\b`)
	// optionalLabel opens a comment its writer marks as minor or optional.
	optionalLabel = regexp.MustCompile(`(?i)^\W*(?:nit(?:pick)?|typo|optional|minor|question|praise|thought|` +
		`note)\b`)
)

// unexplainedRequest reports an inline comment of one sentence that asks for a
// change and gives no reason. A second sentence, before or after the request,
// is read as its reason.
func unexplainedRequest(in input) []Finding {
	paras := paragraphs(in.prose, mentionsMasked)
	if len(paras) != 1 || optionalLabel.MatchString(paras[0].text) {
		return nil
	}

	para := paras[0]
	if reasonGiven.MatchString(para.text) || purpose.MatchString(para.text) || groundGiven.MatchString(para.text) {
		return nil
	}

	var sentences []string

	for _, span := range sentenceBounds(para.text) {
		if s := strings.TrimSpace(para.text[span[0]:span[1]]); s != "" {
			sentences = append(sentences, s)
		}
	}

	if len(sentences) != 1 || strings.HasSuffix(sentences[0], "?") {
		return nil
	}

	at := changeAsked.FindStringIndex(sentences[0])
	if at == nil {
		return nil
	}

	m := sentences[0][at[0]:at[1]]

	return []Finding{{
		Message: fmt.Sprintf("Give the reason for '%s': what goes wrong without the change, as in "+
			"'Use a sync.Map here: two workers write this map at once.'", m),
		Match: m, Line: para.lineAt(strings.Index(para.text, m)),
	}}
}

var (
	// nitMarked is a label or decoration that marks a comment non-blocking.
	nitMarked = regexp.MustCompile(`(?i)^\W*(?:\[(?:nit(?:pick)?|minor|optional)\]|(?:nit(?:pick)?|minor|optional)\b` +
		`(?:\s*\([^)]*\))?\s*:|\w+\s*\((?:non-blocking|if-minor)\)\s*:)`)
	// blockingMarked is a label or decoration that marks a comment blocking.
	blockingMarked = regexp.MustCompile(`(?i)^\W*(?:\[(?:blocking|blocker)\]|(?:blocking|blocker)\s*:|` +
		`\w+\s*\(blocking\)\s*:)`)
	// mustWords demand the change before the merge.
	mustWords = regexp.MustCompile(`(?i)\b(?:must|blocker|blocking|will break|breaks the build|` +
		`before (?:merg|land)\w*|do not merge|don't merge|required)\b`)
	// optionalWords leave the change to the author.
	optionalWords = regexp.MustCompile(`(?i)\b(?:optional(?:ly)?|if you (?:like|want|prefer)|feel free|` +
		`up to you|your call|non-blocking|not blocking|no strong (?:opinion|feelings)|take it or leave it|` +
		`nit(?:pick)?)\b`)
)

// nitLabel reports a non-blocking label over blocking words, or a blocking
// label over optional ones.
func nitLabel(in input) []Finding {
	paras := paragraphs(in.prose, mentionsMasked)
	if len(paras) == 0 {
		return nil
	}

	head := paras[0].text
	contradicted := mustWords

	labelEnd := nitMarked.FindStringIndex(head)
	if labelEnd == nil {
		contradicted = optionalWords

		if labelEnd = blockingMarked.FindStringIndex(head); labelEnd == nil {
			return nil
		}
	}

	label := strings.TrimSpace(head[:labelEnd[1]])

	for i, para := range paras {
		offset := 0
		if i == 0 {
			offset = labelEnd[1]
		}

		if at := contradicted.FindStringIndex(para.text[offset:]); at != nil {
			m := para.text[offset+at[0] : offset+at[1]]

			return []Finding{{
				Message: fmt.Sprintf("Make the label '%s' match '%s': mark it blocking if it must change "+
					"before the merge, or drop the demand if it is optional.", label, m),
				Match: m, Line: para.lineAt(offset + at[0]),
			}}
		}
	}

	return nil
}

var (
	// pointsInline sends the reader to the review's inline comments.
	pointsInline = regexp.MustCompile(`(?i)\binline\b|\bcomments? (?:below|above)\b|\bsee (?:my |the )?comments?\b|` +
		`\b(?:left|added) (?:a |some |a few )?comments?\b|\bin the comments?\b|\bthreads?\b`)
	// blockReason names why the change cannot merge as it is.
	blockReason = regexp.MustCompile(`(?i)\b(?:because|since|otherwise|breaks?|broken|fails?|failing|races?|leaks?|` +
		`deadlocks?|panics?|crash(?:es)?|regress(?:es|ion)?)\b`)
)

// emptyRequestChanges reports a request for changes whose body names nothing
// that blocks it. Without the review's event it judges nothing.
func emptyRequestChanges(in input) []Finding {
	if in.opts.event != EventRequestChanges {
		return nil
	}

	whole := strings.Join(in.source, "\n")
	if specific.MatchString(whole) || symptom.MatchString(whole) || pointsInline.MatchString(whole) ||
		blockReason.MatchString(whole) || holdsListItem(in.prose) {
		return nil
	}

	return []Finding{{
		Message: "Name what blocks the merge, or point at the inline comments that do, as in 'Requesting " +
			"changes: `Cache.put` races with `Cache.get` (see the comment on cache.go).'",
	}}
}

// holdsListItem reports whether prose holds a list item, which a summary uses
// to list what blocks.
func holdsListItem(prose []proseLine) bool {
	for _, ln := range prose {
		if ln.item {
			return true
		}
	}

	return false
}

var (
	// blockerWords name a blocker in an approval.
	blockerWords = regexp.MustCompile(`(?i)\b(?:must (?:be )?fix(?:ed)?|must change|blocking|blockers?|` +
		`needs? to (?:change|be fixed|be changed) before|before (?:merg|land)\w*|do not merge|don't merge)\b`)
	// unblocked negates a blocker word before it: "non-blocking", "no
	// blockers", "nothing blocking", "not blocking".
	unblocked = regexp.MustCompile(`(?i)(?:\bnon-|\bno |\bnothing |\bnot |\bnone )$`)
)

// approveBlocker reports the first blocker an approval names. Without the
// review's event it judges nothing.
func approveBlocker(in input) []Finding {
	if in.opts.event != EventApprove {
		return nil
	}

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		for _, at := range blockerWords.FindAllStringIndex(para.text, -1) {
			if unblocked.MatchString(para.text[:at[0]]) {
				continue
			}

			m := para.text[at[0]:at[1]]

			return []Finding{{
				Message: fmt.Sprintf("Request changes instead of approving, or say why '%s' need not block: "+
					"an approval lets the change merge as it is.", m),
				Match: m, Line: para.lineAt(at[0]),
			}}
		}
	}

	return nil
}

var bareApproval = regexp.MustCompile(`(?i)^\W*(?:lgtm|looks good(?: to me)?|ship ?it|approved?|\+1|:shipit:|` +
	`:\+1:)\W*(?:(?:thanks|thank you|thx|ty)\W*)?$`)

// bareLGTM reports a summary that is only an approval. A request for changes
// is not one.
func bareLGTM(in input) []Finding {
	if in.opts.event == EventRequestChanges {
		return nil
	}

	text := strings.TrimSpace(strings.Join(in.source, " "))
	if !bareApproval.MatchString(text) {
		return nil
	}

	line := 1

	for i, ln := range in.source {
		if strings.TrimSpace(ln) != "" {
			line = i + 1

			break
		}
	}

	return []Finding{{
		Message: "Say what you checked, as in 'LGTM: ran the migration against a copy of the data; " +
			"`TestRetry` covers the new path.'",
		Match: text, Line: line,
	}}
}

var (
	// putOff puts the change off: to later, a follow-up or a TODO.
	putOff = regexp.MustCompile(`(?i)\b(?:(?:will|'ll|can|could|should|going to|plan to|let's)\b[^.!?]*\blater\b|` +
		`(?:in )?a (?:follow[- ]?up|separate|future|another|next|later) (?:pr|pull request|mr|change|commit|patch)\b|` +
		`(?:as )?a follow[- ]?up\b|\bfollow[- ]?up (?:pr|pull request|mr|change|issue|work)\b|` +
		`\b(?:separate|next|future|another) (?:pr|pull request|mr)\b|\bTODO\b)`)
	// issueLink is a URL, an issue number or a GitHub issue path.
	issueLink = regexp.MustCompile(`https?://|(?:^|[\s(])#\d+\b|\bissues?/\d+|\bgh-\d+\b`)
	// trackerKey is a tracker's project key and number, ABC-123, or a numbered
	// standard written the same way.
	trackerKey = regexp.MustCompile(`\b([A-Z][A-Z0-9]+)-\d+\b`)
)

// standardFamilies name encodings, hashes and standards whose numbered names
// read as a tracker key: UTF-8, SHA-256, ISO-8601.
var standardFamilies = map[string]bool{
	"UTF": true, "UCS": true, "SHA": true, "SHA2": true, "SHA3": true, "MD": true, "CRC": true, "AES": true,
	"RSA": true, "ISO": true, "IEC": true, "IEEE": true, "RFC": true, "ECMA": true, "ANSI": true,
}

// linksIssue reports whether text names where deferred work is tracked: a
// URL, an issue number or a tracker key that is not a standard's name.
func linksIssue(text string) bool {
	if issueLink.MatchString(text) {
		return true
	}

	for _, m := range trackerKey.FindAllStringSubmatch(text, -1) {
		if !standardFamilies[m[1]] {
			return true
		}
	}

	return false
}

// deferralNoLink reports the first deferral in a reply that links nothing.
func deferralNoLink(in input) []Finding {
	if linksIssue(strings.Join(in.source, "\n")) {
		return nil
	}

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		if at := putOff.FindStringIndex(para.text); at != nil {
			m := para.text[at[0]:at[1]]

			return []Finding{{
				Message: fmt.Sprintf("Link the issue that tracks '%s', as in 'Filed #123 for the retry; this "+
					"pull request keeps the current behavior.'", m),
				Match: m, Line: para.lineAt(at[0]),
			}}
		}
	}

	return nil
}

// declineOpener turns the change down at the opening of a reply.
var declineOpener = regexp.MustCompile(`(?i)^\W*(?:won't fix|wontfix|will not fix|not going to|no,|nope\b|no\.)`)

// declineNoReason reports a short decline: one paragraph, five words or fewer
// after the decline, and no reason marker.
func declineNoReason(in input) []Finding {
	paras := paragraphs(in.prose, mentionsMasked)
	if len(paras) != 1 || reasonGiven.MatchString(paras[0].text) || purpose.MatchString(paras[0].text) {
		return nil
	}

	text := strings.TrimSpace(paras[0].text)

	at := declineOpener.FindStringIndex(text)
	if at == nil || len(strings.Fields(text[at[1]:])) > 5 {
		return nil
	}

	m := strings.TrimSpace(text[:at[1]])

	return []Finding{{
		Message: fmt.Sprintf("Give the reason with '%s', as in 'Keeping the copy: the caller mutates the "+
			"slice after this returns.'", m),
		Match: m, Line: paras[0].lineAt(at[0]),
	}}
}

var (
	// impatient are the phrases Ferreira et al. found impatience in.
	impatient = regexp.MustCompile(`(?i)\b(?:as|like) I (?:already )?(?:said|mentioned|explained|wrote|pointed out)\b|` +
		`\bI(?:'ve| have)? already (?:said|told|explained|mentioned|answered|pointed out|asked)\b|` +
		`\bfor the (?:last|third|second|nth) time\b|\bobviously\b`)
	// againOpener opens a sentence by repeating it: "Again, the map is shared".
	againOpener = regexp.MustCompile(`(?i)^again,`)
)

const impatienceMessage = "Drop '%s': restate the point plainly, as in 'The map is shared by both workers, so " +
	"it needs the lock.'"

// impatience reports each impatient phrase in a reply, and each sentence it
// opens with "Again,".
func impatience(in input) []Finding {
	out := matchFindings(in, impatient, nil, impatienceMessage)

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		for _, s := range sentenceStarts(para.text) {
			if m := againOpener.FindString(strings.TrimLeft(para.text[s:], "*_( ")); m != "" {
				out = append(out, Finding{Message: fmt.Sprintf(impatienceMessage, m), Match: m, Line: para.lineAt(s)})
			}
		}
	}

	return out
}
