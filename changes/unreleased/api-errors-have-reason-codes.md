### Changed

- **Every `/api/` error carries a reason code.** Validation, not-found, conflict and
  internal failures answer in the auth refusals' JSON envelope instead of plain text:
  MGS9023 invalid request, MGS9024 not found, MGS9025 state conflict, MGS9026 no
  workspace, MGS9027 internal failure, MGS9028 streaming unsupported, MGS9029 review host
  failed. Statuses are unchanged; an internal failure keeps its cause in the server log.
