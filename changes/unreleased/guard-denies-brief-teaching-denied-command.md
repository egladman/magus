### Added

- **The guard refuses a spawn brief that teaches a denied command (`brief-command`).** Each
  fenced shell block and inline code span in an Agent or SendMessage brief is graded like a
  shell line; a line naming a command to forbid it is passed over.
