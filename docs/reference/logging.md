---
title: Logging and verbosity
description: What each verbosity level actually prints, when to reach for one over another, and what ends up in your logs that you may not want to paste into a bug report.
tags:
  [
    logging,
    verbosity,
    quiet,
    silent,
    debug,
    trace,
    log level,
    log format,
  ]
---

# Logging and verbosity

magus has four output modes over three log levels. The flags are cheap to
remember; what they actually buy you is not obvious from their names, and two of
them change behavior beyond filtering.

| Flag                 | Level | What you get                                                |
| -------------------- | ----- | ----------------------------------------------------------- |
| `-q` / `--quiet`     | error | Which targets failed, and their full output. Nothing else.  |
| _(none)_             | info  | The run's scoreboard, plus warnings and workspace changes.  |
| `-v`                 | debug | Cache-key derivation and every subprocess, scoreboard kept. |
| `-vv`                | debug | The same, plus each target's output live and interleaved.   |
| `-vvv`               | trace | The same, plus DAG scheduling and a startup timing table.   |

`-s` / `--silent` is a variant of `--quiet`, not a fifth level. See
[quiet versus silent](#quiet-versus-silent).

Set the level without flags via `log.level` in `magus.yaml` or
`MAGUS_LOG_LEVEL`. See [Configuration](config.md) for the full key list.

## Default: the scoreboard

Without flags you get the result of each target and little else:

```
[pass] docs (ran, 1m36s)
  magus run generate:rw docs
out3a777178
[summary] 2 cached, 5 ran, 0 failed (1m44s)
```

Each result carries an [output reference](../concepts/cache/output-refs.md) - the short
`out...` id addressing the exact bytes that run produced. `magus query output
out3a777178` replays them verbatim.

A failure prints more, because more is actionable:

```
[fail] docs generate:rw (ran, 50s)
  cause: magus generate docs: 1 spell(s) failed
  output: out462efb79
  inspect: magus query output out462efb79
  reproduce: magus run generate:rw docs
```

The `inspect:` hint is omitted on CI. A reference addresses your local cache, so
a command that cannot work where the log is read is worse than no hint; the
reference itself stays, since it still correlates the failure with the run.

Warnings surface here too, because they are things magus wants you to reconsider
before they become failures: a charm no target declares (usually a typo), a
filesystem with coarse mtime resolution that can return stale cache hashes, a
concurrency setting above twice your CPU count.

What you do NOT get by default is the target's own output. A passing target's
subprocess stdout and stderr are withheld entirely, and shown only if it fails.
That is what keeps a wide fan-out readable, and it is what `-vv` turns
off (see [below](#-vv-the-firehose)).

## `-v`: why did this run, and what did it run

Reach for `-v` when the question is **"why did magus rebuild this?"** or
**"what command did it actually execute?"**.

Two records answer nearly every such question. `cache.key` fires once per step
with the derived hash and the inputs that produced it - source count, dependency
count, tool versions, charms. `run.exec` fires once per subprocess with the
command, its full arguments, and its directory, rendered as a shell line you can
copy and run yourself:

```
  $ go test ./... -race
```

Beyond that: cross-project dispatch, spell fan-out decisions, the derived
affected set with the full project list, and breadcrumbs for best-effort
operations that failed quietly (a knowledge-shard push, a diagnostics write).

Every record also gains a `dir` attribute naming the project that emitted it.
magus never changes the process working directory - targets carry their own
directory so they can run concurrently - so per-line attribution is how a log
line is traced back to its project. There is no "entering directory" marker to
look for, and with parallel targets there could not usefully be one.

## `-vv`: the firehose

`-v` keeps the scoreboard: each target's own output is still withheld unless it
fails, so the run stays readable. `-vv` turns that off, streaming every target's
subprocess output live and interleaved.

The two are separate because they answer separate questions. Raising the log
level asks magus for more records ABOUT the run; it is not a request to stop
withholding the output OF each target. Reach for `-vv` when you want to watch a
build happen, and expect concurrent projects to interleave.

You can set this without the flag, independent of level:

```yaml
# magus.yaml
log:
  stream: true
```

or `MAGUS_LOG_STREAM=1`. `-vvv` implies `-vv`.

## `-vvv`: scheduling and startup

Trace is not "even more debug". It is two specific questions.

**Why is my build serialized?** Trace adds exactly two events: `schedule.wait`
(a step's upstream dependencies) and `schedule.run` (its admission to the pool,
with its slot count and whether it holds the pool exclusively). Together they
answer what a step is waiting on, without a profiler.

**Why does magus take so long to start?** On exit, trace prints a phase timing
table:

```
magus startup trace:
  startup.find_root_early              1.204ms
  startup.config_load                  3.881ms
  startup.flag_parse                   0.442ms
  total                                7.918ms
```

Trace also stamps source locations on records - but the default pretty output
does not render them. Pair it with a machine format to actually see them:

```sh
magus -vvv --log-format=text run build
```

Without that, `-vvv` pays for collecting source locations and shows you nothing
extra.

## Quiet versus silent

Both suppress everything below error. They differ in what happens when a target
fails, and that difference is the reason to pick one.

**`--quiet`** dumps the failing target's log in full, verbatim. Use it when you
intend to read the whole failure, and in a pipeline where you want the raw log
preserved.

**`--silent`** bounds the dump to the last 50 lines and prints the path to the
complete log:

```
-- api (failed) --
... 812 earlier line(s) omitted; full log: .magus/logs/<hash>.log
```

`--silent` also **bubbles up notices**. Any line a target prints beginning with
`magus:notice:` is re-emitted to stderr, on success as well as failure:

```sh
echo "magus:notice: deployed api v1.2.3"
```

```
notice: api: deployed api v1.2.3
```

That is the only output an otherwise-silent passing run produces, which makes
`--silent` the right choice for a scheduled or unattended run: near-zero noise,
a bounded failure excerpt, and an explicit channel for the few lines that matter.

## What ends up in your logs

Two things are logged that you may not want to share.

**Full command arguments at `-v`.** `run.exec` logs a subprocess's complete
argument list, unredacted, and renders it as a copy-pasteable shell line. A
target invoking a tool with `--token=...`, `-p <password>`, or a signing key on
the command line puts that value in your terminal. Nothing in that path redacts
anything.

Read `-v` output before pasting it into an issue, a chat, or a CI artifact.

**Full request URLs at default verbosity.** Outbound HTTP from a magusfile is
logged with the complete URL including its query string, at info - no flag
required. Presigned URLs and `?token=` parameters land in ordinary output.

Prefer environment variables over command-line arguments for secrets in
magusfile targets. That is good practice regardless, since a process's arguments
are readable by other processes on the machine, but it also keeps them out of
these two paths.

## Formats

`--log-format` selects the renderer: `pretty` (default, for a terminal), `text`,
or `json`.

One thing to know before piping: with `json`, cache events go to **stdout**
while diagnostics go to **stderr**. Redirecting only stdout gives you a clean
event stream and leaves warnings on the terminal, which is usually what you
want - but it does mean neither stream is the whole picture.

Some output is also TTY-dependent by design. The concurrency pool status line is
suppressed when output is not a terminal, so a piped or CI log will not contain
it. That is intentional: it repaints in place and would otherwise flood the log.
