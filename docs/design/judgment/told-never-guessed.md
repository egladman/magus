---
title: Told, never guessed
order: 7
description: magus never sniffs where it runs to change what it does; a caller that needs different behavior asks for it.
tags: [design, judgment, configuration, ci]
---

# Told, never guessed

magus does not guess where it runs to change what it does. It does not
read `CI`, `GITHUB_ACTIONS` or `GITLAB_CI`, probe for a container, or work out
which agent host is calling, and then pick a default, a result, or which
provider or feature is on. When a caller needs different behavior, the caller
asks for it: a flag, a charm, a key in `magus.yaml`, or a variable the workflow
sets on purpose. The same command does the same work on a laptop, on a runner
and inside an agent's shell.

My words, when this was written down: "We should never be doing environment
sniffing, ever. Thinking that we're smarter than the developer, or trying to be
too clever, is incredibly dangerous."

The danger is the same one [No silent defaults](no-silent-defaults.md) names,
arriving from the other side. A guess is a setting nobody wrote. It cannot be
read in the file the reader is already looking at, it cannot be turned off
without first learning it exists, and it is wrong exactly where the environment
differs from the one its author pictured: a self-hosted runner that looks
hosted, a laptop with `CI=true` exported by a test harness, an agent host that
happens to share a field name with another. A command that passed locally and
failed in CI because magus noticed it was in CI is the worst kind of red,
because the difference is invisible in both places.

Two things are not guessing. The first is adapting to the terminal in front of
the person: whether a stream is a terminal, what kind and how big, whether it
is reached over ssh, and which window a once-only notice belongs to. That
decides how magus talks to them (color, links, hover, which prompt it can
show), never what work it does. The second is reading the input a caller
sent: a flag, an argument, the event a hook was handed. `NO_COLOR`
belongs with them: a person set it to say what they want.

An agent host is a caller like any other, so it says who it is. The hook
configuration `magus describe harness` prints names the host explicitly, and
a hook invoked without that name is refused rather than defaulted to one host.

## Enforced by

- `TestNoEnvironmentSniffing` in [`conventions_test.go`](https://github.com/egladman/magus/blob/main/conventions_test.go) fails the build when
  code reads a CI, hosting or agent-host variable outside a short list of
  inputs read after the caller chose them, and the failure says to take a flag
  instead.

## Good

The workflow says what it wants, in the file a reader is already looking at:

```yaml
- run: magus affected ci --plan -o json
```

## Bad

```go
if runningInCI() {
    opts.NoCache = true
}
```

`runningInCI` guesses from the environment. Nobody wrote this setting, nobody
can see it from the workflow, and a laptop whose test harness exports the
variable it reads gets it too.

## See also

- [No silent defaults](no-silent-defaults.md)
- [Provider I/O is Buzz](provider-io-is-buzz.md)
