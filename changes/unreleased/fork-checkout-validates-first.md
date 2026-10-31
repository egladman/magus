### Fixed

- **`magus job fork` validates before it builds and follows the checkpoint.** A held checkout fast-forwards to a new checkpoint or is refused with the path and reason. A `./magus` built at the base is reused, only the projects a job touches install, and text output repeats no goal or path.
