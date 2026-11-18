### Changed

- **`magus job fork` refuses a check its worker could never run because a person runs it.** A `--check`, or a postcondition's check, naming a `human_only` target is refused with the declared reason, and the refusal asks for a check a worker may run, not a target that only compiles. Postcondition checks are graded against the guard like the row's own.
