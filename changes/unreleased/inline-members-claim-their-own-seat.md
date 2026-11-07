### Changed

- **Breaking: every `ctx.needs` member claims a machine seat of its own.** A member that
  runs inline in a cacheable composer, in any project, claims while the composer lends,
  sized from its own runs. A member that declares nothing and has fewer than 3 measured
  runs claims no memory, and its sizing says so.
- **Breaking: a composer's claim and recorded peak cover its body alone.** `memory_mb` on
  a composer no longer covers its members: declare it on the member that needs it. Older
  outcomes still count the members until they age out.
- **Inline members record their own outcomes in the run history.**
- **`magus doctor` grades each target's own figure.** The gate-fits check weighs each ci
  member's claim alone, and MGS1030 grades each target against its own declaration.
- **Breaking: a measured claim is the highest measured peak.** The 1.25 margin over that
  peak is gone, from the peak and from every bucket of its curve.
- **Breaking: the chain memory fold is gone.** `types.SizedChainMemory`,
  `types.ChainMemoryMB`, `types.ChainMemory`, `types.MemorySizer` and
  `types.MembersClaimTheirOwn` are removed.
