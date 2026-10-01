### Changed

- **A search for a symbol is refused even when the symbol index is behind the tree.** The
  guard classifies each alternative of a `grep`, `rg` or `git grep` pattern (a declaration
  lookup, a CamelCase name, a word search, a qualified member, or text) and refuses when any
  one is a name the index defines, serving `magus refs` for each name and
  `magus refs --text` for each literal text alternative. A stale index used to turn the
  refusal into advice, which let nearly every search through; it now serves
  `magus graph build --silent` first. Text, prose, one named file, stdin, `.github`, a
  directory holding no source, and a revision still run, and every refusal names what it
  classified and why.
- **Listings the graph reproduces are refused with the query.** `find` (by name or path),
  `fd`, `rg --files`, `ls <dir>` and `ls -R <dir>` whose files are all graph file nodes are
  answered by `magus query`, with the answer inline, whatever revision the index was built
  at. `git ls-files`, alone or piped into a search of its paths, is checked against the
  tracked files, and the refusal names any tracked match the graph does not index. A
  host's own content and file search tools are judged as the `rg` and `find` lines they
  stand for, and the files a search must reach are the languages a spell indexes.
- **`magus query -h` and `--help` print usage** instead of searching the graph for them.
