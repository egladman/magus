---
title: "Postel's law: accept liberally, name the default"
order: 35
description: Accept every well-formed spelling of a value, and make the unit a bare number takes loud in help; never encode the unit in the flag name.
tags: [design, principle, cli, flags, postel]
---

# Postel's law: accept liberally, name the default

Postel's robustness principle says to be conservative in what you send and
liberal in what you accept. On a CLI that principle is concrete: a duration
flag takes `5m`, `300s` and `300000ms`, and a bare `300` still works when the
caller already knows what unit the flag means. The same idea applies wherever
a value has more than one honest spelling: a size in bytes or kibibytes, a
rate with a unit suffix, a count that admits a SI prefix.

The failure it prevents is two kinds of silent guess. Encoding the unit in
the flag name (`--tool-ms`, `--timeout-sec`) forces every caller to learn a
new spelling for each unit, and it locks the flag to one unit forever: adding
`5m` later means a second flag or a breaking rename. Accepting a bare number
with an undeclared default is worse, because help never says what `300` means
and an agent that guesses milliseconds when the flag meant seconds is wrong
quietly.

So the rule has two halves, and both are required:

- **Accept every well-formed spelling.** A Go duration string (`5m`, `420ms`,
  `1h30m`) parses. A bare number parses too, counted in the flag's declared
  unit. The flag's name never carries the unit.
- **Name the default unit in help.** The declaration carries a `Unit` (ms, s,
  m, h). Help, the man page and `-h` append "Bare numbers are milliseconds"
  (or seconds, minutes, hours) so a person or an agent never has to guess. A
  Duration without a Unit is refused when the flags are generated.

This is the permissive twin of [No silent defaults](../judgment/no-silent-defaults.md).
That page refuses a default that changes what happens without saying so. This
page refuses a default that *is* said (in help, in the declaration) so the
liberal accept stays safe. The unit is a fact the registry owns, not a
convention the caller is expected to remember.

## Enforced by

- `cmdline.Check` refuses a Duration flag with no `Unit`, and refuses `Unit`
  on any other Kind.
- `cmdline.DurationUsage` appends the bare-unit sentence to every Duration
  flag's help; binders and `BindFlags` register through `DurationVar`, which
  applies that unit to a bare number.
- `TestNoFlagNameEncodesAUnit` refuses a new flag whose name ends in a unit
  suffix (`-ms`, `-sec`, and the rest).

## Good

```go
{Name: "duration", Kind: cmdline.Duration, Unit: "ms",
 Doc: "How long the host says the tool ran"},
```

Typed as `--duration 420ms`, `--duration 5m`, or `--duration 420`. Help reads
"Bare numbers are milliseconds."

## Bad

```go
{Name: "tool-ms", Kind: cmdline.Int, Doc: "Tool runtime in milliseconds"},
```

The unit is in the name, so `5m` cannot be spelled, and a later rename to
accept minutes breaks every caller that already wrote `--tool-ms 420`.

## See also

- [No silent defaults](../judgment/no-silent-defaults.md)
- [Flags, config and help from one source](flags-config-help-one-source.md)
- [Inform people, contain the engine](inform-people-contain-the-engine.md)
