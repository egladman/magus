### Added

- **`flags\parse` takes a `required` list.** A required flag that is absent or given an
  empty value raises, so a workflow passing an unset variable (`--issue "$ISSUE"`) fails
  instead of reading as a choice to skip the step.
