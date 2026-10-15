### Changed

- **The agents magus ships run on an exact model id.** A harness spell's `harness_models` rows may name the model tier each id runs (`"tier": "economy"`). A workspace that declares no tiers gets the shipped ones mapped to those ids, so a spawn's model resolves to a tier by equality, and Claude Code's `magus-scout` runs on `claude-haiku-5-5` again.
