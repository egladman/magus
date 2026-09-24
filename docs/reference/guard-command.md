---
title: magus\guard.command
description: Register one Buzz function the agent guard calls on every agent shell command, to add a deny or an advisory from your repository's own rules. magus ships the seam and no rules.
tags: [guard, agents, shell, commands, policy, magusfile, hooks, strengthen-only]
---

# magus\guard.command

`magus\guard.command` is the function form of
[`magus\guard.shell`](../guides/integrations/agents/guard.md), for a rule a program and
an argument list cannot express. The root magusfile registers one function; the agent
guard calls it on every shell command an agent is about to run, after its built-in
rules, hands it the parsed command, and adds its answer to the built-in verdict.

**magus ships no command rule.** How your repository waits on CI, which tools an agent
may reach for, and what it should be told instead are your decisions. The seam exists
so they live in your tree, in Buzz, versioned with the code they govern.

```buzz
import "magus";

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    foreach (c in req.commands) {
        if (c.program == "curl" and c.args.indexOf("https://prod.example") != null) {
            return magus\guard.deny("Do not hit prod from an agent shell; use staging.");
        }
    }
    return magus\guard.allow();
});
```

## The request

| field         | what it is                                                                            |
| ------------- | ------------------------------------------------------------------------------------- |
| `host`        | the host the hook wiring named itself as                                              |
| `session`     | the calling session's id                                                              |
| `command`     | the shell line as the host sent it                                                    |
| `description` | the label the caller wrote for the command; empty when its host has none              |
| `commands`    | the programs the line runs (below); empty when the line does not parse                |
| `parent`      | the description the calling subagent was itself spawned with; empty for a root caller |
| `role`        | `worker` when a lease binds the calling session in this checkout, else `root`         |
| `lease`       | the bound job row for a worker, null for root                                         |
| `checkout`    | for a line that runs `git push` only: the checkout's state (below); null otherwise    |
| `dir`         | the directory the line runs in, as the host reported it; empty when it reported none  |
| `workspace`   | the root of the workspace holding `dir`, empty when none does                         |

`checkout` is read in the directory the push runs in (after any `-C`), through the
version control that resolves there, and only for a push, since it costs processes:
`branch` is the branch the checkout is on, empty when it is on none (a detached HEAD),
and `remoteBranches` lists the remotes' branches as `<remote>/<branch>` as of the last
fetch, so a branch never fetched is absent. It is null when that version control cannot
report it, which a rule should read as unknown.

Each entry of `commands`:

| field     | what it is                                                                                  |
| --------- | ------------------------------------------------------------------------------------------- |
| `program` | the program's base name                                                                     |
| `args`    | its arguments as the shell would pass them; a variable or substitution renders empty        |
| `repeats` | true inside a `while` or `until` loop, which reruns it until a condition changes; not `for` |
| `path`    | the program's file when the line names it by a path, made absolute against `dir` (below)    |

`commands` is what the built-in rules read: the line is parsed rather than matched, and
wrappers (`env`, `timeout`, `nohup`, `sh -c`, `eval`, ...) are peeled to the program
they run, so `GH_TOKEN=x timeout 60 gh pr checks 1` arrives as one `gh` entry. Match on
it rather than on `command`: a pattern over the raw line cannot tell a pipe from one
inside quotes.

`path` is empty for a program found on `PATH`, and whenever the file is not known before
the line runs: a word holding a variable or substitution, a relative path after a `cd`
on the same line, a program reparsed out of a `sh -c` payload, or no `dir` to resolve it
against. Symlinks are left as spelled.

`magus\guard.once(key)`, `magus\guard.count(key)` and `magus\job\list()` work here as
they do in a [spawn rule](guard-spawn.md), and share its store: a key means one thing to
the whole policy.

`magus\guard.binary()` describes the magus answering the hook: `path`, the running
executable with symlinks resolved, and `stamp`, the text its build passed through the
linker (`-X github.com/egladman/magus/internal/interp/bindings.buildStamp=<text>`), empty
when it passed none. magus never reads the stamp; a build and a rule agree on its format,
which is how a rule can tell whether the binary judging it was built from the sources in
front of it.

## Strengthen only, fail open, tighten live

The rule is asked only about a command the built-in rules passed or advised on. Its
`deny` blocks the command, its `advise` reaches the agent where no built-in advice
already spoke and is added to one that did, and nothing it returns lifts a built-in
deny. On a command magus itself served as a `next`, its advice stands down and its deny
still holds.

A rule that raises, runs past three seconds, or returns something other than a verdict
judges nothing: the built-ins apply alone and the agent is told once per session that
the workspace rule failed. Registering twice, from a project's magusfile, or with a
non-function stops the workspace load with [MGS1045](codes/magusfile/MGS1045.md).

