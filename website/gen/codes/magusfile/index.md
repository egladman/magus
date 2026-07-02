# Magusfile authoring diagnostics

Codes in the `MGS1xxx` range flag problems with how a workspace's magusfile(s)
are authored: targets that must exist but don't, declarations that won't
resolve, and similar. Magus raises them at run time (as a typed
`DiagnosticError`) and, where applicable, as a `magus doctor` health check so the
gap is visible before CI runs.

| Code      | Meaning                                           |
| --------- | ------------------------------------------------- |
| `MGS1001` | no `ci` target defined in the selected project(s) |
