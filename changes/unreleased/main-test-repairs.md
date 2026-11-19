### Fixed

- **An encoding/json build keeps the wrapper a type's own `UnmarshalJSON` adds to a codec type error.** The
  error from `codec.Unmarshal` now reads as it does on encoding/json/v2.
