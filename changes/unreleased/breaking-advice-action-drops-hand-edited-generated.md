### Removed

- **Breaking: the advice action's `hand-edited-generated` advisor and input.** It named
  generated files as hand edits whenever no declared input of their project changed, which
  was false for every output whose target opts out of the cache, such as a `MAGUS.md`
  rendered from the whole graph. The drift gate already fails on a real hand edit. Delete
  the key from `with:`.
