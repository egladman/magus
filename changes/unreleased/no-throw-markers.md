### Changed

- **`no-throw` honors no comment.** A `//nolint:no-throw` comment, or a `platform double` or
  `platform contract` comment, is a finding in any file, and `typescript-never-throws` no
  longer excuses a throw or a reject under one. A test double for a failing platform call
  takes its rejection from `console/src/lib/rejected.ts`.
- **The Buzz scan reads a function that returns a map type.** `> {str: int} !> any` was
  never counted; the sites it found now return `result\Result::<T>`, as do the `std/lib`
  helpers `lcov`, `diskwatch` and `reclaimdisk`, `libs/kustomize` and `spells/git/hooks`.
