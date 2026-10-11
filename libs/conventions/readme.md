# conventions

Go analyzers for this repository's own rules over Go source. Each one was a test
in the root `conventions_test.go` that walked the tree as text; as an analyzer it
runs in `magus run lint` beside the stock linters, reports at the offending line,
and takes a `//nolint:<name> // <reason>` where an exception is deliberate.

| Linter          | Reports                                                                                                     |
| --------------- | ----------------------------------------------------------------------------------------------------------- |
| `hostagnostic`  | a line of non-test source naming an agent host outside a filesystem path                                    |
| `hostvocab`     | a host's tool name (`"Read"`, `"Bash"`) as a string literal in guard code                                   |
| `ruletext`      | guard rule text (`"magus workspace:"`) in the guard's CLI half                                              |
| `asciistrings`  | a typographic glyph in a string literal of a listed user-facing file                                        |
| `importceiling` | a package importing more packages under a prefix than its ratchet allows                                    |
| `stutter`       | an exported package-level name opening with its package's name                                              |
| `filenames`     | a Go file name segment that splits into two segments the tree uses as file names                            |
| `nameoutput`    | a `case outputName:` arm that does not render through an emitter                                            |
| `testisolation` | a test binary linking the runtime-directory package with no isolating `TestMain`                            |
| `globalrestore` | a test assigning a configured package-level variable with no `t.Cleanup` or `defer` restoring it            |
| `fieldwise`     | a test asserting every field of a struct one at a time instead of comparing the whole value once            |
| `providerio`    | Go source outside an allowlist reaching toward a CI/VCS provider (an HTTP client, or a provider SDK import) |
| `diagmsg`       | a message magus prints that runs long, stacks reasons, names two commands or opens with a tag               |
| `errmsg`        | an error text joining clauses, spanning lines or sentences, misplacing `%w`, or naming the wrong origin     |
| `stderrprint`   | a write to `os.Stderr` outside a usage function and the display, which the verbosity flags never see        |

Every path, word list, host name, ceiling and exemption lives in the root
`.golangci.yml`, so the analyzers carry the mechanism and the config carries the
policy. No diagnostic names this repository's identifiers or paths on its own: an
analyzer whose remedy is repository-specific takes a `hint` setting, appended to
each of its diagnostics.

## Scope

golangci-lint stops at a `go.mod`, so the root `lint` target runs it once more in
each module under `libs/`, on the same config, with only `hostagnostic`,
`stutter` and `filenames` enabled: the rules the old tree walks held everywhere.
The rest name root-module paths.

`filenames` reads beyond its package on purpose: its vocabulary is every Go file
name the go tool would read under the root of `module`, nested modules included,
walked once when the plugin loads. Like the go tool, the walk never enters a dot
or underscore directory or `testdata`. Rules over source text also read the files
a package's build constraints exclude on this platform, so a darwin run still
checks the `_linux.go` files the walks did.

A setting that names a path (`files`, `package`, `dirs`, `skip-dirs`) is checked
against the tree when the plugin loads, from the directory whose `go.mod`
declares `module`. A pattern matching no file, a package or directory with no Go
files, or a skip entry naming no directory that holds Go files, is a load error
naming the setting: a scope that matches nothing would otherwise report nothing,
and a skip entry that matches nothing is a setting the tree moved out from under.

`stutter` reads the package name from the package clause, which is what a call
site spells, and skips `package main`, which has none. Its zero settings check
every package name of three bytes or more.

`testisolation` carries reach as a package fact along imports, which needs type
information; the rest load types only because golangci-lint leaves the package
unset without them.

`globalrestore` reads test files only. It reports the first assignment in a test
function, a subtest closure or a helper taking a `*testing.T` to a configured
variable, or to a field or element of one, unless that function (or an enclosing
test) hands `t.Cleanup` or `defer` a closure assigning the variable or a package
function that does, such as `t.Cleanup(snapshotGlobals())`. A configured `vars`
name its package does not declare is a load error.

