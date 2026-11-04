### Changed

- **`types.Secret` drops `Format` and `LogValue`.** `String` masks `%v`, `%+v`, `%s`, `%q`,
  `%x` and `%X`, `GoString` masks `%#v`, and `MarshalText` masks encoding/json, yaml.v3,
  go-toml and both slog handlers. `%x` and `%X` now print the mask in hex. Every other verb
  prints the address of the plaintext, never the plaintext.
