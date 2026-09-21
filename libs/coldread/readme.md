# coldread

A Go analyzer for comments written for a cold reader. A comment should tell
someone opening the file in six months what the code cannot. Whether one explains
WHY is semantic and out of a linter's reach; each check below reports a shape
that fails the test mechanically, with few false positives.

| Check           | Reports                                                       | Findings at landing |
| --------------- | ------------------------------------------------------------- | ------------------- |
| `aside`         | a spaced hyphen spelling an em-dash                           | 0 (swept earlier)   |
| `restate`       | a one-line comment whose every word appears in the next line  | 4, fixed            |
| `steps`         | `step 1` narration, or numbered comments sequencing a body    | 5, fixed            |
| `history`       | `used to`, `previously`, `now correctly`, `the old code`, ... | 359, off in config  |
| `docstub`       | `Foo is a Foo`, `NewFoo creates a new Foo`, `Foo ...`         | 1, fixed            |
| `commentedcode` | an own-line comment that parses as Go                         | 0                   |

Counts are over the root module, which is what golangci-lint scopes to here.

It ships as a plain `analysis.Analyzer` with no linter-runner dependency, plus a
golangci-lint module plugin in `plugin/`.

No check but `aside` reports a comment group holding a line that opens with
`TODO`, `FIXME`, `BUG`, `compat(until:`, `compat:`, or `Deprecated:`. Those carry
meaning a person or tool acts on, and a gate red because a note exists does not
do the work.

## The intent checks

