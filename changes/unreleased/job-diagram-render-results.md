### Fixed

- **Job graph SVG output consumes rendering results.** The Go diagram bridge
  unwraps the Buzz renderer's `Result` and preserves validation findings as
  errors. A refused diagram leaves the output directory untouched.
