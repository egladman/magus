---
title: Flags, config and help from one source
order: 33
description: Each flag is declared once, by its positive name. Parsing, help, the man page, the completions and the config vocabulary read that declaration.
tags: [design, principle, cli, flags, config]
---

# Flags, config and help from one source

A flag is declared once, in the command registry
([`internal/commands/registry.go`](https://github.com/egladman/magus/blob/main/internal/commands/registry.go)).
The parser, `-h`, the man page, the markdown reference and the shell
completions are all produced from that declaration. None of them keeps a
second copy to drift from it.

The failure this prevents is two spellings for one idea. `graph build` took
`--no-symbols` while `graph export` took `--symbols`, so the same concept was
on by default in one place and off in the other, and a reader had to learn
both polarities. `--no-cache=false` meant "use the cache", a double negative no
one writes on purpose. Help printed `-symbols` with a single dash, and a
backquoted word in a usage string became a value name in the middle of a
sentence.

**The conventions.**

- **A bool is named for what it turns on.** The registry declares `cache`, not
  `no-cache`, and gives it a default. A bool with no default, or one whose name
  starts with `no-`, is refused when the flags are generated.
- **The off spelling is generated.** Every bool also takes `--no-<name>`.
  The generator adds it; nobody declares it. A one-character flag has no
  negation, and an alias does not get one.
- **`--no-<name>` takes no value.** `--no-cache=false` is an error that names
  `--cache` as the spelling to use. `--cache=false` still works.
- **Help shows both forms and the default.** A bool reads
  `--[no-]cache (default: on)`. Help is printed from the same table the man
  page is, never from `flag.PrintDefaults`.
- **One concept, one spelling per surface.** `Vocabulary` in
  [`internal/commands/vocabulary.go`](https://github.com/egladman/magus/blob/main/internal/commands/vocabulary.go)
  lists each flag with its magus.yaml key, its Go option field and its Buzz
  option-record field. A column that is filled in must be the flag's name in
  that surface's case.

## Enforced by

- `cmdline.Check` and the generator refuse a bool named `no-x`, a bool with no
  default, and a bool whose `--no-x` another flag already takes.
- `cmdline.NegatedBoolVar` refuses a value on `--no-x`.
- `TestEveryFlagRegistersItsConcept` fails when a flag has no `Vocabulary` row.
  `TestNoConceptHasTwoRows` fails when two rows are one concept under two
  names, and `TestEverySurfaceSpellsTheFlag` fails when a surface spells a row
  another way.
- `TestManpageFlagsMatchTheCLI` fails when a command binds a flag the registry
  does not declare, or the reverse.

## Good

```go
{Name: "cache", Kind: cmdline.Bool, Default: true, Doc: "Replay a cache hit"},
```

Typed as `--cache` or `--no-cache`; shown as `--[no-]cache (default: on)`.

## Bad

```go
{Name: "no-cache", Kind: cmdline.Bool, Doc: "Force a fresh run"},
```

The default is unstated, the positive form `--cache` cannot be spelled, and
`--no-cache=false` reads as its opposite.

## See also

- [One source, every door](one-source-every-door.md)
- [Enforcement, not pinky promises](enforcement-not-pinky-promises.md)
