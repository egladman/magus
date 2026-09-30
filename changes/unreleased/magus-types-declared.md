### Fixed

- **Every `magus\` type a script names is declared and buildable.** `magus\Context` and
  `magus\Exec` are typed, so a misspelled ctx member fails the check. `magus\DirsOptions{...}`
  and every other record and enum construct at run time, including through an aliased import.
  Callback parameters carry their function type, so `fs\walk`, `os\withEnv`, the charm
  `*Func` anchors and the `magus\guard` rules refuse a callback of the wrong shape.
- **The playground and `--dry-run` check a script as the engine does.** `proc`, `http`,
  `vcs` and the other host modules are typed there too, so a body the engine refuses no
  longer passes a dry run.
