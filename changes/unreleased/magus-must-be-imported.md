### Changed

- **Breaking: `magus` must be imported.** A magusfile, spell or script calling `magus\`
  needs `import "magus";`; without it the load fails with MGS1039, which names the fix.
