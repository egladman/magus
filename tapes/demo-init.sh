#!/usr/bin/env bash
# demo-init.sh puts the recording shell inside a throwaway copy of the demo
# workspace. It is meant to be SOURCED, not executed, because it has to leave
# the caller's shell sitting in the directory it just made:
#
#   source tapes/demo-init.sh
#
# The workspace itself is tapes/demo.txtar, and magus-termcast -materialize is
# what writes it out and makes it a git repo. This file is only the part that
# has to happen in the CALLER'S shell - a cd, a PATH, a prompt - which is the
# one thing a Go program cannot do for it.
#
# It used to carry the whole fixture inline, as eleven heredocs and about 280
# lines of Go and Buzz inside shell strings. That put the fixture beyond the
# reach of gofmt, the compiler, the linter, `magus buzz -t` and every editor,
# and it is how two real bugs lived here unnoticed: the affected set could not
# resolve a base ref, so the recording's third act fell back to running
# everything, and one of the four demo projects was never git-added.
#
# Why the fixture is an archive and not committed files: magus discovers a
# project from ANY magusfile.buzz below the workspace root. As files they would
# be projects of the magus repo itself (MGS1002); inside one txtar they are
# data. The temp directory is also what keeps the cache story honest - magus
# keeps its cache in .magus/ under the workspace root, so a fresh directory is a
# genuinely cold cache and nothing here touches the developer's own.
set -u

_demo_repo=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
_demo_root=$(mktemp -d "${TMPDIR:-/tmp}/magus-demo.XXXXXX")

# The tapes drive the magus built from this checkout, not whatever release
# happens to be on PATH, so a recording shows the behavior of the commit it was
# recorded at.
PATH="$_demo_repo:$PATH"
export PATH

# The demo projects bind the go spell, which probes its toolchain (govulncheck
# among them) to build a cache key. A version manager resolves tools PER
# DIRECTORY, and the workspace is a fresh temp dir with no config, so the shims
# on PATH resolve to nothing there and every project records an UNPROBED key
# with a warning - four lines of red in a recording whose whole job is a first
# impression. Resolving in the repo, where the pins do apply, and passing the
# real directories down is what makes the workspace inherit this checkout's
# toolchain the same way the magus binary above does.
if command -v mise >/dev/null 2>&1; then
	_demo_bins=$(cd "$_demo_repo" && mise bin-paths 2>/dev/null | tr '\n' ':')
	if [ -n "$_demo_bins" ]; then
		PATH="$_demo_bins$PATH"
		export PATH
	fi
	unset _demo_bins
fi

(cd "$_demo_repo" && go run ./cmd/magus-termcast -materialize "$_demo_root") || return 1

cd "$_demo_root" || return 1

# A short, stable prompt. The recording is 900px wide and a real prompt would
# spend most of it on a path nobody can read anyway.
PS1='$ '
export PS1

unset _demo_root _demo_repo
