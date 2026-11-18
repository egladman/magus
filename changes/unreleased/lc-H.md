### Changed

- **Breaking: the JSON keys magus owns that were camelCase are snake_case.** The cache
  manifest (`project_path`, `created_at`, `duration_ms`, `log_records`), the disk samples
  store (`total_bytes`, `free_bytes`), the `dry` playground graph (`depends_on`,
  `no_cache`), spell discovery (`depends_on`, `not_inferred`), the spell syntax and
  version descriptors (`line_comments`, `block_comments`, `ignore_escape`, `body_style`,
  `up_to`), `describe` output (`call_index`, `when_args`, `retry_on_volatile`,
  `include_os`, `include_arch`, `trim_mb`, `list_command`) and
  `hack/dev/show-import-meet.buzz -o json` (`a_reaches_b`, `b_reaches_a`,
  `importers_of_both`). External formats keep their spelling. `tagliatelle` holds the
  rule in lint, with path exclusions for the mirrors.
- **The cache key version is 8.** A manifest in the old spelling read back empty, so
  every entry from before is a miss and runs once.
- **A disk samples line in the old spelling is refused.** `magus doctor` and the
  diskwatch module name the line and say to delete `disk/samples.jsonl`.
