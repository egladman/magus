# Changelog fragments

Each unreleased change adds its changelog entry as one file under `unreleased/`.
There is no changelog file to edit, so concurrent pull requests add different
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
`magus run pr-changelog . -- "<title>"` checks a branch's fragments, and
`magus run lint .` checks them all.
`hack/changelog.buzz` holds the grammar, and `testdata/fragments.txtar` holds the
cases it and `magus-utils cut` are both tested against.

`magus run changelog-page docs` renders the changelog page from these fragments
and the release manifests under `releases/`. `magus-utils cut` folds the fragments
into the release manifest and deletes them when a release is cut.
