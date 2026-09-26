### Security

- **The merge queue's own git no longer runs what a change's hook configures.** Git in
  a candidate reads the repository recorded when the checkout was made, with fsmonitor
  and submodule recursion off; a rewritten `.git` refuses the change. Hooks get the
  shared object store read-only, and every head is fetched before the first hook.
