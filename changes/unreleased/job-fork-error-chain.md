### Fixed

- **Preserve job fork error causes.** Stacked-head resolution, VCS metadata, and job-plan write failures keep their wrapped causes for diagnostic classification and `errors.Is` or `errors.As`. Usage errors retain their exit status and message.
