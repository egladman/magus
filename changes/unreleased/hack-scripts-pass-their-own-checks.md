### Fixed

- **`hack/dev/land-commits.buzz` passes the title the way `pr-title` and `pr-changelog` read it.** Both targets take the title as their argument; the script sent `--title=<text>`, so the check failed and nothing was pushed. A rerun now continues on the branch an earlier run made from the same commits instead of refusing its name.
- **`buzz-test` runs the Buzz modules that exist.** `docs/lib` lists adr, conventions, crosscheck, design, dictionary and glossary; the modules that moved to `libs/ssg` run from there. A test fails when a listed module is missing or a module is unlisted.
- **The `merge-job-branches` tests no longer leave a store directory in the user cache.** The fixture repository now sets `cache.dir` inside itself.
