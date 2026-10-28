### Added

- **The buf spell reports the projects its buf config forms.** A `buf.work.yaml`, a
  v2 `buf.yaml`, and a v1 `buf.yaml` that no workspace lists each form a project with
  `build`, `lint` and `format` wired to `buf-build`, `buf-lint` and `buf-format`, and
  the modules they list as spanned directories. `buf-generate` is never wired: the gap
  names each plugin's `out:` from `buf.gen.yaml` for a generate target to declare. A
  `.proto` file outside every listed module, `.proto` files with no `buf.yaml`, a
  module path that leaves the workspace root, and a marker that is not valid YAML are
  each reported as a gap naming the file.
