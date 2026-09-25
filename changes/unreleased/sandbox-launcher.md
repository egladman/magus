### Security

- **The sandbox confines each child, not magus.** A launcher applies the landlock
  ruleset to every process a run starts, so a server serves each workspace under its
  own policy. `required` needs landlock ABI 3 (Linux 6.2), else MGS2012. MGS2010 now
  means a nested or forwarded run asked for a weaker mode.
