### Changed

- **Every `/api/` error carries a reason code.** Validation, not-found, conflict and
  internal failures answer in the same JSON envelope as the auth refusals instead of plain
  text, with the reason in `error.details`. New codes: MGS9023 invalid request, MGS9024
  resource not found, MGS9025 request conflicts with current state, MGS9026 server has no
  workspace, MGS9027 internal failure, MGS9028 streaming unsupported, MGS9029 review host
  failed. Statuses are unchanged, and an internal failure no longer sends the cause to the
  client.