`fieldwise` reads test files only. Its zero settings report a value whose
assertions name every field its struct declares, which one comparison of the
whole value replaces exactly. `report-partial` adds a value asserted on two or
more of its fields: that fix compares fields the test never looked at, so it may
need volatile fields normalized first. This tree turns it on: once a struct grows
a field, a test that named every field asserts only some, and only
`report-partial` still reports it.

`errmsg` judges a plain Go error, which its callers extend with ": " as it
wraps: `error-join` reports `"; "`, `" - "` or an em dash, `error-newline` a newline,
`error-sentences` a period followed by more text outside quotes and
abbreviations, and `error-wrap` a `%w` anywhere but an opening `"%w: "` or a closing `": %w"`. A
`fmt.Errorf` built only to wrap inside a coded diagnostic is still judged; the
diagnostic's own constructor is `diagmsg`'s. Capitalization and trailing
punctuation are staticcheck's ST1005.

The error that originates a failure names its origin once, and a wrap adds what
its own call was doing. `error-origin` reports a leading `"name: "` naming another
package of the module, read from every package clause under `module`;
`operations` lists the names that are also commands (`run`). `error-stutter`
reports a wrap opening with its own package's `"name: "` around a variable last
assigned from a call into that package, which already named it.
`error-notice` reports an error built into the message of a log record that
carries one of `notice-attrs`: the record names who is speaking, the error rides
as an attribute, and the display names a shared origin once.

## Not here

Rules whose subject is not one package's Go source stayed tests: a vocabulary or
symbol index built from the whole tree (comments naming symbols that exist), and
rules that also read Buzz, shell or markdown
(environment sniffing, registered env vars, diagnostic raise sites, rescanning
string loops).

## proofread

`proofread` is not an analyzer, and golangci-lint never loads it. It judges text by
what the text is for, its kind:

| Kind                          | Text                                                                                  |
| ----------------------------- | ------------------------------------------------------------------------------------- |
| `doc-comment`                 | one symbol from a SCIP index: its doc comment, and the name of a function or method   |
| `reference`                   | a hand-written Markdown page a reader looks things up in                              |
| `guide`                       | a procedural page, held to the guide rules as well                                    |
| `agent-instructions`          | Markdown an agent loads as written, such as a SKILL.md                                |
| `agent-instructions-template` | a `text/template` body that renders a short and a full form, as `internal/agent` does |
| `change-description`          | a pull request: its title on the first line, its description after it                 |
| `review-reply`                | a review comment, a review's body or a reply in a thread                              |

The magus module never imports `libs/conventions`, and `proofread` imports only the
standard library and `libs/diagnostics`. `cmd/proofread` runs it, and its first
argument names a kind: `proofread doc-comment` reads the symbols of
`magus\symbols()` as JSON on stdin, fed by
`hack/lint/symbol-docs-follow-proofread-rules.buzz`, and a finding points at the
declaration because an index records no position inside a doc. The page and
agent-instruction kinds judge the files the arguments name;
`change-description` and `review-reply` read the text on stdin. A finding from
text names its file, or the kind it read from stdin, and its line. A Go symbol
and a TypeScript one meet the same rules. For an `agent-instructions-template`,
what the short form shows meets the agent-instruction rules, and what only the
full form shows meets the reference rules and `bare-rule`.

### Install

