### Fixed

- **A step's process group survives a broker restart and still ends with its run.** A
  stopping broker stopped every group a run had handed it, and a run re-asserted only
  its claims, so a group outlived a run that died after a broker crash. A stopping
  broker now leaves groups to their runs, which hand them to the next broker.
