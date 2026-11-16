### Changed

- **Breaking for SDK callers: JSON and the JSON Lines store live in `libs/codec`.**
  `internal/json` and the JSON Lines helpers of `internal/file` are gone, and
  `github.com/egladman/magus/libs/codec` holds both: `codec.Marshal`, `codec.Unmarshal`,
  `codec.UnmarshalStrict`, `codec.RawMessage`, `codec.JSONL[T]`, `codec.ReadLines`,
  `codec.AppendLines` and their siblings keep their names. `codec.RawMessage` is
  `encoding/json.RawMessage` in every build, no longer `jsontext.Value`. No alias
  remains; change the import and the qualifier.
- **Breaking: the codec's own errors read the same in every build.** A malformed
  document is a `*codec.SyntaxError` naming its byte offset, a repeated key, invalid
  UTF-8 or trailing data included, and `UnmarshalStrict` refuses with a
  `*codec.StrictError` around it. A store reports `*codec.LegacyStoreError` and
  `*codec.LineError`; `file.StoreDiagnostic` gives them MGS3049 and MGS3050.
- **Breaking: a build without `GOEXPERIMENT=jsonv2` compiles.** The codec then runs on
  `encoding/json` and refuses, with a `*codec.UnsupportedError`, a type that build would
  write differently: `,unknown`, a named `,inline`, omitempty on a bool, number, struct
  or interface, and a bare `time.Duration`. magus itself still builds with the
  experiment.
- **Breaking for `libs/safetyscissors` records: omitempty on a bool or number is
  gone, and on a struct or pointer it is omitzero.** A zero count or a false flag is
  written, as magus already wrote it; a job declaration's optional ones are omitzero
  and left out at zero. A non-nil pointer to an empty value is written rather than
  dropped.
