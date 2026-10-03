### Fixed

- **`magus describe harness` retires only the host entries a harness spell marked as its own.**
  A hook entry is magus's when it runs a shipped template or its command ends in
  `# magus:harness`; a harness spell declaring any other entry is refused. Every other
  hook stays yours, whatever it mentions.
