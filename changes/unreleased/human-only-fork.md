### Changed

- **`magus job fork` refuses a check its worker could never run because a person runs it.** A `--check`, or a goal's check, naming a `human_only` target is refused with the declared reason, and the refusal asks for a check a worker may run, not a target that only compiles. Goal checks are graded against the guard like the row's own.
