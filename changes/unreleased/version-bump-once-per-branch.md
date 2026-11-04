### Added

- **This repository's guard holds a branch to one bump per version counter.** An edit that
  changes a schema or format counter, such as `agent-skill-version` or
  `knowledge-schema-version`, is denied when the branch already changed it against its
  merge base. The first bump and a return to the base value pass. The counters are listed in
  `hack/policy/version-bumps.buzz`.
