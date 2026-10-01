### Removed

- **`cmd/magus-ruledocs`.** The docs `content-generate` target renders the guard-rule
  reference through `hack/magusfile/ruledocs.buzz` from `magus\describe.rule`, the same
  catalog `magus describe rules` prints, so the pages and the CLI read one source.
