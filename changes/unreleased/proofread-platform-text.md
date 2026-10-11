### Added

- **`proofread review-reply -anchored` judges an inline comment as one.** A comment
  posted on a line of the diff, or a reply in its thread, refers to that line, so
  `nonspecific` leaves it alone and judges top-level comments only. The guard passes the
  flag for the `pulls/N/comments` and `.../replies` endpoints.

### Changed

- **proofread no longer judges what a platform wrote into a pull request.** Copilot's
  tips and firewall warning, the GitHub Pull Requests extension's footer, a review bot's
  marked summary, and a template's attestation checkbox ("- [ ] I have added tests") are
  blanked before any rule reads the text. A checkbox in the author's own words is still
  judged.
- **A `participles` finding quotes each clause it counted**, so you can check which
  -ing words it read as clauses.
- **`second-person` allows "Let's" and the project's "we" in a guide.** "We support",
  "we recommend" and "we ship" state what the project offers; a "we" doing a step ("Now
  we run the build") is still reported.