Each magus release ships `proofread_<version>_<os>_<arch>_static.tar.gz` beside
the magus archives, under the same signed `SHA256SUMS`, so `proofread` runs where
there is neither Go nor a magus checkout. `go install` cannot fetch it, because
this module's `go.mod` keeps a `replace` directive. The
[verify guide](https://eli.gladman.cc/magus/guides/setup/verify/) covers the
download and both checks.

### Decisions

Proofread speaks the decisions of a magus guard rule. Each rule ships a default
on each kind it judges, `deny` or `advise`, and a decisions table sets any rule
`off`, `advise` or `deny`, the way `magus\guard.builtins` overrides a built-in
guard rule. A caller refuses on `deny`, reports `advise` and lets it through, and
never sees `off`. A rule marked `advise` has words with senses it cannot tell
apart from the one it means; `lead-context` marks a lead that opens on a defect
`advise` itself. A table that names a rule sets every one of its findings,
those included.

House style ships `off`: the rules that encode one repository's conventions
rather than writing a teammate reads (its glossary, plain-ASCII typography, the
present tense with no author, credit to tools, the doc-comment budgets, the
agent-instruction budgets). With no table proofread holds text to shape, tone,
claims and the generated-writing tells alone; a repository turns its house style
on in its table.

`-decisions <file>` reads the table, or stdin for `-` when the text itself is
not on stdin:

```json
{
  "rules": {"terms": "deny", "verdict": "deny", "staccato": "off"},
  "paths": {"docs/blog/**": {"tense": "off"}}
}
```

`rules` applies to everything judged. `paths` applies to the file arguments a
glob matches, each glob in the order the file lists them, so the last match
wins over `rules` and over an earlier glob. A glob matches the argument as
given, slash-separated with any leading `./` dropped, so a caller passing
workspace-relative paths writes workspace-relative globs; `**` matches any
number of directories. An unknown rule, a decision other than the three, a glob
matching no file argument, and an unknown key are errors that exit 2 and name
it. `-only` takes comma-separated rule names and judges by those alone; an
unknown name, or one the table leaves off for every file, exits 2.
`-thread-length N` tells `long-thread` how many replies the author already
posted in the thread. The flags follow the kind. `proofread rules` writes every
rule as its reference page shows it, `{name, code, kinds, decisions, house,
catches, why}`, and judges nothing; `proofread explain <rule or code>` prints one
rule's catches, reason, default decisions and page as text. A first argument
that names no subcommand prints the usage and exits 2.

### Findings

The findings JSON is the contract: any command that writes the same array, a
team's own style checker or a reviewer it drives, feeds the same consumers, and
the same decisions table applies to it. proofread writes one JSON array on
stdout and, by default, exits 0 whatever it found; a finding is not a failure, and
the caller decides what one costs. `-fail-on deny|advise|never` makes the exit code
say it: 1 when a finding at or above that decision remains after suppression and
the baseline, 0 otherwise, and 2 when proofread could not judge (a bad flag, a
missing file, a malformed table), so a caller tells a finding from a failure. The
default is `never`.

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

Codes are numbered by family and never reused: `PRF1xxx` shape, `PRF2xxx` tone,
`PRF3xxx` claims and hedges, `PRF4xxx` generated-writing tells, `PRF5xxx` house
style, `PRF6xxx` doc comments, `PRF7xxx` agent instructions, `PRF8xxx` review
replies.

### Output, baselines and suppressions

`-format` picks what proofread writes: `json` (the default) is the array above,
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
proofread reference -format sarif docs/*.md > proofread.sarif
proofread reference -format rdjson docs/*.md | reviewdog -f=rdjson -reporter=github-pr-review
proofread reference -format text -fail-on deny docs/*.md
```

`proofread commit-message FILE` judges the message file a VCS hook passes. It drops
the lines that start with `#` and the scissors line with everything after it, and a
finding's line is its line in the file; with no argument it reads the message from
stdin. The [proofread guide](https://eli.gladman.cc/magus/guides/proofread/)
installs it as a git, Mercurial or Jujutsu check.

`-record` keeps what happened to a file's findings, on this machine and nowhere
else, under `$XDG_STATE_HOME/proofread/` (`~/.local/state/proofread/` when the
variable is unset). Per file and rule it stores the findings' fingerprints, the
hash of the rule, the match and the path that SARIF uses, and no text. On the next
`-record` run of the same file a fingerprint that disappeared is `fixed`, one the
file now suppresses is `suppressed`, and one still reported is `reported`.
`proofread stats [-format json|text]` prints each rule's counts and its
`not_useful_rate`, (suppressed + reported) / (suppressed + reported + fixed), and
flags a rule at or over 10% `probation` and at or over 25% `ship-off`. A run
judges again without the file's suppressions to tell `suppressed` from `fixed`, and
a rule the run did not judge (off, `-only`, another kind) keeps its state.

`-baseline FILE` adopts proofread on text that already has findings. The file
holds a count per file and rule, `{"version":1,"counts":{"docs/a.md":{"filler":2}}}`.
A run tolerates up to each count and writes the findings past it: for a file and
rule that is the last ones in the order proofread lists them. A count is a
ceiling, so a file that improved reports nothing until `-prune` lowers it.
`-baseline FILE -prune` rewrites the file to this run's counts and writes no
findings, and creates it when it is missing; without `-prune` a missing file is an
error. Commit the file, and run `-prune` as the findings are fixed.

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

### Voice files

A voice file records one author's measured style per kind: change descriptions,
review replies, commit messages and issues. `proofread voice build -kind KIND
[FILE...]` measures the author's own texts, one per file, NUL-separated on stdin,
or as JSON lines with `-field` (and `-kind-field` for a kind per line), and
writes the file to `-o`, by default `$XDG_CONFIG_HOME/proofread/voice.json`
(`~/.config/proofread/voice.json` when unset). It refuses that default inside a
git work tree. The file holds aggregates alone, per kind: the texts and words
counted, the first-person and future-tense rates, and the p10, p50 and p90 of
each feature: words, words per sentence, the longest paragraph's words, colons,
parentheses and dashes per 100 words, code spans, contractions, headings and
bold, the marker words its, every, one, with, all, and, the, and a commit
subject's characters. It holds no text, no content word and no path. It never
leaves the machine and is never committed. Spelling, casing and semicolons are
not measured.

`-voice FILE` on any kind runs `voice-drift` (PRF1040), an advise-only rule
that reports a text once when two or more features measure outside the author's
p10 to p90 range, naming each feature and its direction. A kind under 30 texts
sets no range. A voice whose change descriptions use "I" at 10 or more per 1000
words and "will" at 2 or more holds `tense` to advice on change descriptions,
where a decisions table would deny it. The [proofread guide](https://eli.gladman.cc/magus/guides/proofread/) walks through building one.

### Rules

| Rule                 | Code    | Kinds                                | Default                              | Reports                                                                          |
| -------------------- | ------- | ------------------------------------ | ------------------------------------ | -------------------------------------------------------------------------------- |
| `comment-block`      | PRF6001 | doc comment                          | off (house)                          | a doc over 250 words                                                             |
| `comment-sentence`   | PRF6002 | doc comment                          | off (house)                          | a doc sentence over 60 words                                                     |
| `filler`             | PRF4001 | all                                  | deny                                 | throat-clearing ("Note that") and filler adverbs ("simply")                      |
| `terms`              | PRF5001 | all                                  | off (house)                          | a spelling the glossary replaces ("sub-agent")                                   |
| `name-suffix`        | PRF6003 | doc comment                          | off (house)                          | a function or method name whose last word is Of or For                           |
| `aside`              | PRF6004 | doc comment                          | off (house)                          | a spaced hyphen spelling an em-dash, inline or ending a line                     |
| `history`            | PRF6005 | doc comment                          | off (house)                          | a phrase narrating the change rather than the code ("used to")                   |
| `docstub`            | PRF6006 | doc comment                          | off (house)                          | a one-line doc that only repeats the symbol's name                               |
| `lead-context`       | PRF1001 | change description                   | deny; advise for a defect            | a lead that is not a paragraph saying what a reader can now do                   |
| `reply-voice`        | PRF1002 | pages, change description, reply     | deny                                 | a reply opener, a conversation, a bold-label item, a stock label or heading      |
| `tense`              | PRF5004 | pages, change description            | off (house)                          | the future tense, and the author as the actor of a change                        |
| `hedge`              | PRF3002 | pages, change description            | deny                                 | a softener on a claim ("might fix", "could potentially")                         |
| `attribution`        | PRF5005 | pages, change description, reply     | off (house)                          | credit to a tool, or an account of how the work was made                         |
| `terse-sentence`     | PRF7001 | agent instructions                   | off (house)                          | a sentence over 25 words                                                         |
| `terse-paragraph`    | PRF7002 | agent instructions                   | off (house)                          | a paragraph or list item over 60 words                                           |
| `wordy`              | PRF4002 | pages, change description            | deny                                 | a phrase with a shorter equivalent ("in order to")                               |
| `bare-rule`          | PRF7003 | agent instructions                   | off (house)                          | "rule" with no mechanism named, in either form of a template                     |
| `second-person`      | PRF1003 | guide                                | deny                                 | we, us, our or ours where a guide addresses you                                  |
| `step-verb`          | PRF1004 | guide                                | deny                                 | a numbered step that opens with no verb ("1. The target...")                     |
| `condescension`      | PRF2006 | pages, change description, reply     | deny                                 | in a guide, a step called easy; elsewhere a word that presumes ("of course")     |
| `suppression-unused` | PRF1090 | every kind                           | deny                                 | a suppression with no reason, or one that matched no finding                     |
| `blame`              | PRF2001 | change description, reply            | deny                                 | a person or a pull request as the subject of a fault; contempt ("sloppy")        |
| `verdict`            | PRF2002 | change description, reply            | advise                               | a judgment in place of the behavior ("was broken", "a mess")                     |
| `absolute`           | PRF2003 | change description, reply            | advise                               | never, nobody or nothing about the past ("has never fired")                      |
| `intent`             | PRF2004 | change description, reply            | advise                               | a motive given to a tool or a person ("guessed", "pretends")                     |
| `credit`             | PRF2005 | change description                   | advise                               | a removal or replacement that says nothing of what the old design was for        |
| `claim`              | PRF3001 | change description, reply            | advise                               | a measurement, comparison or completion with no evidence in its sentence or item |
| `reply-opener`       | PRF8001 | reply                                | deny                                 | a sentence that opens by contradicting ("No,", "As I said")                      |
| `judgment-as-fact`   | PRF8002 | reply                                | advise                               | a recommendation with no reason ("This should be a map.")                        |
| `stacked-hedge`      | PRF8003 | reply                                | advise                               | two softeners in a sentence, or an apology before the point                      |
| `long-thread`        | PRF8004 | reply                                | advise                               | the author's fourth or later reply in a thread, given `-thread-length`           |
| `signpost`           | PRF4003 | pages, change description, reply     | deny                                 | an announcement where the point should be ("Here's the thing")                   |
| `chatbot`            | PRF4004 | pages, change description, reply     | deny                                 | text addressed to a chat's user ("I hope this helps")                            |
| `leak`               | PRF4005 | all                                  | deny                                 | a citation marker or an unfilled placeholder                                     |
| `buzzword`           | PRF4006 | pages, change description, reply     | deny                                 | a word chosen to sound significant ("delve", "tapestry")                         |
| `buzzword-weak`      | PRF4007 | pages, change description, reply     | advise                               | a buzzword that also has a plain sense ("crucial")                               |
| `contrast`           | PRF4010 | pages, change description            | deny in a change description; advise | a claim made by denying its opposite ("not just X, it is Y")                     |
| `vague`              | PRF4008 | pages, change description, reply     | deny                                 | weight or consensus with nothing named ("experts argue")                         |
| `closer`             | PRF4009 | pages, change description, reply     | deny                                 | a sentence announcing it restates the text ("In conclusion,")                    |
| `ing-tail`           | PRF4011 | pages, change description            | advise                               | a participle clause claiming significance (", highlighting")                     |
| `staccato`           | PRF4012 | reference, change description        | deny in a change description; advise | three or more sentences of six words or fewer in a row                           |
| `dash`               | PRF5002 | pages, change description, reply     | off (house)                          | an em dash, an en dash or a spaced double hyphen                                 |
| `ascii`              | PRF5003 | pages, change description, reply     | off (house)                          | a curly quote, an ellipsis character or an emoji                                 |
| `heading-case`       | PRF4013 | reference, guide, agent instructions | advise                               | a heading whose every word after the first is capitalized                        |
| `template`           | PRF7004 | agent-instructions template          | off (house)                          | a body that does not render, so neither form can be judged                       |

"Pages" are reference pages, guides and agent instructions. A guide and agent
instructions take every reference and change-description rule but
`lead-context`. A reply takes `filler`, `terms`, `reply-voice` without its openers and
conversations, `attribution`'s credit to a tool, `condescension`, the tone rules
and its own four; it speaks in the first person, so `tense` and `hedge` do not
run on it. `blame`, `verdict`, `absolute`, `intent` and `claim` judge only text
written to teammates: a page names its reader's possible mistakes and states
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
