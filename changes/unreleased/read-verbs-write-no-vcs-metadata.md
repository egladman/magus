### Security

- **No read verb writes VCS metadata.** `magus ls`, `describe` and `query` no longer
  register the merge driver in `.gitattributes` and `.git/config`. Only `magus init --vcs
  <name>` installs it; later runs refresh an existing registration. `magus doctor` names
  that command when the diff drivers are missing.
