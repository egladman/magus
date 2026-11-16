### Changed

- **`pr-changelog` ties a breaking change's two markers together.** A title without
  `!` that adds a `Breaking:` entry is refused, and so is a `!` title that owes a
  fragment but adds no such entry. Each refusal names the fix.
