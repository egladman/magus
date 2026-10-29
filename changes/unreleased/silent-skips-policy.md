### Fixed

- **A raising workspace guard rule holds back change-landing calls.** While it raises, pushes, PR creation and merges, state-writing verbs and commits are denied; other calls still run.
