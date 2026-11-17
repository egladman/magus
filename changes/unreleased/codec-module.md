### Changed

- **Breaking for SDK callers: JSON and the JSON Lines store live in `libs/codec`.**
  `internal/json` and the JSON Lines helpers of `internal/file` are gone; `codec.Marshal`,
  `Unmarshal`, `UnmarshalStrict`, `RawMessage`, `JSONL[T]`, `ReadLines` and
  `AppendLines` keep their names. `codec.RawMessage` is `encoding/json.RawMessage` in every
  build. No alias remains.
- **Breaking: the codec's own errors read the same in every build.** A malformed document
  is a `*codec.SyntaxError` naming its byte offset, and `UnmarshalStrict` wraps it in a
  `*codec.StrictError`. Stores report `*codec.LegacyStoreError` and `*codec.LineError`,
  which magus gives MGS3049 and MGS3050.
- **Breaking: a build without `GOEXPERIMENT=jsonv2` compiles.** The codec then runs on
  `encoding/json` and refuses, with a `*codec.UnsupportedError`, a type that build would
  write differently. magus itself still builds with the experiment.
- **Breaking: `libs/safetyscissors` records drop omitempty on a bool or number.** A zero
  count or false flag is written; a struct or pointer is omitzero, and a non-nil pointer
  to an empty value is written.
