### Fixed

- **A failed version probe quotes the tool's error, not a warning that mentions one.**
  MGS3035 quoted the first output line containing "err", so a mise warning about an HTTP
  "client error" hid the ERROR line that actually failed the probe. It now prefers an error
  line that is not a warning.
