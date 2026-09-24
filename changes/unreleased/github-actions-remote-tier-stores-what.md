### Fixed

- **The GitHub Actions remote tier stores what it uploads.** The spell read the signed
  URLs under their lowerCamel names while the service answers `signed_upload_url`, and took
  the empty URL for an existing entry: every upload reported success, nothing was stored,
  and every lookup missed. It reads either name.
