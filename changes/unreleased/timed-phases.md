### Added

- **A slow bootstrap or fork names the phase that ran past its bound.** Bootstrap steps, the job store's landing sweep and a fork's checkout reads each have a warm-cache bound. Past it, one line names the step, its time against the bound and the likely cause. Nothing is refused.
