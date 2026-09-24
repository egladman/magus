### Fixed

- **Share and wrong-method failures answer in the refusal shape.** `/api/v1/share` and a
  wrong method on any `/api/` route send AIP-193 JSON (MGS9012-MGS9014); the console shows
  its message and Help link. Health reports down when every workspace failed, and the
  Windows sign-in line is PowerShell's `Start-Process`.
