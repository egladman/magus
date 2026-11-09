### Changed

- **Wait remedies name the completion notice.** Remedies that sent an agent to `magus job wait` or `gh run watch` to wait now name the backgrounded run's notice. `magus job wait` only verifies a job's result, so another agent's job is read once with `magus describe job`. A worker refused a full budget files its check owed and stops.
