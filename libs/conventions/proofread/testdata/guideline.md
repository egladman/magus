# proofread labeling guideline

You get `candidates.jsonl`, one proofread finding per line, and `labels-template.jsonl`, one row `{id, label, note}` per candidate id. For each finding, decide whether proofread was right or wrong to raise it, and fill in your copy of the template. Work alone. Do not look at the other labeler's sheet, and never open another round's sheets, candidates or adjudicated cases. The round's freeze record names the sha256 of this file; label by the copy that hash names.

## What you label

- Label the finding, not the writing quality. The only question: does the text have the problem this rule's Catches line names, at this match? Ignore whether the text is good, whether you would have written it that way, or whether the writer had a fair reason. A fine paragraph can carry a correct finding; a clumsy one can carry a wrong finding.
- Judge the match in its context. Read `context` (the paragraph, with the match marked `[[...]]`), then `sentence`, then `message` (what the tool said). The same word is a problem in one sense and fine in another. When `match` is empty the finding is about the whole paragraph or the whole text, and the context is wrapped in `[[...]]`.
- The `text` field always carries the full text, even when `context` is cut. When `scope` is `"text"`, the rule judges the whole text: read all of `text`, and `file` when present, before you decide.
- Labels, exactly one of these, in uppercase:
  - `CORRECT`: the text has the problem the rule names, at this match.
  - `WRONG`: it does not. The rule misread a sense, a scope, a parse or a register (see the causes below).
  - `UNSURE`: only when the text given is not enough to decide (a reference to a missing earlier message, a claim you would need to check against the code). Put one line in `note` saying what is missing. Do not use UNSURE because the call is close or because you dislike the rule; decide it. UNSURE counts as WRONG, so use it sparingly.
