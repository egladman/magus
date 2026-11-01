### Added

- **The tool-time row records which worker ran a call and a digest of its result.**
  `magus shell --tool-ms` takes `--call-digest` and `--result-digest`, and the row
  names the thread and subagent. A host that gives no digest is recorded as
  unobserved, never as an empty value, and the result itself is never stored.
- **`magus session show` reports calls repeated with an unchanged answer.** The
  `repeats` section counts runs of one call, one answer and no write by the same
  thread between, with their turns and tool time. It applies no threshold; this
  repository's `hack/policy/repeated_calls.buzz` owns the count, window, tools and
  exemptions.
