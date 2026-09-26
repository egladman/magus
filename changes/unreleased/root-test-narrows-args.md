### Fixed

- **`magus run test . -- -run X` runs only the selected tests.** The workspace's `test`
  target passed explicit arguments to `go test`, which replaced the forwarded ones, so every
  narrowed run executed the whole suite. It now appends its forwarded args and skips the
  coverage floor when narrowed; the spell docs show the `+ args` idiom.