- For every WRONG, start `note` with one cause tag, then a few words: `scope:` (the match is inside a quote, code span, code fence, pasted log, someone else's words, platform boilerplate, front matter, a title or link text), `sense:` (the word or pattern is used in a different, legitimate sense), `parse:` (a sentence split, list item or heading mangled, or the wrong paragraph), `register:` (fine in this kind of text, such as a reply in a thread both people share), `meaning:` (you would need to know whether the claim is true to decide). A CORRECT may have an empty note.
- Judge each candidate on its own. Do not infer the rule's precision from how many in a row look right or wrong.

## General scope rules (apply to every rule)

- Words quoted from another author (a block quote with `>`, an issue pasted into a pull request body, a reviewer's comment reproduced in a reply), code fences, code spans, program output, stack traces, URLs and file names are not the writer's own prose. A match there is WRONG (scope), even when the quoted words show the problem. The writer's own sentence that mentions the quote is judged normally.
- Platform boilerplate is not the author's prose either: bot footers (a "Generated with ..." line, a co-author trailer, a link to an agent session), pull request template checkboxes and headings left unfilled, a contributor license block, and a CI or bot summary pasted into the body. A match inside it is WRONG (scope), whichever rule matched. For a rule that judges a rate over the whole text, discount the clauses inside the boilerplate and judge by what remains.
- A rule that names a phrase as an example ("Note that", "in order to") catches the whole family; judge by the pattern, not the exact words.
- The kind tells you the genre:
  - `change-description`: a pull request title and body, title on line one.
  - `commit-message`: subject, then body.
  - `review-reply`: one person answering another in a thread they share.
  - `agent-reply`: what a coding agent writes back to its person at the end of a turn.
  - `reference` and `guide`: documentation pages; `guide` is a how-to page.
  - `changelog` and `release-notes`: changelog pages, fragments and release manifests.
  - `issue`: a bug report. Where the source has no issues, a pull request body whose title describes a bug stands in, and the entry says so.
  - `message`: one text a program prints, taken from source code. A verb such as `%s` stands for a run-time value and `<value>` for an operand that was not a literal.
  - `cli-help`: the help text of one command or flag.
  - `tool-description`: the description of an MCP tool, a skill or an agent.
- Some rules are fine in some genres; their entries say so.
- `source` names where the text came from and `record` its place in that source. When present, `file` names the repository file, with `@<commit>` for a historical version.
- Most findings in the `agent-reply` kind quote the agent's own earlier work to a person who watched it happen. Do not excuse a finding because the person could have seen the work; excuse it only where the rule's entry says so.
- Redaction: anything that looked like an email, token or key was replaced with `[REDACTED]`. If the replacement sits inside the match and decides the call, answer UNSURE.

## Agreement

- A rule's labels agree when the two sheets disagree on at most n/10 of its n firings (4 of 40, 2 of 20), or when Krippendorff's alpha (nominal) over the two sheets is at least 0.80.
- Every disagreement needs a note from both labelers. A disagreement where one sheet left the note empty goes back to that labeler for a note before adjudication.
- UNSURE counts as WRONG in the precision: an UNSURE final label lowers it as a WRONG does. In agreement it is a label of its own, so an UNSURE against a WRONG is a disagreement and needs both notes.

## Adjudication

- The adjudicator never tuned the rule being judged and was not one of its two labelers.
- For each disagreement, the adjudicator reads both notes, decides by this guideline, and writes the reason in a few words.
- The adjudicator writes `adjudicated.jsonl` with one row `{id, label, reason, adjudicated}` for every candidate. An agreed candidate gets the agreed label, `adjudicated: false`, and an empty reason or the shared note. A decided disagreement gets `adjudicated: true`.
- Where this guideline left the call open, the adjudicator records the case id and the question it raised. Each such record becomes a revision of this file for the next round.

## Rules

One entry per rule labeled in a round, in alphabetical order. Each opens with the rule's Catches line as the tool states it, then what CORRECT and WRONG mean, the known false-positive shapes, and one worked example of each label. The examples are invented and describe no real code.

### absolute (PRF2003)

- Catches: never, nobody or nothing as a claim about the past ("has never fired", "nobody checked")
- CORRECT: "never", "nobody" or "nothing" as a claim about a history of work or care: never tested, never reviewed, never implemented, nobody checked. The test: the sentence stays true if you replace the word with "no one did X", and it points at people or past work.
- WRONG: the word describes what code or data does or lacks, something you could check by reading it: "the spool hook never runs after a timeout", "the size field is never sent upstream", "the socket opens and never closes". This includes the last step of a causal chain and a bug report's mechanism. Judge by the subject: code, data or a document as the subject is WRONG; a person, a team, or "this path was ... by anyone" is CORRECT. A contract ("never returns nil") and a quoted message or command example are WRONG too.
- WRONG also: "nobody" or "never" inside a clause that names a code condition (an input nobody declared, a flag never set, a key nobody reads). The clause states something you could check in the code or the config, so it is about code, even when the word reads like a person.
- Known false-positive shapes: a bug's mechanism told with "never"; a contract statement in documentation; "never" inside a quoted error string; "nobody" in a relative clause that names a missing declaration.
- CORRECT example: "Nobody profiled the lexer after the tokenizer rewrite."
- WRONG example: "The planner rejects a spool step whose input nobody declared." The clause names a missing declaration in the config.

### agreement-opener (PRF8026)

- Catches: an agent reply that opens with praise or agreement ("Great question", "You're right")
- CORRECT: an agent reply that opens with praise or agreement ("Great question", "You're right", "Good instinct"). "You're right" opening a reply that concedes a correction is still CORRECT: the finding is accurate even where the opener is useful.
- WRONG: the matched words are not an opener of praise or agreement: they sit mid-reply, they are not agreement ("The right fix"), or they are inside a quote.
- Known false-positive shapes: "right" as a plain adjective in the first sentence; a quoted line from the person.
- CORRECT example: "You're right: the lexer drops a trailing tab, and the patch below keeps it."
- WRONG example: "The right fix is to flush the spool buffer before the second retry." No agreement.

### all-caps (PRF8013)

- Catches: words in capitals for emphasis in a review reply ("DO NOT", "NEVER")
- CORRECT: a run of capital words for emphasis that includes an English word such as NOT, NEVER or ALL ("DO NOT merge").
- WRONG: acronyms or constants; ALL or ANY used as technical keywords (SQL, a spec's RFC keywords); capitals inside a code span.
- Known false-positive shapes: RFC 2119 keywords (MUST, MUST NOT); SQL "ALL"; AND, OR and NOT written in capitals as logical operators in a statement about a condition, which is sense, not emphasis.
- CORRECT example: "Please do NOT bump the spool size on this branch."
- WRONG example: "The ledger query keeps duplicate rows with UNION ALL." ALL is a SQL keyword.

### bare-imperative (PRF8012)

- Catches: a short command in a review reply that gives no reason anywhere ("Fix this.")
- CORRECT: a short command (five words or fewer) in a review reply that gives no reason anywhere in the reply and names no code ("Fix this.", "Please rename.").
- WRONG: the reply gives a reason elsewhere, or the command names the code (rename `foo` to `bar`).
- Known false-positive shapes: a short command whose reason sits in the next sentence of the same reply.
- CORRECT example: "Please revert."
- WRONG example: "Please rename it; the three retry callers spell it backoff." A reason follows.

### blame (PRF2001)

- Catches: a person or past work as the subject of a fault, and contempt for code or a decision
- CORRECT: a person or past work as the subject of a fault ("whoever wrote this", "forgot to"), or contempt for code or a decision ("sloppy", "stupid", "crazy").
- WRONG: first person owning a fault ("I forgot") reads as candor and is left alone; "should have" as possession (you should have a tests file); a contempt word inside a name (a user handle or a package name), in its literal sense ("lazy loading"), or in a quote.
- Known false-positive shapes: "sloppy" about the writer's own earlier claim; "not crazy" used to reassure; "they" with no fault named.
- CORRECT example: "The last person to touch the tokenizer neglected the empty case."
- WRONG example: "My first draft of the retry loop was sloppy, so I rewrote it." Own fault, first person.

### body-separator (PRF1013)

- Catches: a commit body that starts on the line after the subject
- CORRECT: a commit body that starts on the line right after the subject, with no blank line between them.
- WRONG: the message is a subject plus trailers ("Key: value" lines), which is left alone; or the first line was mis-split, so what the rule took for the subject is not one.
- Known false-positive shapes: a subject followed directly by a trailer block.
- CORRECT example: the subject "fix the spool flush", then a prose line directly beneath it that says why.
- WRONG example: the subject "fix the spool flush", then a sign-off trailer directly beneath it.

### bold-label (PRF8020)

- Catches: list items or paragraphs of an agent reply that open with a bold label
- CORRECT: list items or paragraphs of an agent reply that open with a bold label, turning points into fields of a form (`**Cause:**`, `**Fix:**`).
- WRONG: a bold UI name, a defined term or emphasis inside a running sentence; a bold word in the middle of a line; bold inside a table cell.
- Known false-positive shapes: bold for emphasis in a sentence. A bold lead that is itself a full sentence of the point is CORRECT when the bold part reads as a field name followed by a value.
- CORRECT example: "- **Cause:** the spool lock is held. - **Fix:** drop it before the retry."
- WRONG example: "Press **Rebuild** to clear the parser cache." A UI name.

### buzzword (PRF4006)

- Catches: a word chosen to sound significant rather than to say what is so ("delve", "tapestry")
- CORRECT: a stock phrase with a plain equivalent, even when its meaning is clear: "moving forward" (from now on), "lean into", "deep dive", "delve", "tapestry", "pivotal" in its buzzword sense. "Intricate" as praise alone is CORRECT. A heading or label the writer chose is CORRECT too ("Deep Dive" over a section of the writer's own).
- WRONG: the literal sense: moving a widget forward; "same page" naming a page; "vibrant" as a colour. "Deep dive" as the title of an existing document that the sentence links or cites is WRONG. A match that spans a file name or an identifier is WRONG (scope). "Intricate" stating a cost of a described scheme (hard to get right, hard to test) is WRONG.
- Known false-positive shapes: the literal senses above; identifiers that contain the phrase.
- CORRECT example: "Moving forward, the parser rejects tabs in keys."
- WRONG example: "The palette's vibrant amber marks a stale spool entry." A colour.

### buzzword-weak (PRF4007)

- Catches: a buzzword that also has an ordinary sense ("crucial", "landscape")
- The test is the word itself, not the sentence: could this one word be replaced by a plainer word that says the same thing? If the plainer word is a synonym ("add" for "enhance", "also" for "Additionally"), the sentence already says what is so: WRONG.
- CORRECT: the word carries a claim nothing else in the sentence or the next one supports: "a crucial improvement", "aligns with common conventions" (which?), "enhanced experience" (how?), "comprehensive" with no coverage named. A follow-up that only restates the claim ("clearer", "better") is not a specific.
- WRONG: specifics follow the word (enhances the lexer with tab handling); a reason follows ("valuable, since ..."); the word has its plain sense and names something ("landscape" as page orientation, "enhanced" as a mode name). Connectives ("Additionally", "Moreover") are WRONG (sense): they are not buzzwords.
- Known false-positive shapes: product, mode and setting names; orientation words; a verb with its specifics right after it.
- CORRECT example: "This is a crucial improvement to the retry experience."
- WRONG example: "Enhances the lexer with tab and CRLF handling." Specifics follow.

### changelog-group (PRF1031)

- Catches: a changelog heading that is not Added, Changed, Deprecated, Removed, Fixed or Security
- CORRECT: a changelog heading that names a group other than Added, Changed, Deprecated, Removed, Fixed or Security ("Breaking", "Bug fixes", "Features"). In release notes, only a plain synonym of one of the six ("Bug fixes", "Features") counts.
- WRONG: the heading is one of the six, or is not a group heading (a version heading, a fragment title, a heading in a code sample).
- Known false-positive shapes: a group the repository's fragment grammar accepts but Keep a Changelog does not name: label by the message, which names the six.
- CORRECT example: "### Bug fixes" in a changelog.
- WRONG example: "### Fixed".

### changelog-heading (PRF1030)

- Catches: a changelog version heading that is not "## [version] - date" or "## [Unreleased]"
- CORRECT: a version heading in another shape: a version without brackets, a missing date, a different date shape, or a heading with another word in it.
- WRONG: the heading is not a version heading (a group heading, a fragment title, a heading inside a quoted entry), or the match is inside a code fence. A leading "v" inside the brackets follows the heading shape and is WRONG when the message objects only to the brackets or the date; label by what the message says.
- Known false-positive shapes: a fragment under `changes/unreleased/` has no version heading and is not judged by it; "## [Unreleased]" is correct as written.
- CORRECT example: "## 2.7.0 (2031)" where "## [2.7.0] - 2031-04-09" is wanted.
- WRONG example: "### Fixed", a group heading, not a version.

### chatbot (PRF4004)

- Catches: text a chat assistant addressed to its user: an offer, flattery, a knowledge disclaimer
- CORRECT: text a chat assistant addressed to its user: an offer ("Let me know"), flattery ("Great question"), a knowledge disclaimer ("As an AI"), a letter opener ("Dear", "I am writing") in a change description.
- WRONG: in a review reply, an agreement, an invitation or an answer's opener between two people who share the thread ("You're right", "feel free", "great catch"); the phrase is in a quote of someone else (an issue pasted into a pull request body); or the text is documentation about chat assistants.
- Known false-positive shapes: "Feel free" in a block quote; "let me know" inside a quoted request.
- CORRECT example: a pull request body about a parser fix that ends "Hope this helps!" and then offers to make any changes the reader wants.
- WRONG example: a review reply "Good catch; the spool test covers that now." Two people in one thread.

### claim (PRF3001)

- Catches: a measurement, a comparison or a completion with no evidence in its sentence or bullet
- CORRECT: a completion ("fixes", "all tests pass") or a measurement with no test, command, run name, link, issue, commit or ref in its sentence or bullet. A mechanism (fixes the bug by moving the check) is not evidence that the fix works.
- CORRECT also: a benefit verb ("eliminates", "removes", "prevents") that reports an observed result, a completion stated as what happened after the change, with nothing in the sentence backing it.
- The evidence must back the flagged figure itself. A test name, run or link in the same sentence that backs a different claim in it does not count: in a sentence joined by semicolons, a "Verified:" clause about one figure leaves the other figure unbacked.
- WRONG: a benefit verb stating what the design does (the lock prevents two writers from sharing a spool); a count or design statement that is not a measurement (teaches 2 of the 9 keys, before the list that shows them; "Smaller" as a ranking label); a target stated with "capable of" or "can handle", unless the sentence presents it as measured; a progress tally against a tracked list ("12 of 30 done"), which is a status, not a measurement; a measurement that names the run it came from; a setting (a 300s bound); a limit ("Not measured"); a quoted figure.
- A colon that introduces a list counts as the same bullet: evidence in that list is beside the claim.
- Known false-positive shapes: settings and sizes read as measurements; the elapsed time of a process still running (a status, not a claim of result); a design benefit read as a result.
- CORRECT example: "Cold parser startup is now three times faster."
- WRONG example: "Cold parser startup is three times faster (`go test -bench=ColdParse`: 420ms to 140ms)." The benchmark backs the figure.

### closer (PRF4009)

- Catches: a sentence that opens by announcing it restates the text above ("In conclusion,")
- CORRECT: a sentence that opens by announcing it restates the text above: "In conclusion,", "To summarize,", "Overall,".
- WRONG: it opens a summary a reader needs after a long page and that states the point nowhere else; or it is a quote.
- Known false-positive shapes: "In summary" at the head of a TL;DR that is the only place the point is stated.
- CORRECT example: "In conclusion, the new spool is smaller and simpler."
- WRONG example: a block-quoted line of a pasted issue that opens "To summarize,".

### closing-offer (PRF8021)

- Catches: an agent reply whose last paragraph offers more work or asks leave to go on
- CORRECT: an agent reply whose last paragraph offers more work or asks leave to go on, where the agent could pick and proceed: the order of work, whether to do more of the same, an optional extra ("If you want, ..."), or asking leave while it continues anyway.
- WRONG (a needed decision) only when the agent cannot proceed without the person: an action the agent is not allowed to take alone (push, open a pull request, merge, publish), a value only the person holds (a cap, a name, a credential), or a precondition the person controls. WRONG also when the offer is not in the last paragraph.
- Known false-positive shapes: a needed question. A question about which of two orders to take is not a needed decision.
- CORRECT example: "Want me to also update the retry docs?" The agent could do it or skip it.
- WRONG example: "Should I push the parser branch or keep it local? Pushing needs your go-ahead."

### condescension (PRF2006)

- Catches: a word that tells the reader how hard a step should feel or what they should already know
- CORRECT: a word that tells the reader how hard a step should feel or what they should already know: "simply run", "of course", "obviously", "everyone knows", "trivially".
- WRONG: "easy" or "obvious" used as a warning (easy to get wrong) or as a plain description of code; "just" as recency or restriction; the word is inside a quote or a code span.
- Known false-positive shapes: "obviously" describing data is still CORRECT when it presumes what the reader knows; "easy" or "simple" naming a measured property (a simple loop with no branches) can be WRONG (sense).
- CORRECT example: "Simply delete the spool directory and rerun."
- WRONG example: "Retry hooks are easy to misorder, so the loader sorts them." The word warns.

### contrast (PRF4010)

- Catches: a claim made by denying its opposite first ("not just X, it is Y")
- CORRECT: a claim made by denying its opposite first: "not just X, it is Y", "isn't a choice; it's", "is not whether".
- WRONG: the writer is correcting a position that was stated in the thread or the text; it is a plain "not X but Y" with a real X someone holds (judge with context); or the pattern is quoted.
- Known false-positive shapes: documentation that states a design rule on purpose (the rule only advises outside change descriptions for that reason, but label by the pattern).
- CORRECT example: "This is not just a rename, it is a rework of the spool."
- WRONG example: "Retries are not on by default; they stay off until you set a limit." A plain statement, no argued opposite.

### credit (PRF2005)

- Catches: a change description that removes or replaces something and says nothing of what it was for
- The rule asks what the removed or replaced thing did or why it was there, not why it goes.
- CORRECT: the description gives only the reason for the change ("in favor of", "for reliability", an issue number, the new thing's purpose) and says nothing of what the old thing did.
- WRONG: a clause says what the old thing did, did well or was for; the title verb is not a removal ("Remove" as a UI action label); or the change adds something.
- Known false-positive shapes: a title that removes an unused import, where nothing of purpose is expected (still CORRECT; mark WRONG only when the body gives the earlier purpose).
- CORRECT example: title "Delete the legacy spool" with a body that lists the removed files and says "in favor of the new writer".
- WRONG example: "Replace the v1 spool; it kept partial writes out of the cache, and the new one does the same per shard." It says what the old one was for.

### filler (PRF4001)

- Catches: throat-clearing ("Note that") and filler adverbs ("simply", "basically")
- CORRECT: a throat-clearing opener ("Note that", "Worth noting") or a filler adverb or modifier that adds nothing ("simply", "basically", "seamlessly", "robust", "really" as an intensifier) and could be deleted without losing a fact.
- WRONG: the word carries meaning in that sentence: "just" as "merely" is filler, but "just" as recency ("just pushed") or a literal ("just the header") is not; "very" grading a size or position; "actually" stating a contrast mid-sentence (only the opener "Actually," counts); a hyphenated compound ("all-powerful"); "more robust" as a comparison; "This function" outside a doc comment.
- Known false-positive shapes: lowercase "actually" as contrast; "really" meaning "in reality"; "robust" in a technical sense with a stated property (robust to reordering).
- CORRECT example: "It is worth noting that the spool is seamlessly flushed." The opener and the adverb add nothing.
- WRONG example: "The lexer fails when the key is actually blank, not just short." "Actually" contrasts with an assumption; "just" is part of a contrast.

### heading-case (PRF4013)

- Catches: a heading whose every word after the first is capitalized
- CORRECT: a heading whose every word after the first is capitalized, as a Title Case heading.
- WRONG: the capitals are proper nouns, product names, acronyms or command names; or the heading has one or two words.
- Known false-positive shapes: lists of product names; acronyms.
- CORRECT example: "## Configuring The Spool Writer"
- WRONG example: "## Formats: CSV, TSV, JSON" Acronyms.

### hedge (PRF3002)

- Catches: a softener qualifying a claim ("might fix", "could potentially")
- CORRECT: a softener qualifying a claim the writer could have scoped instead: "might fix", "could potentially", "seems to", "probably", "perhaps".
- WRONG: the hedge states a real uncertainty of the thing described (a nondeterministic event; may fail if the network drops, as a documented condition); it appears inside a quote; or the sentence sits under a "Not verified" heading or opens "Not measured" or "Untested" (these state a limit and are exempt).
- Known false-positive shapes: "may" as permission ("the user may"); "could" in a conditional about the system; hedging inside a quoted issue.
- CORRECT example: "This should probably cure the spool flake." The writer ran nothing and does not say so.
- WRONG example: "Not measured on a cold disk; first-run spool timings may differ." It states a limit.

### help-length (PRF9011)

- Catches: help text over 240 runes
- CORRECT: help text over 240 runes (the message states the count).
- WRONG: the count includes text the help does not print as help (an example block printed separately), or the text is not help for one command or flag.
- Known false-positive shapes: a command description that the CLI prints in a different place from the flag table. A text the message measures at 240 or fewer is not a finding at all, so label by the stated count.
- CORRECT example: a 300-rune description of one flag.
- WRONG example: a 100-rune description; if you see one, the count was measured on the wrong thing.

### help-sentence (PRF9010)

- Catches: a sentence of help text over 40 words
- CORRECT: a sentence of help text over 40 words (the message states the count). A long comma list of items is still one sentence a reader scans; label it CORRECT unless it is plainly a table of values.
- WRONG: the "sentence" is a list of values, a usage line or an example that the rule counted as one sentence.
- Known false-positive shapes: an enum of allowed values; a path or URL read as words.
- CORRECT example: one 55-word sentence in a flag description.
- WRONG example: a 45-token list of enum values with no verb.

### ing-tail (PRF4011)

- Catches: a participle clause added to claim significance (", highlighting the importance of")
- CORRECT: the participle clause claims importance, benefit or quality the sentence does not show: ", ensuring reliability", ", highlighting the importance of", ", improving maintainability".
- WRONG: the tail states a checkable fact, even with a stock verb: a version change, the reason for a change (", reflecting the new limit"), or a file's contents (", listing the retry hooks"). A tail that reports someone's emphasis with a checkable object also states a fact: the reviewer asked for a fix, emphasizing that the key include the platform. WRONG also: a real action (", running the tests first"), or a quoted line.
- Known false-positive shapes: a participle naming a concrete step; ", reflecting" or ", emphasizing" followed by a fact; a tail naming what a file holds.
- CORRECT example: "The spool is warmed first, ensuring a smooth developer journey."
- WRONG example: "The script reads the spool, then writes the index, leaving the input untouched." A fact.

### intent (PRF2004)

- Catches: a motive given to a tool or a person ("guessed", "pretends")
- CORRECT: a motive given to a tool or a person: "guessed", "pretends", "tries to hide", "lies" in the sense of deceiving.
- WRONG: "lies" as location (the bug lies in the parser); "guessed" about the writer's own earlier test; a tool's documented behavior named in plain words (falls back to a guess, in a mechanism sense; judge the sentence).
- Known false-positive shapes: first person (my first test guessed); "lies" as position.
- CORRECT example: "The uploader pretends the spool was flushed."
- WRONG example: "The fault lies in how the spool path is joined." Location.

### issue-repro (PRF1020)

- Catches: a bug report with neither what happened against what was expected, nor steps to reproduce it
- Source note: where the source has no issues, the candidates are pull request bodies whose titles describe a bug, judged as an issue. Read the title and the body as a bug report.
- CORRECT: the title has a defect word ("crash", "fails", "regression", "broken") and the body gives neither what happened against what was expected, nor steps to reproduce. A fix description that states the cause and the fix but no steps is still CORRECT: it names neither expected nor actual.
- WRONG: the body does give expected and actual, or steps, in other words (a before and after, now returns X instead of Y, a command to run). Before and after code annotated with its behavior (a comment saying the old loop drops the last record, and the new one keeps every record) counts as actual against expected. WRONG also when the title is not a defect report (a feature or a refactor using "error" or "fail" as a topic word).
- Known false-positive shapes: a title that names an error handling feature; annotated before and after code read as a bare fix.
- CORRECT example: title "Spool flush fails", body "Moves the flush before the close." Cause and fix, no expected or actual.
- WRONG example: title "Spool flush fails", body "Running `spool flush --all` prints a lock error; I expected the files on disk."

### judgment-as-fact (PRF8002)

- Catches: a recommendation in a review reply stated as a fact, with no reason given
- CORRECT: a recommendation in a review reply stated as a fact with no reason ("This should be").
- WRONG: the modal states a requirement of the language or the tool (must be exported for the linker); a reason follows in the same reply; or the text is a label saying it is the writer's call.
- Known false-positive shapes: "should" for an invariant the code documents; a reason in the next sentence.
- CORRECT example: "This should be a ring buffer."
- WRONG example: "This should be a ring buffer, since the writer drops old entries anyway." A reason follows.

### lead-context (PRF1001)

- Catches: a change description that does not open with a paragraph naming what a reader can now do
- Label the content, not the opener. The test for an effect: the lead names what a reader can now do, no longer has to do, or what stops happening, beyond the change itself. A lead that names only the change (deprecates the package, refactors the store) names no effect.
- CORRECT: a list, checklist, heading or reply opener in the lead's place; a lead that names only the mechanism; a lead under 12 words that names no effect. A heading in the lead's place is CORRECT even when the paragraph under it names the effect, unless the heading is the title line.
- WRONG: the lead names an effect in any words, including a short lead for a trivial change (fixed spelling errors in the setup script). "This PR" before such a statement does not make it CORRECT. WRONG also when the rule mis-split the lead (the title line or a quoted issue taken for it).
- Known false-positive shapes: an outcome phrased in words the rule does not know; a short lead that is complete because the change is trivial.
- CORRECT example: a body that opens with a checklist item and no sentence naming what changes for a reader.
- WRONG example: "Retries now stop after the third timeout, so a dead mirror no longer stalls the build for ten minutes." It names the effect.

### leak (PRF4005)

- Catches: residue of a tool or a template: a citation marker or an unfilled placeholder
- CORRECT: residue of a tool or a template: a citation marker (`oaicite`, `[cite: 1]`), an unfilled placeholder (`[insert name]`, `TBD`, `<describe the change>`). "TBD" as a word in a sentence is CORRECT only when it stands in for missing content.
- WRONG: link text naming a file and line (`[spool.go:88]`); bracket punctuation of another script around a label; a placeholder that is a documented example in a template page; a quoted issue template; `TBD` inside a quote; a sentence that talks about a marker rather than leaving one (scope).
- Known false-positive shapes: file and line links, bracketed labels in other scripts, quoted templates.
- CORRECT example: "Fixes the lexer [cite: 2]."
- WRONG example: "See [spool.go:88] for the default." A file and line link.

### long-thread (PRF8004)

- Catches: a review reply that is its author's fourth or later in a thread
- CORRECT: the candidate carries `thread_length`, the count of replies the author already posted, and `thread`, the earlier comments (AUTHOR is the same person, OTHER anyone else), up to six, each cut to 160 characters. CORRECT when `thread_length` is 3 or more and the comment is a reply in a thread.
- WRONG: the count was wrong (the earlier AUTHOR entries are not replies by this author, or fewer than three are shown and nothing says more exist), or the comment is not a reply.
- UNSURE: the shown thread cannot confirm the count and `thread_length` is missing.
- Known false-positive shapes: a thread where the author's earlier comments are their own root comment, not replies.
- CORRECT example: the fifth reply by one person in one thread.
- WRONG example: a first comment on a line.

### message-commands (PRF9003)

- Catches: a message naming more than one backticked command
- Source note: the texts are format strings and guard denial reasons from the Go source. A backticked `%s` or `<value>` that stands for a command counts as a command.
- CORRECT: a message naming more than one backticked command (the message says how many).
- WRONG: only one command, or the backticks hold a path, a flag, a config key or a value, not a command a reader would run.
- Known false-positive shapes: backticked file paths and YAML snippets; one command named once and echoed in an example of the same command.
- CORRECT example: "Run `spool check`, then `spool repair --all`."
- WRONG example: "Run `spool check` (see `docs/spool.md`)." A path, not a second command.

### message-length (PRF9001)

- Catches: a message longer than its rune cap, 160 unless the caller names one
- Source note: the texts are format strings and guard denial reasons from the Go source. A verb such as `%s`, `%q` or `%d` stands for a run-time value; a `<value>` stands for an operand that is not a literal. Count the text as shown.
- CORRECT: a message longer than its rune cap, in one piece a person reads as one message.
- WRONG: the length was measured on something that is not one message: a verdict followed by a rationale kept behind a stored reference, or text joined from several messages.
- Known false-positive shapes: a denial whose first line is the printed verdict and whose later lines are shown only behind a ref.
- CORRECT example: a 220-character error line with a clause for each of three causes.
- WRONG example: two separate messages joined by the extractor and measured as one.

### message-rationale (PRF9002)

- Catches: a message that joins more than one reason (so, because, a semicolon, ", which")
- Source note: format strings and guard denial reasons from the Go source; verbs such as `%s` stand for run-time values.
- Count every explaining clause after the verdict. A cause plus a consequence joined by "so" or ", which" is two reasons.
- CORRECT: a message with two or more explaining clauses.
- WRONG: a single explaining clause; a semicolon that separates a reason from the next action (a remedy), not from a second reason; a "because" that justifies the remedy rather than explaining the failure; "since" in its temporal sense (has since moved); "because" or "so" inside a quoted value or a name.
- Known false-positive shapes: a semicolon before a remedy; temporal "since".
- CORRECT example: "cannot flush the spool because the lock is held, which stalls every writer"
- WRONG example: "cannot flush the spool because the lock is held; run `spool unlock`" One reason, then a remedy.

### message-tag (PRF9004)

- Catches: a message opening with a component tag ("server: ") or carrying a marker such as "[AGENT]"
- Source note: format strings and guard denial reasons from the Go source.
- CORRECT: a message opening with a component tag ("auth: ", "server: ") or carrying a marker such as "[AGENT]".
- WRONG: the opening word is part of the sentence itself, not a tag for who spoke; the tag-like text is a quoted value; or the leading verb is `%s` standing for the whole message.
- Known false-positive shapes: a command name before a colon that the message needs: label CORRECT when it names the component that spoke, WRONG when it names the thing the message is about.
- CORRECT example: "spooler: cannot open the queue file"
- WRONG example: "Cannot open the queue file for writing."

### nominalizations (PRF4021)

- Catches: a text whose verbs are turned into nouns (validation, agreement, stability) at a generated-writing rate
- Read three sentences of running prose. List items and checklist lines are not running prose.
- CORRECT: those sentences turn actions into nouns that an actor could perform (the creation of the store before the registration of the service; improves maintainability and organization), even when the nouns are also domain terms.
- WRONG: the count comes mostly from code, a directory tree, headings, front matter, tables, list items or checklist lines; or from one domain noun repeated as a name, which is when one noun makes up more than half the count ("the Configuration file", "the Validation tab").
- Known false-positive shapes: technical nouns such as "configuration", "application" and "version" used as names of things; a count driven by a file tree, a table or a bullet list of topics.
- CORRECT example: a body full of sentences like "the implementation of the rotation of the spool requires the coordination of the writers".
- WRONG example: a page whose count comes from a bullet list of section names and a directory listing.

### nonspecific (PRF8010)

- Catches: a top-level review comment that judges or asks for a change and names no code, path, line, example or reason
- Candidates are top-level comments only. A reply anchored to a diff line is judged with `-anchored`, and the rule skips it: the line is its referent. If an anchored comment (one that shows a path and a line) appears anyway, it is WRONG (scope).
- CORRECT: a top-level comment that judges or asks for a change and names nothing a reader could find from where it sits: no code, path, line, example or reason.
- WRONG: the comment names the code, a path, a line, an example or a reason you can read in the same comment, or carries a suggestion block.
- Known false-positive shapes: a top-level comment that names its file or function in a noun phrase; a suggestion block.
- CORRECT example: a review summary that says only "Not convinced by this approach, please redo the spool part."
- WRONG example: "Rename `spoolDir` to `spoolRoot`; every other file uses the second name."

### option-list (PRF8023)

- Catches: an agent reply that lays out labeled options or alternatives
- CORRECT: an agent reply that lays out labeled options or alternatives the person did not ask for (Option A, Option B). The only exception is that the person asked for options. "Your call" or a real trade-off does not make it WRONG; the rule's fix is to recommend one and say why. Without the question visible, label CORRECT.
- WRONG: the person asked for the choices; the labels are not alternatives (steps, cases of one design); or the sentence compares options already laid out earlier rather than laying out a menu (the third option is the quickest to build).
- Known false-positive shapes: a reply to a question asking what the options are; labeled steps of one plan.
- CORRECT example: an unprompted menu of two bold-labeled ways to shard the spool that ends by asking which one the person wants.
- WRONG example: a reply to a question asking for the ways to shard the spool, which lists them.

### participles (PRF4020)

- Catches: a text that hangs present participial clauses on its sentences at a generated-writing rate
- The message states the count, the words, the rate per 1000 words and the cap, and quotes each clause it counted. Judge those clauses, not others you find.
- First strike every counted clause that sits in platform boilerplate (a bot footer, a template line, a pasted summary). If the clauses left no longer put the author's own words over the cap, label WRONG (scope).
- A "claimed-significance" clause is a comma plus an -ing tail whose content is a benefit, an effect or a result: ", giving tidier logs", ", wasting retries", ", raising the count to 212". A concrete effect still counts: the rule measures the shape, not the truth of the content.
- CORRECT: a text of 200 words or more where at least half of the counted clauses left after the strike are claimed-significance tails.
- WRONG: the count comes mostly from gerunds after a preposition ("by ensuring", "for testing"), from code, logs, front matter or tables, from list items that start with an -ing verb, or from boilerplate.
- UNSURE: only when the message quotes no clauses and the text is cut before you can find half of the count.
- Known false-positive shapes: front matter and generated reference pages counted as prose; gerunds after "by"; a text whose participles sit in a code sample or a bot footer.
- CORRECT example: a pull request body where most counted clauses end a sentence with ", improving X", ", ensuring Y" or ", enabling Z".
- WRONG example: a pull request body whose counted clauses sit two in a bot footer and one after "by"; the author's own words hold one tail.

### repeated-marks (PRF8014)

- Catches: a run of question or exclamation marks in a review reply ("??", "!!", "?!")
- CORRECT: a run of question or exclamation marks ("??", "!!", "?!") in a review reply.
- WRONG: an operator or text in a code span or code fence (`??` for null-coalescing).
- Known false-positive shapes: operators quoted outside a code span.
- CORRECT example: "Why does the spool grow??"
- WRONG example: "This reads cleaner with `??` here." An operator in a code span.

### reply-opener (PRF8001)

- Catches: a sentence of a review reply that opens by contradicting ("No,", "As I said")
- CORRECT: a sentence of a review reply that opens by contradicting: "No,", "As I said", "That's wrong". A "No," that denies what the other person said or proposed is CORRECT even when a reason follows.
- WRONG: "No" is part of another phrase ("No problem", "No worries", "No changes needed"); "No," is a plain factual answer to a question that was not a challenge; or the phrase sits in a quote of someone else.
- Known false-positive shapes: "No issues here"; "Again," as a plain sequence word with no sense of repeating oneself.
- CORRECT example: "As I said, the spool cannot overflow here."
- WRONG example: "No problem; I'll rebase onto the parser fix."

### reply-voice (PRF1002)

- Catches: text that answers a prompt the reader never saw: a reply opener, a bold-label item, a stock label
- CORRECT: text that answers a prompt the reader never saw: a reply opener ("Great question", "Sure, here is"), a reference to unseen discussion ("as discussed", "as requested"), a bold-label list item (`**Cache:**`), or a stock label heading ("Summary").
- WRONG: the phrase is part of a real thread the reader shares, a quoted request, or a bold label that is a legitimate term (a defined term, a UI name, a table column name); or a heading that is the page's own subject.
- Known false-positive shapes: "as requested" where the request is linked; a bold UI label; "Summary" as a real section of a long page.
- CORRECT example: a pull request body whose bullets each open with a bold field name such as "Changes:" and "Impact:", like a form filled in for an unseen prompt.
- WRONG example: "Renames `--spool` to `--queue-dir` to match the linked issue, as requested there." The request is linked.

### request-recap (PRF8022)

- Catches: an agent reply that opens by restating what the person asked
- CORRECT: an agent reply that opens by restating what the person asked ("You asked", "To answer").
- WRONG: the restatement corrects a misreading, or "You need" starts the answer's instruction rather than a recap.
- Known false-positive shapes: an opening that restates the question to correct it.
- CORRECT example: "You asked whether the spool is per user. It is."
- WRONG example: "You need one flag for this: pass `--no-spool`."

### second-person (PRF1003)

- Catches: we, us, our or ours in a guide, which speaks to the reader as you
- The rule is judged only on pages typed as guides (`docs/guides/**`). A candidate from any other page (a README, a blog post, a design record, a concept page, a skill) is WRONG (scope).
- CORRECT: "we", "us", "our" or "ours" doing a step the reader does, blurring who acts (now we run the build).
- WRONG: "Let's" in a tutorial, taking the reader along; the organizational "we", naming the project in what it offers or advises ("we support", "we recommend against"); a heading or link text; a sentence that names the reader beside the project or speaks as "I"; "we" inside a quote, a code sample or a log line; "our" inside a product name.
- Known false-positive shapes: a page that is not a guide; "Let's" opening a tutorial step; the project's support policy stated as "we".
- CORRECT example: "Next we point the spool at a scratch directory."
- WRONG example: "We do not recommend a shared spool across hosts." The project states what it advises.

### short-reply-heading (PRF8025)

- Catches: a heading in an agent reply under 300 words
- CORRECT: a heading in an agent reply whose prose runs under 300 words.
- WRONG: the "heading" is a quoted line (a `> #` inside a quote) or a code comment, or the reply runs longer than 300 words.
- Known false-positive shapes: a `#` line inside a quote or a code fence.
- CORRECT example: a 150-word reply with a "## Summary" heading.
- WRONG example: a code fence holding a shell comment that starts with `#`.

### signpost (PRF4003)

- Catches: an announcement standing where the point should be ("Here's the thing", "Let's dive in")
- CORRECT: a lead-in that can be deleted with no fact lost, even when the point follows in the same sentence: "It turns out" (write the point), "Here's the thing", "Honestly?", "Look,", "Let's dive in". "Honestly" as an opener is CORRECT.
- WRONG: the phrase carries content that deleting would lose: "What if" asking a real question, "Here's how" captioning a code block or a list, or a quote. "Honestly" as a manner adverb inside the clause ("answer honestly") is WRONG.
- Known false-positive shapes: "Here's the list:" before a list; "What if" as a genuine question; legal or template boilerplate (see the general scope rules).
- CORRECT example: "Here's the thing: the spool lock is held across the retry." Delete the lead-in and nothing is lost.
- WRONG example: "Here is the spool patch:" followed by the patch. The phrase captions what follows.

### staccato (PRF4012)

- Catches: three or more consecutive sentences of six words or fewer in one paragraph
- CORRECT: three or more consecutive sentences of six words or fewer in one paragraph that read as a drumbeat.
- WRONG: the short sentences are a list, a table of facts, generated reference lines, or lines of a quote or a log; or the "sentences" came from splitting a path or an abbreviation.
- Known false-positive shapes: generated reference pages with one-line statements; log output; a split at an abbreviation such as "e.g.".
- CORRECT example: "The spool filled. Writes stalled. We checked. The lock leaked."
- WRONG example: a block of file-header lines such as "Package spool. Defined in writer.go. Generated."

### stacked-hedge (PRF8003)

- Catches: two softeners in one sentence of a review reply, or an apology before its point
- CORRECT: two softeners in one sentence of a review reply, or an apology before the point.
- WRONG: only one softener; the two words are not softeners (a literal "sorry" in a quoted message); or the hedge is a real uncertainty of the facts.
- Known false-positive shapes: "I think maybe" counts as two; "sort of" in a technical term.
- CORRECT example: "Sorry, I think this could maybe race with the flusher."
- WRONG example: "Might race with the flusher." One softener.

### step-verb (PRF1004)

- Catches: a numbered step of a guide that does not open with its imperative verb
- A numbered list is a procedure only when most of its items open with an imperative. A list that mixes imperatives with claims or notes is otherwise a list of notes, and the rule's finding on it is WRONG.
- CORRECT: a numbered step in a procedure list that does not open with its imperative verb (the config file is created; a new directory for the project).
- WRONG: the list is not a procedure (a recap, a precedence order, a list of reasons, a numbered list of claims), or the step opens with an imperative the parser did not see (a verb after a bold lead, a link or a code span).
- Known false-positive shapes: items opening with a code span naming a command; a numbered list of principles that holds one imperative; a step that continues the previous step's sentence.
- CORRECT example: "3. A scratch directory for the spool." in a how-to list of actions.
- WRONG example: "1. Spool hit. 2. Spool miss." as an ordered list of outcomes.

### subject-length (PRF1011)

- Catches: a commit subject over 100 bytes
- CORRECT: a commit subject over 100 bytes.
- WRONG: the first line was mis-split (the body joined into the subject), or the extra length is a forge-appended pull request number.
- Known false-positive shapes: a message with no blank line after the subject (look at `context`).
- CORRECT example: a 140-character subject joining three clauses with semicolons.
- WRONG example: a 96-byte subject that runs past 100 only with the forge's " (#123)" suffix.

### subject-mood (PRF1010)

- Catches: a commit subject opening in the past tense, the third person or a gerund ("added", "fixes", "making")
- CORRECT: a commit subject opening in the past tense, the third person or a gerund ("added", "fixes", "making") where the imperative is wanted.
- WRONG: the first word is a plural noun that is also a verb (changes to the key), a noun phrase title, or a revert or merge subject form.
- Known false-positive shapes: a subject opening "changes to"; "updates and fixes" as a noun list.
- CORRECT example: "fixed spool flush ordering"
- WRONG example: "changes to the spool format" as a noun phrase.

### subject-period (PRF1012)

- Catches: a commit subject ending in a period
- CORRECT: a commit subject ending in a period.
- WRONG: the subject ends in "..." (left alone), or the period belongs to an abbreviation or a version.
- Known false-positive shapes: a subject ending in an abbreviation such as "etc.".
- CORRECT example: "fix the spool flush."
- WRONG example: "bump spool to 2.7.0" with no final period.

### tool-boundary (PRF9021)

- Catches: a description that names no case where the tool is the wrong choice and no tool to use instead
- Source note: the texts are descriptions of this repository's MCP tools, installed skills and agents.
- CORRECT: the description names no case where the tool is the wrong choice and no tool to use instead. Read the whole description before you decide. A redirect that only says where other material belongs, without naming a case where this tool is the wrong choice, is not a boundary.
- WRONG: it does name a boundary anywhere ("Do not use", "instead use", "rather than", "last resort", a "Do NOT use" clause).
- Known false-positive shapes: a boundary phrased without the rule's key words but plainly saying when not to use the tool.
- CORRECT example: a description that only says what the tool does.
- WRONG example: a description ending "Do not use it on a live spool; stop the writer first."

### tool-lead (PRF9020)

- Catches: a description whose first sentence opens on the tool itself, holds under 4 words or runs past 40 words
- Source note: the texts are descriptions of this repository's MCP tools, installed skills and agents.
- CORRECT: a description whose first sentence opens on the tool itself, holds under 4 words or runs past 40 words (the message states the count).
- WRONG: the first sentence was cut at an abbreviation or an identifier containing a dot, so it is shorter or longer than the message says; or the sentence is within bounds when you count it yourself.
- Known false-positive shapes: a first sentence split at "e.g." or at a dotted identifier; a hyphenated list counted as many words.
- CORRECT example: a 45-word first sentence that lists nouns before it reaches the verb.
- WRONG example: "Flush, compact and verify a spool directory before a release build." Within bounds.

### tool-length (PRF9022)

- Catches: a tool or skill description over 1024 runes
- Source note: the texts are descriptions of this repository's MCP tools, installed skills and agents.
- CORRECT: a description over 1024 runes (the message states the count).
- WRONG: the count includes text that is not part of the description (an example block, a separate parameter list).
- Known false-positive shapes: a description joined with its parameter descriptions.
- CORRECT example: a 1200-rune description.
- WRONG example: a 600-rune description measured with its parameter list.

### unbacked-done (PRF8024)

- Catches: a claim of done, fixed, verified or passing with no command, output ref, file or link beside it
- CORRECT: a claim of done, fixed, verified or passing with no command, output ref, file or link beside it.
- WRONG: a negated or conditional claim ("not verified", "once it passes"), or the evidence sits in the same paragraph or bullet (a backticked command, a file, a link).
- Known false-positive shapes: the evidence appears in a different paragraph; count it only if it is in the context shown, otherwise answer UNSURE.
- CORRECT example: "Fixed and verified."
- WRONG example: "Fixed; `go test ./spool/...` passes." The command sits beside the claim.

### vague (PRF4008)

- Catches: weight or consensus asserted with nothing named ("experts argue", "the stakes are high")
- CORRECT: weight or consensus asserted with nothing named: "experts argue", "studies show", "the stakes are high", or "the real problem" in running text with no specifics. Judge "the real problem" by whether the same or the next sentence names the problem.
- WRONG: the sentence names the source or the stake; a heading, or a parenthesis inside a heading, labels a section whose body names the thing.
- Known false-positive shapes: "the real problem" followed at once by the named problem; a section heading.
- CORRECT example: "Most maintainers agree a shared spool is risky."
- WRONG example: a heading "## The real problem" over a section that names the leaked lock.

### verdict (PRF2002)

- Catches: a judgment word standing in for the behavior it judges ("was broken", "a mess")
- CORRECT: a judgment word standing in for the behavior it judges: "was broken", "a mess", "terrible", "wrong" about a design or a decision, with no facts.
- WRONG: the word names a literal state (a broken test with a stated error, the wrong checkout, a broken link), or it quotes a title or an issue.
- Known false-positive shapes: "broken" in a quoted issue title; "wrong" as a plain adjective (the wrong branch).
- CORRECT example: "The old spool writer was a mess."
- WRONG example: "The spool test was broken: it compared map keys in iteration order." It names the defect.

### why-opener (PRF8011)

- Catches: a review reply sentence that opens "Why did you" or "Why would you"
- CORRECT: any sentence of a review reply opening "Why did you", "Why do you" or "Why would you", or a "Why" question with "don't you": the question is about the author rather than the code, whatever its tone or intent. A question asking why the author says or thinks something counts, and so does a suggestion phrased with "don't you".
- WRONG: "why you" is not the opener ("That is why you"), or the phrase sits in a quote.
- Known false-positive shapes: "That is why" and "which is why" mid-sentence; a quoted earlier comment.
- CORRECT example: "Why did you drop the spool check?"
- WRONG example: "That is why you get a cold spool on the first run."

### wordy (PRF4002)

- Catches: a phrase with a shorter equivalent ("in order to")
- CORRECT: a phrase with a shorter equivalent that says the same: "in order to" (to), "make sure that" (check that), "due to" before "the fact" (because).
- WRONG: the long form is part of a quote, a code span or a fixed term; or the words only look like the phrase because "in order" is a sequence word followed by "to" as a preposition.
- Known false-positive shapes: "in order" meaning sequence; quoted text (scope). Do not label WRONG because the shorter form would read slightly differently.
- CORRECT example: "We keep the spool in order to skip a rescan."
- WRONG example: "Keep the spool rows in order to the end of the file." Here "in order" is a sequence and "to" a preposition.

## Rules no longer in the catalog

Every rule labeled in rounds 1 to 4 is still in the catalog.

## Reminders

- Plain facts first: read the match, then the sentence, then the context; decide; write a short note when WRONG.
- If two causes apply, use the first that fits in this order: scope, sense, parse, register, meaning.
- Do not change `id`. Keep `label` to one of `CORRECT`, `WRONG`, `UNSURE`.
