### Added

- **A job problem carries its remedy as `goal` or `deviation` data.** Each MGS3044 and MGS3045
  problem in `-o json` names the exact goal or deviation that settles it, so a root merges
  `problems[].goal` in one `jq` pass instead of parsing `fix`. Magus never applies them: #771 dropped
  `--fix` because applying a remedy was magus acting, and printing one is computing.
