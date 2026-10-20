### Fixed

- **A spawn naming a host's short model, such as Claude Code's `opus`, is judged at its row's tier.** spawn-names-model and spawn-above-model-tier resolved a spawn bound to a job row only through the workspace's declaration, which maps full model ids, so every alias was refused as having no tier. They now also read the `harness_models` aliases, as spawn-above-parent-tier already did.
