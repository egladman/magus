### Changed

- **Every command that prints a record takes every output format.** `-o json`, `yaml`, `jsonl`,
  `name` and `template=<go-template>` all work wherever one does. A command with no record
  refuses `-o` before doing its work, and a test walks the command tree. See
  [Output formats](docs/reference/output-formats.md).
