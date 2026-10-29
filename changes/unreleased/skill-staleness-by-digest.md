### Changed

- **An installed skill is current when its own content digest matches.** The global
  `agent-skill-version` counter is gone, and changing one skill restamps only its copies.
  Copies stamp `skill-stamp-format` instead. A copy with no format, an unknown format or no
  digest grades stale with the reason, so every install reads stale once until reinstalled.
