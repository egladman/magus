### Fixed

- **A command rule judges a push in the checkout the line enters.** After `cd <dir>`, `git -C`, `hg -R` or `jj -R`, the request's `dir` and `workspace` name that checkout, as they already did for `gh pr create`. A root session that cds into a fork to push was refused for its own branch's topics.
