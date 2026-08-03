#!/bin/sh
# Point THIS worktree's git merge driver at a magus built from THIS worktree.
#
# Run once per worktree, and again after a change to the merge driver itself:
#
#   ./hack/install-dogfood.sh
#
# Why it is needed. `magus init` registers the merge driver as whichever `magus` leads PATH
# (gitMergeDriverCommand, vcs/git.go). For someone using magus that is the right call - the
# registration survives an upgrade-in-place. For magus's OWN worktrees it is backwards: PATH
# holds a release, the change under test is in the working tree, and git resolves generated
# conflicts with the release. A released driver that regenerated the entire site once per
# conflicted file turned a rebase over a handful of generated files into an apparent hang,
# while the version in the tree resolved the same file in well under a second.
#
# Worktree scope is the point. .git/config is shared by every linked worktree (40 of them in
# this repo at the time of writing), so registering an absolute path there would aim every
# other worktree at this one's binary. extensions.worktreeConfig is enabled here, so
# `git config --worktree` keeps the override local to this checkout; every other worktree
# keeps whatever it had.
#
# This deliberately does NOT use ./magus. That binary is rebuilt constantly while iterating,
# and swapping the driver's binary during a rebase changes the tool mid-operation. magus-dev
# moves only when this script runs.
set -eu

root=$(git rev-parse --show-toplevel)
cd "$root"

# git config --worktree fails outright unless the extension is on, and enabling it rewrites
# how EVERY worktree reads config - so ask rather than assume, and say what to do.
if [ "$(git config --get extensions.worktreeConfig || echo false)" != "true" ]; then
  echo "install-dogfood: per-worktree config is off, so an override here would leak to every" >&2
  echo "  other worktree sharing .git/config. Enable it first:" >&2
  echo "    git config extensions.worktreeConfig true" >&2
  exit 1
fi

# Built through magus, not `go build`: the target carries the version stamp (ld_flags) and the
# gopherbuzz dependency, so magus-dev reports a real version instead of a bare "dev".
magus run dogfood .

test -x "$root/magus-dev" || { echo "install-dogfood: magus run dogfood produced no $root/magus-dev" >&2; exit 1; }

# The %O %A %B %L %P placeholders are git's, passed through verbatim. The subcommand is
# `vcs merge-driver`, matching gitDriverArgs in vcs/git.go - keep the two in step. A stale
# spelling does not fail loudly: git runs it, the dispatch rejects the arguments, and every
# generated file comes back as a hand-merge conflict instead.
git config --worktree merge.magus.driver "$root/magus-dev vcs merge-driver %O %A %B %L %P"

echo "install-dogfood: this worktree now merges generated files with $root/magus-dev"
echo "  $("$root/magus-dev" version)"
echo "  re-run after changing the merge driver, or the registration keeps the older build"
