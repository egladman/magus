# conventions

Go analyzers for this repository's own rules over Go source. Each one was a test
in the root `conventions_test.go` that walked the tree as text; as an analyzer it
runs in `magus run lint` beside the stock linters, reports at the offending line,
and takes a `//nolint:<name> // <reason>` where an exception is deliberate.

| Linter          | Reports                                                                                                                       |
| --------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| `hostagnostic`  | a line of non-test source naming an agent host outside a filesystem path                                                      |
| `hostvocab`     | a host's tool name (`"Read"`, `"Bash"`) as a string literal in guard code                                                     |
| `ruletext`      | guard rule text (`"magus workspace:"`) in the guard's CLI half                                                                |
| `asciistrings`  | a typographic glyph in a string literal of a listed user-facing file                                                          |
| `benchbeside`   | an `// optimization:` comment or a `Cache`/`Memo` type in a package whose tests hold no benchmark                             |
| `stutter`       | an exported package-level name opening with its package's name                                                                |
| `filenames`     | a Go file name segment that splits into two segments the tree uses as file names                                              |
| `nameoutput`    | a `case outputName:` arm that does not render through an emitter                                                              |
| `namewords`     | a function or method name, exported or not, ending in a listed word (`Of`, `For`) or using a listed short form (`Src`, `Ctx`) |
| `testisolation` | a test binary linking the runtime-directory package with no isolating `TestMain`                                              |
| `globalrestore` | a test assigning a configured package-level variable with no `t.Cleanup` or `defer` restoring it                              |
| `fieldwise`     | a test asserting every field of a struct one at a time instead of comparing the whole value once                              |
| `providerio`    | Go source outside an allowlist reaching toward a CI/VCS provider (an HTTP client, or a provider SDK import)                   |
| `diagmsg`       | a diagnostic that runs long, stacks reasons, names two commands or opens with a one- to three-word tag                        |
| `errmsg`        | an error joining clauses, misplacing `%w` or naming the wrong origin, or an error's text in a log record                      |
| `stderrprint`   | a write to `os.Stderr` outside a usage function and the display, which the verbosity flags never see                          |

Every path, word list, host name, ceiling and exemption lives in the root
`.golangci.yml`, so the analyzers carry the mechanism and the config carries the
policy. No diagnostic names this repository's identifiers or paths on its own: an
analyzer whose remedy is repository-specific takes a `hint` setting, appended to
each of its diagnostics.

`diagmsg` holds a `log/slog` message to the tag rule alone, and never lets an
allowlist exempt one: a record names what logged it with `attr.Component`, not a
tag in its text. `errmsg` judges a `log/slog` call too. `error-log` reports an
error built into a message, and `error-attr` an error's text passed as an `error`
or `err` attribute; both belong in `attr.Error`, so the display names the error's
origin once.

## Scope

golangci-lint stops at a `go.mod`, so the root `lint` target runs it once more in
each module under `libs/`, on the same config, with only `filenames`,
`hostagnostic`, `namewords`, `stutter`, `testifylint`, `testlayout` and
`testpair` enabled, plus `errmsg` outside gopherbuzz: the rules that hold in
every module. The rest name root-module paths.

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

`namewords` judges only declared functions and methods, exported or not, split
into words at underscores, camel-case boundaries and digit runs. It skips a test,
benchmark, fuzz or example entry point, a generated file, and a method that
satisfies an interface of a standard library package its package reaches, such as
`io.WriterTo`'s `WriteTo`. A one-word name is never judged against `suffixes`.
A short form is a clipped word (`Ctx` for context). An initialism Go spells in
caps, and a word the standard library spells the same way, is not one.

## Not here

Rules whose subject is not one package's Go source stayed tests: a vocabulary or
symbol index built from the whole tree (comments naming symbols that exist), and
rules that also read Buzz, shell or markdown
(environment sniffing, registered env vars, diagnostic raise sites, rescanning
string loops).

## proofread

`diagmsg` takes its message rules from proofread, the writing judge, which lives in
its own module: see [`libs/proofread/README.md`](../proofread/README.md).
