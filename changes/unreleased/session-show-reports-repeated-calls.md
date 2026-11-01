### Added

- **`magus session show` reports calls repeated with an unchanged answer.** The
  `repeats` section counts runs of one call, one answer and no write by the same
  thread between, with their turns and tool time. It applies no threshold; this
  repository's `hack/policy/repeated_calls.buzz` owns the count, window, tools and
  exemptions.
