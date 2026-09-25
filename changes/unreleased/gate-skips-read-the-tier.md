### Changed

- **The redundancy check, CI verdict inheritance and job completion skip only a trivial
  change.** A comment-only edit, and markdown a package embeds or a gate target reads,
  no longer skip the gate. A job's completion gate on `ci` passes on a green `ci` gate
  when the change since it is trivial.
