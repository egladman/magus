# conventions

Go analyzers for this repository's own rules over Go source. Each one was a test
in the root `conventions_test.go` that walked the tree as text; as an analyzer it
runs in `magus run lint` beside the stock linters, reports at the offending line,
and takes a `//nolint:<name> // <reason>` where an exception is deliberate.

| Linter          | Reports                                                                          |
| --------------- | -------------------------------------------------------------------------------- |
| `hostagnostic`  | a line of non-test source naming an agent host outside a filesystem path         |
| `hostvocab`     | a host's tool name (`"Read"`, `"Bash"`) as a string literal in guard code        |
| `ruletext`      | guard rule text (`"magus workspace:"`) in the guard's CLI half                   |
| `asciistrings`  | a typographic glyph in a string literal of a listed user-facing file             |
| `importceiling` | a package importing more packages under a prefix than its ratchet allows         |
| `stutter`       | an exported package-level name opening with its package's name                   |
| `nameoutput`    | a `case outputName:` arm that does not render through an emitter                 |
| `testisolation` | a test binary linking the runtime-directory package with no isolating `TestMain` |

Every path, word list, ceiling and exemption lives in the root `.golangci.yml`,
so the analyzers carry the mechanism and the config carries the policy.

## Scope

golangci-lint here lints the root module, so these rules no longer reach the other
modules under `libs/` that the old tree walks covered. Rules over source text
(`hostagnostic`, `hostvocab`, `ruletext`, `asciistrings`, `importceiling`,
`nameoutput`) also read the files a package's build constraints exclude on this
platform, so a darwin run still checks the `_linux.go` files the walks did.

`testisolation` carries reach as a package fact along imports, which is why it is
the one analyzer that needs type information.

## Not here

Rules whose subject is not one package's Go source stayed tests: a vocabulary or
symbol index built from the whole tree (file names that mash two words, comments
naming symbols that exist), and rules that also read Buzz, shell or markdown
(environment sniffing, registered env vars, diagnostic raise sites, rescanning
string loops).
