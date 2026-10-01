#!/usr/bin/env bash
# Exercises the bash completion script magus ships, inside the official bash image.
# Run by the `completion-test` target with the repo mounted at the working directory;
# there is no magus binary in the container, so this asserts the STATIC candidates
# only - the project completions shell out to `magus ls` and are legitimately empty.
set -euo pipefail

# shellcheck source=/dev/null
source cmd/magus/completions/magus.bash

# Both names must be registered, not just magus: the mgs shorthand is a symlink to
# the same binary, so a completion that knows only one name is half broken.
complete -p magus >/dev/null || { echo "magus is not registered for completion" >&2; exit 1; }
complete -p mgs >/dev/null || { echo "mgs is not registered for completion" >&2; exit 1; }

# `magus <TAB>` offers subcommands.
COMP_WORDS=(magus "")
COMP_CWORD=1
_magus_complete
[ "${#COMPREPLY[@]}" -gt 0 ] || { echo "no candidates for 'magus <TAB>'" >&2; exit 1; }

# `magus self <TAB>` offers both subcommands. Asserting a specific one keeps this
# honest: an empty COMPREPLY and a stale COMPREPLY look identical to a count check.
COMP_WORDS=(magus self "")
COMP_CWORD=2
_magus_complete
for want in update install-shorthand; do
    printf '%s\n' "${COMPREPLY[@]}" | grep -qx "$want" ||
        { echo "'magus self <TAB>' is missing $want" >&2; exit 1; }
done

echo "bash: ok"
