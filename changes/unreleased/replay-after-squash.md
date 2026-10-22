### Changed

- **`hack/dev/rebase-branches-onto-base.buzz` recovers a pull request whose base pull request squash-merged.** It finds the newest merged pull request head the branch still carries, rebases the branch's own commits onto `origin/main` in a fresh `magus job fork --checkout` checkout, regenerates outputs through `magus affected ci`, and pushes with `--force-with-lease`. `hack/ci/pull-requests.buzz` routes such a conflicting pull request to it as the `rebase-branches-onto-base` action. `hack/dev/update-stack.buzz` is now `hack/dev/merge-base-into-branches.buzz`.
