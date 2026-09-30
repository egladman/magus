### Fixed

- **Every `magus\` type a script names is declared and buildable.** `magus\Context` and
  `magus\Exec` are typed, so a misspelled ctx member fails the check. `magus\DirsOptions{...}`
  and every other record and enum construct at run time, including through an aliased import.
  Callback parameters are `any` instead of an undeclared `Function`.