**restate** needs every word of the comment, less stop words, to appear among the
next line's identifier parts, literals, keywords, or operator verbs (`++` reads
as increment), and at least one to be an identifier. It caps at five words and
skips a trailing comment, a declaration doc (docstub's job), and a comment over a
line ending in a comma, which labels a run of table rows rather than one line.

**steps** looks only inside function bodies. A doc comment's numbered list is
contract, and a numbered comment over a table element is a label.

**history** is a phrase list, not a word list. The bare words `now`, `fixed`,
`instead of` and `correctly` were measured and rejected: they match 441, 212,
680 and 83 comment lines here, and most describe the code as it stands.
`instead of` above all is how a comment names the alternative it rejected. The
list keeps one known false positive: a purpose after a bare noun ("the key used
to sign") reads the same as a habit and is reported. A purpose after a verb
("is used to sign") is not.

**docstub** reports a one-line declaration doc made only of the symbol's name,
its receiver's, and stub vocabulary. `implements` is deliberately not stub
vocabulary: `String implements fmt.Stringer` names a contract.

**commentedcode** reports an own-line run of comment lines that parses as Go
statements or declarations and carries punctuation prose almost never does
(`:=`, `==`, braces, a method call). An indented run is a godoc example, and an
example function's `Output:` block is test data; both are skipped. A trailing
comment is skipped too, since code-shaped text there (`field!=value`) is an
annotation.

## The aside check

```text
// The run is cached - so nothing executes.   <- reported
// The run is cached: nothing executes.       <- one fix
// The run is cached; nothing executes.       <- another
// The run is cached (nothing executes).      <- another
```

### What aside does not report

`" - "` reaches a Go comment for four reasons that are not prose punctuation, and
each is exempt.

Measured over this repository before the tree was swept, with
`./custom-gcl run --default=none` and only this check enabled: **4513 findings
across 809 files**. In those same 809 files a plain `grep '^\s*//.* - '` matches 4719
lines, which splits exactly:

| Outcome                                 | Lines | Why                                                    |
| --------------------------------------- | ----- | ------------------------------------------------------ |
| Reported                                | 4489  | prose punctuation                                      |
| Exempt: doc-list bullet (`//   - item`) | 206   | gofmt writes list items exactly this way               |
| Exempt: indented preformatted block     | 11    | godoc renders it verbatim; command examples live here  |
| Exempt: span inside backticks           | 10    | the hyphen belongs to a literal                        |
| Exempt: digit on each side (`100 - 30`) | 3     | arithmetic or a range, where no punctuation is the fix |

The remaining 24 findings are lines that grep cannot reach: a list item whose own
text carries a second aside, and lines inside a `/* */` block, which do not start
with `//`.

Across the whole tree the same grep matches 5125 lines and its bullet form
matches 303. golangci-lint scopes to the root module here, so the nested `libs/*`
modules, `testdata` trees, and generated files account for the rest.

A URL needs no exemption. `" - "` contains spaces and a URL does not, so
`https://example.com/a-b-c` cannot spell the pattern. A line carrying both a URL
and an aside is still reported, for the aside.

#### The bullet exemption is not optional

gofmt owns doc-comment list formatting. Write `// - item` above a declaration and
gofmt rewrites it to `//   - item`; there is no spelling of a doc list that
avoids the spaced hyphen. Reporting one would put the linter and the formatter in
a fight neither can win.

The marker is skipped, not the line. An item whose own text carries an aside is
still reported:

```go
//   - the item body carries an aside - which is reported
```

#### Indentation follows go/doc/comment

Deciding that a line is preformatted takes the three steps `go/doc/comment`
takes: strip the comment marker's single leading space, remove the indent every
line in the group shares, and treat what is still indented as code, unless it is
a list item or the continuation of one.

The shortcut of "indented means code" was measured and rejected. Applied to this
tree it loses **54 genuine findings**: wrapped list items and numbered steps sit
indented and are prose, and only the model above tells them apart from a command
example.

#### Arithmetic between identifiers is still reported

The digit rule is deliberately narrow. `n - 1` is arithmetic too, and it is
reported, because from outside the surrounding code it is indistinguishable from
prose. This tree holds one such line against 4513 findings, which is the wrong
trade to widen the hole for.

### The wrapped half

An aside whose second clause sits on the next line ends its first line in `" -"`.
No line-oriented search can see it, because the trailing hyphen has no space
after it. This tree holds **281** of them, disjoint from every number above.

They are off by default and enabled with `wrapped: true`. The split is about the
fix, not the rule: an inline aside is a one-line edit, while a wrapped one
rewraps a paragraph. Sweep them separately.

## Configuration

```yaml
version: "2"

linters:
  enable:
    - coldread
  settings:
    custom:
      coldread:
        type: module
        description: reports a comment that tells a cold reader nothing the code does not
        original-url: github.com/egladman/magus/libs/coldread
        settings:
          # Exempt globs, filepath.Match against a file's base name. A malformed
          # glob fails at config load and names the pattern.
          allow:
            - "*_gen.go"
          # Also report a comment line ENDING in a spaced hyphen. See above.
          wrapped: false
          # Checks that do not run. An unknown name fails at config load.
          disable:
            - history
```

Each diagnostic opens with its check's name (`restate: ...`), which is also its
`Category`, so a finding names what to disable.

## Building the binary

golangci-lint compiles plugins in rather than loading them at runtime, so you
build a binary carrying this one. The declaration lives in
`libs/testlayout/.custom-gcl.yml`, which names every in-repo plugin, and
`golangci-lint custom` reads that file from its working directory:

```bash
cd libs/testlayout && golangci-lint custom
```

`destination: ../../.magus` writes `.magus/custom-gcl`. `magus run lint`
does both steps.

## Standalone use

The analyzer has no golangci-lint dependency, so `singlechecker` and `go vet`
tools work too:

```go
singlechecker.Main(coldread.Analyzer)
```

`Analyzer` is the default configuration and exposes no flags. To set `allow`,
`wrapped`, or `disable` outside golangci-lint, build your own with
`coldread.New(coldread.Options{...})`, which errors on a malformed glob or an
unknown check name. restate reads each file's source through `Pass.ReadFile`; a
driver that leaves it nil silences restate and commentedcode rather than failing.

## Known limits

A backtick span that opens on one line and closes on the next is blanked only on
the opening line, so a hyphen on the closing line is reported. Carrying backtick
state across lines was the alternative, and it lets one stray backtick silence
every comment after it. A false positive gets reported by whoever hits it; a
false negative never does.

Only the ASCII spaced hyphen is matched. A literal em-dash or en-dash character
in a comment is not reported: code comments are exempt from this repo's
plain-ASCII rule, and nothing has needed the check.