When a file the root magusfile's load read differs from the checked-out commit, the rule
runs from both the working tree and the committed sources, files it imports included,
and the stricter answer stands, as for
[`magus\guard.spawn`](guard-spawn.md#tighten-live-loosen-on-approval). An agent's edit
can tighten the policy that grades it and cannot loosen it.

## What it costs

The guard reads its rules from the root magusfile alone, not the whole workspace, and
the rules that need the workspace open it only when they apply. On a clean tree a rule
adds one VCS status to a hook call; measured in this repository, a `magus shell` hook
takes about 120ms with the rules registered, against about 1.6s when every call loaded
the workspace. Only while a file the root load read is uncommitted does a call load the
committed root magusfile as well, reading just the changed files through the VCS and the
rest from disk: about 100ms more. The three-second limit on resolving the committed side,
which denies when it runs out, therefore has an order of magnitude to spare.

## File writes: magus\guard.write

`magus\guard.write(fun (req: WriteRequest) > GuardVerdict)` is the same seam for a file
an agent writes through its host's edit tools, after the built-in path rules. It is
strengthen only, fails open, and runs from both sides exactly as the command rule does.

| field                                        | what it is                                                                |
| -------------------------------------------- | ------------------------------------------------------------------------- |
| `path`                                       | the file as the host named it                                             |
| `workspace`                                  | the root of the workspace holding `path`, empty when none does            |
| `content`                                    | a whole-file write's new content; empty for an edit                       |
| `oldText`, `newText`                         | an edit's replaced text and its replacement; empty for a whole-file write |
| `host`, `session`, `parent`, `role`, `lease` | the caller, as on a command request                                       |

The texts are read from the host's payload by shape. An edit shape magus does not read
leaves all three empty, which a rule should take as unknown rather than as an empty file.

## Writing a repository policy

Keep the rules in their own file, import it from the root magusfile, and register one
function per seam. magus's own repository does this in
[`tools/policy/guard.buzz`](https://github.com/egladman/magus/blob/main/tools/policy/guard.buzz):

```buzz
import "./tools/policy/guard" as agentpolicy;
magus\guard.command(agentpolicy\judge);
magus\guard.spawn(agentpolicy\judgeSpawn);
magus\guard.write(agentpolicy\judgeWrite);
```

It holds rules magus does not ship, because they are that repository's preference:

- **Deny waiting on CI.** `gh run watch`, the `--watch` form of `gh pr checks` and
  `gh run view`, and a CI or merge-queue read that a `while` loop or `watch` repeats are
  refused with the one command that answers every open pull request.
- **Advise batching, once per session.** Reading one pull request's checks is advised to
  ask for the whole board; reading the merge queue's runs or labels through GitHub is
  advised to ask `magus queue ls`.
- **Only an integrator gates.** `magus affected ci` and `magus run ci` are denied to a
  subagent (one a lease binds, or one spawned with a title) unless its title's role is
  `integrate`. A person or the orchestrator, which no lease binds and no spawn titled,
  runs it freely, and anyone may run the `--plan` form, which runs nothing.
- **Name a new branch in full from a detached HEAD.** `git push <remote> HEAD:<name>`
  is denied when HEAD is detached and the checkout has no `<remote>/<name>`, since git
  refuses that push; the reason names `HEAD:refs/heads/<name>`.
- **A code-changing worker gets its own checkout.** A spawn titled
  `<parent>/<role> <job>` with a change role (feat, fix, refactor, perf, docs, test,
  chore) and no isolation is denied.
- **Changelog entries are fragments.** Once `changes/unreleased/` exists in the checkout,
  a write that adds a line to CHANGELOG.md's `[Unreleased]` section is denied; a fix to a
  released section is not.
- **Each checkout runs its own binary.** A `magus` named by a path that sits at the root
  of one checkout of magus, run against another, is denied, as is `go -C` into another
  checkout of magus for a verb its targets cover; the bootstrap link into a checkout with
  no binary yet passes. magus run against a workspace that is not a checkout of magus is
  magus used as a tool, and passes.
- **Say when ./magus is stale.** go-build links a digest of each source the guard's
  verdicts and the workspace load come from into the binary; a command judged by a
  ./magus whose sources have since changed content, or by one linked without the stamp,
  is advised once per state to rebuild, with the bootstrap escape when the host
  declarations moved.

Three habits keep a policy like it maintainable:

- **Test the rule without a guard session.** Each `judge` copies the request into local
  records and calls a `decide` function that takes the once-per-session gate and any file
  it would read as arguments, so in-file `test` blocks run under
  `magus buzz -t --embedded`. A test cannot build the host's request objects, so keep the
  logic on your own records.
- **Deny only what you can prove.** A deny that misfires stops an agent doing its job;
  an advisory that misfires is noise. Key a deny on the parsed program and its operands,
  never on prose.
- **Say what to do instead.** The reason is the agent's next command, so lead with it.
