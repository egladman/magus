#!/usr/bin/env bash
# core-loop.session.sh - the script record-cast.py drives to produce the site's
# terminal recording. Companion to core-loop.tape, not a replacement: the tape
# renders a GIF for the README, this produces an asciicast for the web page.
#
# Same workspace either way (tapes/demo-init.sh), so the GIF and the cast cannot
# tell different stories about the same tool.
#
# Why a script rather than typing into a live shell: a recording has to be
# reproducible enough to re-record after an output change without a human
# performing it correctly. Commands are echoed before they run so the cast reads
# as a session, and the delays exist so a viewer can follow one step before the
# next arrives - not to fake work that did not happen. Every timing in the cast
# is real; only the pauses BETWEEN commands are staged.
set -u

# shellcheck source=/dev/null
source "$(dirname "${BASH_SOURCE[0]}")/demo-init.sh"

# say echoes a command the way the shell would, pauses so it can be read, runs
# it for real, then pauses again before the next one.
say() {
	printf '\033[38;5;73m$\033[0m %s\n' "$*"
	sleep 0.7
	"$@"
	sleep 1.6
}

sleep 0.8

# 1. What is in here. The whole workspace, one command.
say magus ls

# 2. Cold. A real first run against a genuinely empty cache.
say magus run ci

# 3. Warm. Byte-identical command, and the difference is the whole point.
say magus run ci

# 4. Affected. One file changes; the pipeline narrows to what that reaches.
printf '\033[38;5;73m$\033[0m %s\n' 'echo "// touched" >> libs/authkit/authkit.go'
sleep 0.7
echo "// touched" >>libs/authkit/authkit.go
sleep 0.4
say magus affected ci

sleep 1.2
