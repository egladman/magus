### Added

- **`magus.WithTargetResults` hands a Go caller each finished target as a
  `types.TargetResult`**: its `types.TargetStatus` (`ok`, `permitted`, `failed`),
  whether the cache answered it, its output ref, and on a failure the error itself, so
  `errors.Is` and `errors.As` match a coded refusal without decoding a JSONL sink. A
  cache replay reports one too.
- **The Go SDK guide's examples live in package `magus_test`** (`example_test.go`), so
  they reach only the exported API, and a test fails when a block of
  `docs/guides/go-sdk.md` stops matching them. New sections read a failed target's
  coded error and a run the guard denies.

### Fixed

- **The comment-style write rule no longer counts a Go Example's `// Output:` block as
  prose.** It refused any example whose expected output ran past the comment limits.
