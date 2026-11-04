### Added

- **Encode nested template values as JSON.** Use `{{json .field}}` or `{{.field | json}}` with `-o template` to emit objects, lists, escaped strings, and null through the configured JSON codec. Encoding errors remain errors.
