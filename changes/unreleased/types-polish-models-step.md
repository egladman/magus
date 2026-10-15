### Changed

- **Breaking: an `agents.models` step replaces the harness row's whole step.** An entry setting `step_factor` alone, which kept the harness row's `price_step`, is an error; give both. A `harness_models` row setting only one of the two is an error too.
