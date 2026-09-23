# Changelog fragments

Each unreleased change adds its changelog entry as one file under `unreleased/`,
never by editing `CHANGELOG.md`. Concurrent pull requests then add different
files instead of colliding on one section.

A fragment is the entry exactly as it will read under `[Unreleased]`: one Keep a
Changelog group heading, a blank line, and one entry.

```markdown
### Removed

- **Breaking: the `exclusive` option, with no replacement.** A magusfile that sets
  it fails with MGS1038; delete the key.
```

- The group is one of `Added`, `Changed`, `Deprecated`, `Removed`, `Fixed`,
  `Security`.
- The entry opens with a bold headline, ends with a period, continues on lines
  indented two spaces, and stays within 60 words.
- A breaking change starts its headline with `Breaking:` (or `Breaking for SDK
  callers:`).
- Name the file after the branch, `unreleased/<branch>.md`. Two entries are two
  files. Within a group, entries render in file-name order.

A malformed fragment or an unknown group is an error:
`go run ./cmd/magus-utils lint-fragments changes/unreleased/<name>.md` checks one, and
`magus run pr-changelog . -- "<title>"` checks a branch.

`CHANGELOG.md` keeps an empty `[Unreleased]`. The docs changelog page renders the
fragments there at build time, and `magus-utils cut` folds them into the release
manifest and deletes them when a release is cut.
