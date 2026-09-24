### Added

- **Tools declare `observe` probes; ops declare external effects.** An observation keys
  only the targets that drive the tool (`obs:`). Ops mark `reads-external` or
  `mutates-external`, and MGS1033 fails a cacheable target composing one with neither an
  observation nor `skip_cache`. The docker spell gains `trivy-image`.
