### Fixed

- **Release candidates publish images on the unstable channel.** The release
  workflow's registry preflight used `stable`, which refuses an `rc` tag; it now
  uses `tagged` like the login and build steps. Resume also runs spell and image
  publish after a successful publish job whose platform builds were skipped.
