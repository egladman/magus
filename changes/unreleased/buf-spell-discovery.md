### Added

- **The buf spell reports the projects its buf config forms.** A `buf.work.yaml` or a
  `buf.yaml` forms a project with `build`, `lint` and `format` wired to buf, spanning the
  modules it lists. `buf-generate` is never wired; the gap names each plugin's `out:`.
  Stray `.proto` files, escaping module paths and invalid YAML are gaps naming the file.
