package commands

const buzzLongHelp = `Run Buzz source from a REPL, a file, stdin, or an inline snippet.

With no argument on a terminal it opens a REPL with the magusfile at the
current directory loaded, its targets and bindings ready. A piped or
redirected stdin runs as a script instead. In both, the Buzz stdlib, every
magus host module (fs, os, http, markdown, and the rest), and the magus
namespace are available, so a one-off script needs no dependency install.

A script run inside a workspace runs under that workspace's sandbox policy,
the same one a target gets, so its file, process and network access is
governed identically and a refusal is recorded on the activity trail.

Parsing is upstream-strict by default: a file written for the magusfile
engine needs --embedded, or it fails on rules upstream Buzz enforces and
magus does not. The most common one is "argument N must be labeled".

Normal execution of .magus/harness/hooks/<name> runs the script and shared
helper embedded in this binary. Installed copies may be stale or missing;
they never supply runtime code. Run an authored template outside that reserved
directory to try edits. --check and -t still inspect the named files.

-t runs the test blocks of every script given and reports pass or fail, which
is how Buzz code in this ecosystem is tested. --coverprofile writes one LCOV
report across all the files under -t (a file is measured when it is a -t
subject; its imports are not). The lsp subcommand speaks the Language Server
Protocol over stdio for an editor integration.

--check parses and type-checks the named files and does not run them, which
is the only way to judge a script whose whole job is a side effect: a hook
that reads stdin and shells out cannot be validated by running it. It takes
several paths, reports every diagnostic rather than stopping at the first,
and fails on any of them, warnings included. Resolving a file import still
executes that module's top level, since there is no check-only import pass.

--check resolves magus/spell/* to the spells this build ships, as a
magusfile load does, without loading the workspace, so a magusfile or a
target definition checks as a standalone file. A file that imports one is
checked by the --embedded rules the loader reads it under. Only the named
file's own imports decide that: a spell import reached through a module it
imports does not. A misspelled spell import fails, and a hint names the
spells that resolve. A project/<path> import needs the workspace and fails.

-C changes the working directory first, in every mode, so one command line
runs the same from any directory: magus buzz -C "$ROOT" -s hooks/guard.buzz.`
