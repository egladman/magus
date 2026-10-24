# proofread

proofread judges text by what the text is for, its kind. To add a rule or a kind, or
to see how a rule earns its default through calibration, read
[Changing proofread](#changing-proofread).

| Kind                 | Text                                                                                  |
| -------------------- | ------------------------------------------------------------------------------------- |
| `doc-comment`        | one symbol from a SCIP index: its doc comment, and the name of a function or method   |
| `reference`          | a hand-written Markdown page a reader looks things up in                              |
| `principle`          | a design-principle page, one rule argued in its author's voice; tense does not apply  |
| `guide`              | a procedural page, held to the guide rules as well                                    |
| `agent-brief`        | Markdown an agent loads as written, such as AGENTS.md                                 |
| `agent-compact`      | grammar-free text only an agent reads, such as a skill's short form                   |
| `skill-template`     | a `text/template` body that renders a short and a full form, as `internal/agent` does |
| `change-description` | a pull request: its title on the first line, its description after it                 |
| `review-reply`       | a review comment, a review's body or a reply in a thread                              |
| `stylesheet`         | the comments of a CSS file, each read with the rule or declaration it sits on         |

The magus module never imports `libs/proofread`, which requires only
`libs/diagnostics` and `golang.org/x/tools`. `cmd/proofread` runs it, and its first
argument names a kind: `proofread doc-comment --stdin` reads the symbols of
`magus\symbols()` as JSON on stdin, fed by
`hack/lint/symbol-docs-follow-proofread-rules.buzz`, and a finding points at the
declaration because an index records no position inside a doc. The page and
agent-instruction kinds judge the files the arguments name;
`change-description` and `review-reply` read the text on stdin under `--stdin`, and
`stylesheet` judges the files it names or, under `--stdin`, one on stdin. A finding from
text names its file, or the kind it read from stdin, and its line. A Go symbol
and a TypeScript one meet the same rules. For a `skill-template`,
what the short form shows meets the agent-compact rules, and what only the
full form shows meets the reference rules and `bare-rule`.

## Install

Each magus release ships `proofread_<version>_<os>_<arch>_static.tar.gz` beside
the magus archives, under the same signed `SHA256SUMS`, so `proofread` runs where
there is neither Go nor a magus checkout. This module's `go.mod` keeps no `replace`
directive, so `go install` also builds it from a `libs/proofread/v*` tag. The
[verify guide](https://eli.gladman.cc/magus/guides/setup/verify/) covers the
download and both checks.

## Decisions

Proofread speaks the decisions of a magus guard rule. Each rule ships a default
on each kind it judges, `deny` or `advise`, and a decisions table sets any rule
`off`, `advise` or `deny`, the way `magus\guard.builtins` overrides a built-in
guard rule. A caller refuses on `deny` and never sees `off`. `advise` means
graded: a graded rule has words with senses it cannot tell apart from the one it
means, so its finding fails nothing alone and lowers the text's grade by its
rule's weight (see Findings). `lead-context` marks a lead that opens on a defect
`advise` itself. A table that names a rule sets every one of its findings,
those included.

House style ships `off`: the rules that encode one repository's conventions
rather than plain writing (its glossary, plain-ASCII typography, the
present tense with no author, credit to tools, the doc-comment budgets, the
agent-instruction budgets). With no table proofread holds text to shape, tone,
claims and the generated-writing tells alone; a repository turns its house style
on in its table.

`--decisions <file>` reads the table, or stdin for `-` when the text itself is
not on stdin:

```json
{
  "rules": {"terms": "deny", "verdict": "deny", "staccato": "off"},
  "paths": {"docs/blog/**": {"tense": "off"}},
  "minimums": {"reference": 80, "commit-message": 20}
}
```

`minimums` holds the grade, 0 to 100, each kind is held to. proofread ships none:
a run over a kind the table names no minimum for exits 2 and names the kind, unless
`--minimum N` sets it for that run. Passing both for one kind is an error too.
`rules` applies to everything judged. `paths` applies to the file arguments a
glob matches, each glob in the order the file lists them, so the last match
wins over `rules` and over an earlier glob. A glob matches the argument as
given, slash-separated with any leading `./` dropped, so a caller passing
workspace-relative paths writes workspace-relative globs; `**` matches any
number of directories. An unknown rule, a decision other than the three, a glob
matching no file argument, and an unknown key are errors that exit 2 and name
it. `--only` takes comma-separated rule names and judges by those alone; an
unknown name, or one the table leaves off for every file, exits 2.
`--thread-length N` tells `long-thread` how many replies the author already
posted in the thread, and `--anchored` that the reply sits on a line of the
diff, an inline comment or a reply in its thread: that line is what it refers
to, so `nonspecific` judges only top-level comments. The flags follow the kind. `proofread rules` writes every
rule as its reference page shows it, `{name, code, kinds, decisions, house,
weight, dimension, catches, why}`, and judges nothing; `proofread explain <rule or code>` prints one
rule's catches, reason, default decisions and page as text. A first argument
that names no subcommand prints the usage and exits 2.

## Findings

The findings JSON is the contract: any command that writes the same array, a
team's own style checker or a reviewer it drives, feeds the same consumers, and
the same decisions table applies to it. proofread writes one JSON array on
stdout and exits 1 when a `deny` finding remains after suppression and the
baseline, 0 otherwise, and 2 when proofread could not judge (a bad flag, a missing
file, a malformed table, no minimum for the kind), so a caller tells a finding from
a failure.

Each text also gets a grade out of 100: 100 minus `100 * sum(weight) / (1 + min(words,
1000) / 100)`, floored at 0, over its prose words and its advise findings. A
finding's `weight` is its rule's Wilson 95% lower bound on precision in the latest
labeling round that scored 30 or more of its firings, and 0 for a rule no round
measured that well. A text past 1000 words grades as one of 1000, so padding buys
no headroom. A grade under the kind's minimum is a `grade` finding (PRF1100),
decided `deny`, whose message names the grade, the minimum and the three rules that
cost the most.

| Field      | Holds                                                                               |
| ---------- | ----------------------------------------------------------------------------------- |
| `node`     | what was judged: the symbol's node, the file argument, or the kind read from stdin  |
| `source`   | where: the symbol's index position, `path:line`, or the path alone for a whole text |
| `kind`     | the kind judged, one of the seven above                                             |
| `rule`     | the rule's name                                                                     |
| `code`     | the rule's `PRF` code, from its own domain in `libs/diagnostics`                    |
| `decision` | `advise` or `deny`; `off` never appears                                             |
| `message`  | the fix, with an example where the rule has one                                     |
| `match`    | the offending text, or `""` for a budget                                            |
| `url`      | the rule's page, `https://eli.gladman.cc/magus/reference/proofread/<rule>/`         |
| `weight`   | what the finding costs its text's grade: its rule's weight on `advise`, 0 on `deny` |

Codes are numbered by family and never reused: `PRF1xxx` shape, `PRF2xxx` tone,
`PRF3xxx` claims and hedges, `PRF4xxx` generated-writing tells, `PRF5xxx` house
style, `PRF6xxx` doc comments, `PRF7xxx` agent instructions, `PRF8xxx` review
replies.

## Output, baselines and suppressions

`-o` picks what proofread writes: `json` (the default) is the array above,
`sarif` is a SARIF 2.1.0 log for code scanning, and `rdjson` is reviewdog's
diagnostic result. The SARIF log lists every rule in the driver, gives a deny
finding the level `error` and an advise finding `warning`, and carries a
`proofread/v1` partial fingerprint hashed from the rule, the match and the path,
so a finding that moves down a file is still the same finding. An rdjson
diagnostic carries a suggestion for each replacement a rule offers. `text` writes
one line per finding for a terminal or a hook's output,
`path:line:col: CODE rule [decision] message`, leaving out the column when it is
not known and the line for a finding about the whole text, and when there are
findings it writes a count on stderr. Any other format exits 2.

```sh
proofread reference -o sarif docs/*.md > proofread.sarif
proofread reference -o rdjson docs/*.md | reviewdog -f=rdjson -reporter=github-pr-review
proofread reference -o text --minimum 80 docs/*.md
```

`proofread commit-message FILE` judges the message file a VCS hook passes. It drops
the lines that start with `#` and the scissors line with everything after it, and a
finding's line is its line in the file; with `--stdin` in place of the file it reads
the message from stdin. The [proofread guide](https://eli.gladman.cc/magus/guides/proofread/)
installs it as a git, Mercurial or Jujutsu check.

`--record` keeps what happened to a file's findings, on this machine and nowhere
else, under `$XDG_STATE_HOME/proofread/` (`~/.local/state/proofread/` when the
variable is unset). Per file and rule it stores the findings' fingerprints, the
hash of the rule, the match and the path that SARIF uses, and no text. On the next
`--record` run of the same file a fingerprint that disappeared is `fixed`, one the
file now suppresses is `suppressed`, and one still reported is `reported`.
`proofread stats [-o json|text]` prints each rule's counts and its
`not_useful_rate`, one minus precision over those counts: (suppressed + reported) /
(suppressed + reported + fixed). It flags a rule at or over 10% `probation`
(precision 90% or less) and at or over 25% `ship-off` (75% or less). A run
judges again without the file's suppressions to tell `suppressed` from `fixed`, and
a rule the run did not judge (off, `--only`, another kind) keeps its state.

`--baseline FILE` adopts proofread on text that already has findings. The file
holds a count per file and rule, `{"version":1,"counts":{"docs/a.md":{"filler":2}}}`.
A run tolerates up to each count and writes the findings past it: for a file and
rule that is the last ones in the order proofread lists them. A count is a
ceiling, so a file that improved reports nothing until `--prune` lowers it.
`--baseline FILE --prune` rewrites the file to this run's counts and writes no
findings, and creates it when it is missing; without `--prune` a missing file is an
error. Commit the file, and run `--prune` as the findings are fixed.

A suppression silences a rule at one place and gives its reason. In Markdown it is
a comment before the line, `<!-- proofread off filler: quoted from the user -->`,
and in a doc comment or plain text a line saying `proofread:ignore filler quoted
from the user`. Each covers its own line and the next, and names one rule or
several separated by commas. A block runs from an `off` comment to a later
`<!-- proofread on -->`, which ends every open block, or to
`<!-- proofread on filler -->`, which ends that rule's. A finding with no line,
such as `comment-block`, is covered by a suppression of its rule anywhere in the
text. A suppression with no reason covers nothing, and one that matched no
finding is stale; `suppression-unused` (PRF1090) reports both, at the
suppression's own line. A suppression inside a fenced block or a backtick span is
text, so a page can show the syntax.

## Voice files

A voice file records one author's measured style per kind: change descriptions,
review replies, commit messages and issues. `proofread voice build --kind KIND
FILE...|--stdin` measures the author's own texts, one per file, NUL-separated on
stdin, or as JSON lines with `--field` (and `--kind-field` for a kind per line),
and writes the file to `--out`, by default `$XDG_CONFIG_HOME/proofread/voice.json`
(under the OS config directory when unset, as magus resolves its own). It refuses that default inside a
git work tree. The file holds aggregates alone, per kind: the texts and words
counted, the first-person and future-tense rates, and the p10, p50 and p90 of
each feature: words, words per sentence, the longest paragraph's words, colons,
parentheses and dashes per 100 words, code spans, contractions, headings and
bold, the marker words its, every, one, with, all, and, the, and a commit
subject's characters. It holds no text, no content word and no path. It never
leaves the machine and is never committed. Spelling, casing and semicolons are
not measured.

`--voice FILE` on any kind runs `voice-drift` (PRF1040), an advise-only rule
that reports a text once when two or more features measure outside the author's
p10 to p90 range, naming each feature and its direction. A kind under 30 texts
sets no range. A voice whose change descriptions use "I" at 10 or more per 1000
words and "will" at 2 or more holds `tense` to advice on change descriptions,
where a decisions table would deny it. The [proofread guide](https://eli.gladman.cc/magus/guides/proofread/) walks through building one.

## Rules

| Rule                 | Code    | Kinds                            | Default                              | Reports                                                                          |
| -------------------- | ------- | -------------------------------- | ------------------------------------ | -------------------------------------------------------------------------------- |
| `comment-block`      | PRF6001 | doc comment, stylesheet          | off (house)                          | a doc over 250 words                                                             |
| `comment-sentence`   | PRF6002 | doc comment, stylesheet          | off (house)                          | a doc sentence over 60 words                                                     |
| `filler`             | PRF4001 | all                              | deny                                 | throat-clearing ("Note that") and filler adverbs ("simply")                      |
| `terms`              | PRF5001 | all                              | off (house)                          | a spelling the glossary replaces ("sub-agent")                                   |
| `aside`              | PRF6004 | doc comment, stylesheet, help    | off (house)                          | a spaced hyphen spelling an em-dash, inline or ending a line                     |
| `history`            | PRF6005 | doc comment, stylesheet          | off (house)                          | a phrase narrating the change rather than the code ("used to")                   |
| `docstub`            | PRF6006 | doc comment                      | off (house)                          | a one-line doc that only repeats the symbol's name                               |
| `restates`           | PRF6007 | stylesheet                       | off (house)                          | a one-line comment that only names the code it sits on                           |
| `comment-budget`     | PRF6008 | stylesheet                       | off (house)                          | a comment over the lines its place earns                                         |
| `block-budget`       | PRF6009 | stylesheet                       | off (house)                          | a rule block whose comments, summed, outrun its size                             |
| `banned-word`        | PRF5006 | stylesheet                       | off (house)                          | a word this repository bans                                                      |
| `lead-context`       | PRF1001 | change description               | deny; advise for a defect            | a lead that is not a paragraph stating what the change set out to do and why     |
| `reply-voice`        | PRF1002 | pages, change description, reply | deny                                 | a reply opener, a conversation, a bold-label item, a stock label or heading      |
| `tense`              | PRF5004 | pages, change description        | off (house)                          | the future tense, and the author as the actor of a change                        |
| `hedge`              | PRF3002 | pages, change description        | deny                                 | a softener on a claim ("might fix", "could potentially")                         |
| `attribution`        | PRF5005 | pages, change description, reply | off (house)                          | credit to a tool, or an account of how the work was made                         |
| `terse-sentence`     | PRF7001 | agent instructions               | off (house)                          | a sentence over 25 words                                                         |
| `terse-paragraph`    | PRF7002 | agent instructions               | off (house)                          | a paragraph or list item over 60 words                                           |
| `wordy`              | PRF4002 | pages, change description        | deny                                 | a phrase with a shorter equivalent ("in order to")                               |
| `bare-rule`          | PRF7003 | agent instructions               | off (house)                          | "rule" with no mechanism named, in either form of a template                     |
| `second-person`      | PRF1003 | guide                            | deny                                 | we, us, our or ours where a guide addresses you                                  |
| `step-verb`          | PRF1004 | guide                            | deny                                 | a numbered step that opens with no verb ("1. The target...")                     |
| `condescension`      | PRF2006 | pages, change description, reply | deny                                 | in a guide, a step called easy; elsewhere a word that presumes ("of course")     |
| `suppression-unused` | PRF1090 | every kind                       | deny                                 | a suppression with no reason, or one that matched no finding                     |
| `blame`              | PRF2001 | change description, reply        | deny                                 | a person or a pull request as the subject of a fault; contempt ("sloppy")        |
| `verdict`            | PRF2002 | change description, reply        | advise                               | a judgment in place of the behavior ("was broken", "a mess")                     |
| `absolute`           | PRF2003 | change description, reply        | advise                               | never, nobody or nothing about the past ("has never fired")                      |
| `intent`             | PRF2004 | change description, reply        | advise                               | a motive given to a tool or a person ("guessed", "pretends")                     |
| `credit`             | PRF2005 | change description               | advise                               | a removal or replacement that says nothing of what the old design was for        |
| `claim`              | PRF3001 | change description, reply        | advise                               | a measurement, comparison or completion with no evidence in its sentence or item |
| `reply-opener`       | PRF8001 | reply                            | deny                                 | a sentence that opens by contradicting ("No,", "As I said")                      |
| `judgment-as-fact`   | PRF8002 | reply                            | advise                               | a recommendation with no reason ("This should be a map.")                        |
| `stacked-hedge`      | PRF8003 | reply                            | advise                               | two softeners in a sentence, or an apology before the point                      |
| `long-thread`        | PRF8004 | reply                            | advise                               | the author's fourth or later reply in a thread, given `--thread-length`          |
| `signpost`           | PRF4003 | pages, change description, reply | deny                                 | an announcement where the point should be ("Here's the thing")                   |
| `chatbot`            | PRF4004 | pages, change description, reply | deny                                 | text addressed to a chat's user ("I hope this helps")                            |
| `leak`               | PRF4005 | all                              | deny                                 | a citation marker or an unfilled placeholder                                     |
| `buzzword`           | PRF4006 | pages, change description, reply | deny                                 | a word chosen to sound significant ("delve", "tapestry")                         |
| `buzzword-weak`      | PRF4007 | pages, change description, reply | advise                               | a buzzword that also has a plain sense ("crucial")                               |
| `contrast`           | PRF4010 | pages, change description        | deny in a change description; advise | a claim made by denying its opposite ("not just X, it is Y")                     |
| `vague`              | PRF4008 | pages, change description, reply | deny                                 | weight or consensus with nothing named ("experts argue")                         |
| `closer`             | PRF4009 | pages, change description, reply | deny                                 | a sentence announcing it restates the text ("In conclusion,")                    |
| `ing-tail`           | PRF4011 | pages, change description        | advise                               | a participle clause claiming significance (", highlighting")                     |
| `staccato`           | PRF4012 | reference, change description    | deny in a change description; advise | three or more sentences of six words or fewer in a row                           |
| `dash`               | PRF5002 | pages, change description, reply | off (house)                          | an em dash, an en dash or a spaced double hyphen                                 |
| `ascii`              | PRF5003 | pages, change description, reply | off (house)                          | a curly quote, an ellipsis character or an emoji                                 |
| `heading-case`       | PRF4013 | reference, guide, agent brief    | advise                               | a heading whose every word after the first is capitalized                        |
| `template`           | PRF7004 | skill template                   | off (house)                          | a body that does not render, so neither form can be judged                       |

"Pages" are reference pages, guides and agent instructions. A guide and agent
instructions take every reference and change-description rule but
`lead-context`. A reply takes `filler`, `terms`, `reply-voice` without its openers and
conversations, `attribution`'s credit to a tool, `condescension`, the tone rules
and its own four; it speaks in the first person, so `tense` and `hedge` do not
run on it. `blame`, `verdict`, `absolute`, `intent` and `claim` judge only text
written to reviewers: a page names its reader's possible mistakes and states
contracts ("never returns nil"). `hedge` and `claim` leave alone a sentence that
states a limit: one under a heading such as "Not verified" or "Limits", or one
opening with "Not measured", "Not tested", "Not verified" or "Untested". A claim's
evidence is a code span, a link, an issue or pull request, a commit, or a magus
output ref. A change description may carry headings past its lead, such as
"What changes" or "Not verified", but not a stock label such as "Summary".
A numbered list is a procedure, and `step-verb` judges it, only when one of its
items opens with an imperative; a recap, a precedence order or a list of reasons
is left alone. One word gives one finding: `condescension` leaves a word to
`filler`, `second-person` leaves a "we" to `tense`, and `reply-voice` leaves a
description's lead to `lead-context`, each only where the other rule runs.
Pages and change descriptions take a wider `filler` list ("actually", "robust")
than doc comments do. docs/conventions.md states the written rules for authors.

Code in a doc, fenced or indented, is never judged. A backtick span still counts
toward the budgets, but no wording rule reads inside one.
A doc with a line opening in TODO, FIXME, BUG, `compat(until:`, `compat:` or
`Deprecated:` is exempt from `history` and `docstub`.

## CSS comments

The `stylesheet` kind reads the `/* */` comments out of a stylesheet and
attributes each to the code it speaks of: the rule header or declaration below
it, or the declaration it follows on the same line. It then runs the doc rules,
some of the written ones and the rules above marked stylesheet on each, so a
comment meets the rules a Go comment and a Markdown page meet. A finding names
its line and no column, since the comment is judged with its delimiters gone. `history` also reads "no longer",
"the old", "W2" and the like, which a stylesheet's comments use to narrate what
a rule replaced. A comment a tool reads (`stylelint-`, `biome-`, `prettier-ignore`,
`!`, `# sourceMappingURL`) and a banner of rule lines are not judged.

A comment's budget is set by where it sits, carrying over the Go comment
budget in `hack/policy/comments.buzz`:

| Place               | Lines |
| ------------------- | ----- |
| heading a file      | 8     |
| above a rule        | 4     |
| above a declaration | 3     |
| between rules       | 3     |
| inside a block      | 3     |
| after a declaration | 2     |

A block earns 6 comment lines and one more for every 5 statements directly
inside it; the comment above its header and each comment inside it sum against
that. `restates` judges a one-line comment against the header, and against the
declarations of a block of four or fewer. The console's lint runs the judge over
`console/src/**/*.css` and fails on any finding.

## Changing proofread

This part covers adding a rule or a kind of text, and deciding what a rule ships as.
The [proofread guide](../../docs/guides/proofread.md) covers using it.

### Calibration, not training

No model is trained here. Every rule is hand-written Go that a reader can audit, and
nothing at check time learns or loads weights. What the labeled data does is
calibrate: it measures how often a hand-written rule is right on text nobody tuned it
on, and that measurement decides the rule's default decision (`off`, `advise` or
`deny`). Fixing a rule is ordinary code review. Promoting one takes evidence from a
frozen labeling round. Both the rounds and the word-class tagger's training run from
`proofread-train`, a separate binary in the `train/` module, so neither ships in
`proofread` or reaches `libs/conventions`.

### Where things live

| Path                                    | Holds                                                                                                   |
| --------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| `proofread.go`                          | the `Rule`, `Kind` and `Decision` types, the check table and the core rules                             |
| one file per family                     | each family's rule constants, checks, texts and judges: `tone.go`, `slop.go`, `review.go`, `density.go` |
| `catalog.go`                            | the PRF code families, the dimensions and the merge of every family's texts                             |
| `text.go`                               | the readers: Markdown and plain text into prose lines, and the platform text blanked before any rule    |
| `evidence.go`                           | `evidenceCeiling`: the strictest decision each rule's labeled evidence supports                         |
| `internal/calibrate/`                   | the case loader, the precision tally, Wilson, Krippendorff and the shape separation                     |
| `internal/calibrate/cases/<rule>.txtar` | the labeled cases: regression tests and the tuning set                                                  |
| `internal/wordclass/`                   | the word-class tagger the grammar measures read, and its model trained by `proofread-train train`       |
| `train/`                                | the `proofread-train` module: the tagger's trainer, the case replay and the labeling rounds             |
| `testdata/guideline.md`                 | the labeling guideline every round is frozen with                                                       |
| `testdata/rounds/roundN.json`           | one record per labeling round: counts, scores and gates, no text                                        |

### Add a rule

1. Pick its family file, or start one beside `help.go` if no family fits. Declare the
   `Rule` constant there, its `check` (the kinds it judges, any kind it only advises
   on, and `house` if it encodes one repository's conventions), and its `ruleText`:
   the next free PRF code in its family (`catalog.go` lists them), a dimension, a
   one-line `catches` and a `why` that says what the rule protects and what measured
   it.
2. Write the judge. Read the prose the reader hands you; never re-parse Markdown.
   Leave quoted words, code and platform text alone: they are not the author's.
3. Add table tests beside the judge, both directions: what fires and what must not.
4. Add `internal/calibrate/cases/<rule>.txtar` with at least one `hit/` and one `pass/` section
   (`TestLabeledCasesAgreeWithTheRules` requires both). Write the texts yourself.
5. Add the rule to `evidenceCeiling` at `DecisionOff`, unless it is a house rule,
   which ships off anyway. No rule ships on before a frozen round measures it.
6. Run `magus run test libs/proofread`, then `magus affected generate:rw` for the
   rule's reference page, and add a changelog fragment.

### Add a kind

1. Declare the `Kind` in its family file, or in `proofread.go` beside the others.
2. Route it in `judgeText` (`text.go`) to a reader, an existing one if the text is
   Markdown or plain text, a new one if it is not, as `judgeStylesheet` reads CSS.
3. Decide rule by rule which rules judge it, by adding the kind to each check's kind
   list. A rule written for running prose usually does not suit fragments.
4. Add it to `everyKind` (`suppress.go`) and, if the text is read from files, to the
   CLI's subcommand table in `cmd/proofread/main.go`.
5. Add its row to the kind table at the top of this page.

### Earn a default

A rule's default moves only on a frozen labeling round, run with
`proofread-train calibrate round`; [the round guide](../../docs/guides/proofread-calibration.md)
walks through it step by step. In short:

| Step       | What happens                                                                                                |
| ---------- | ----------------------------------------------------------------------------------------------------------- |
| sample     | seeded, deterministic firings from text no earlier round or committed case holds, at least 10 per source    |
| freeze     | the rule commit, seed, gates, guideline digest, binary sha256 and every source's sha256 are recorded first  |
| label      | two labelers, alone, a note on every label                                                                  |
| adjudicate | someone who never tuned the rule settles each disagreement                                                  |
| score      | precision, Wilson lower bound, alpha and the per-source gate; a rule file changed since the freeze voids it |
| pool       | a deny weighs two frozen rounds on different sources                                                        |

A round that labels 30 or more of a rule's firings gives it a weight, the Wilson 95%
lower bound on its precision, recorded in `evidence.go`'s `weights`; until then a rule
that ships off stays in `unmeasured`. The gates: advise needs a Wilson 95% lower bound of 0.70 over 30 or more firings, on
the worst source with 10 or more, with at most one disagreement per 10 firings (or
alpha 0.80) and a note on every one. A rule's repeat attempt is frozen with z = 2.24,
so trying again cannot pass by luck. Deny needs two rounds on different sources,
pooled to 60 firings with at most 1 wrong, both passing agreement. A rule that passes
gets 10 of its agreed-correct labels spot-checked by a person before its
default moves.

### Tune without fooling yourself

- The committed cases are the tuning set. A round's texts are spent the moment
  anyone tunes on them, so a fix read off a round is measured by the next one.
- Never read the held-out fifth of the cases (`Case.HeldOut`: the fnv64a hash of the
  text, modulo 5). It is a smoke test, not evidence.
- Sealed sets live outside git, under
  `$XDG_CACHE_HOME/proofread/calibration/sealed/`, and the guard refuses a shell line
  that reads them.
- A case a fix stops firing on moves from `fp/` to `pass/` with
  `was=fp:<round>:<id>` on its section header; a lint refuses the move without it.
- Never delete or relabel a labeled case to make a number move.

### History

Rounds 1 to 3 calibrated the first ceilings; rounds 4 to 7 tested fix rounds. Each
round's record is in `testdata/rounds/`, and `evidenceCeiling` cites the round behind
every cap.
