### Changed

- **Breaking: `hack/dev/rebase-branches-onto-base.buzz` pushes only with `--push`.**
  Without it the branches are rebased in the job's checkout, the gate runs, the
  `git push --force-with-lease` each would take is printed, and the checkout and job are
  kept. A caller that relied on the push adds `--push`.
- **The person-run dev scripts ask for a missing choice on a terminal.**
  `reclaim-disk-space`, `reclaim-lost-work`, `resolve-conflicts`, `land-commits`,
  `merge-base-into-branches` and `rebase-branches-onto-base` pick the file, item, commits
  or branches they were not given, and ask once before a removal, write, push or pull
  request.
- **Every prompt has a flag.** With no terminal a missing choice is an error naming the
  flag, and a missing yes stays the dry run.
- **`hack/README.md` matches `hack/dev`.** It no longer lists the removed `render-brief`
  script or the bootstrap modules twice, and `ls-worktrees` shows `--unaccounted`.
