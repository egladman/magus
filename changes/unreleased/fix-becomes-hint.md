### Changed

- **Breaking: a diagnostic's `fix` is `hint`.** The field that names the action clearing a
  diagnostic is `hint` in every form: the JSON and YAML key `"hint"` and `hint:` (was
  `fix`), the Buzz field `hint` on `result\Diagnostic` and a caught error's map, and the
  CLI line `hint: <action>` (was `fix: <action>`). The Go fields are `Hint`, and
  `diagnostics.Error.WithFix` is `WithHint`. A script or consumer that reads the old `fix`
  key fails to find it; no alias reads it.
