### Changed

- **A CI plan computes another project's prerequisite in stage zero.** A computing
  prerequisite that a unit of another project needs leaves stage one, so the shards
  that need it replay it instead of waiting on it. A stage-one shard that would still
  compute one is refused ([MGS3054](https://eli.gladman.cc/magus/reference/codes/sandbox/MGS3054/)).
- **The load refuses three couplings between projects.** A source glob reaching into
  another project's directory ([MGS1060](https://eli.gladman.cc/magus/reference/codes/magusfile/MGS1060/)),
  a `depends_on` naming the root ([MGS1061](https://eli.gladman.cc/magus/reference/codes/magusfile/MGS1061/)),
  and a cross-project `ctx.needs` that `depends_on` leaves out
  ([MGS1062](https://eli.gladman.cc/magus/reference/codes/magusfile/MGS1062/)). Root, docs and
  console now declare each project they need, and docs reads root files through `readsFiles`.
- **An edge folded from a cross-project `ctx.needs` orders only the target it names.** Docs
  `build` no longer waits on the root's `build`. Same-target ordering holds only for an
  edge `depends_on` declares.
