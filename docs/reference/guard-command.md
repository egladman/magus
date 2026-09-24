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

Each entry of `commands`:

| field     | what it is                                                                                   |
| --------- | -------------------------------------------------------------------------------------------- |
| `program` | the program's base name                                                                      |
| `args`    | its arguments as the shell would pass them; a variable or substitution renders empty         |
| `repeats` | true inside a `while` or `until` loop, which reruns it until a condition changes; not `for`  |

`commands` is what the built-in rules read: the line is parsed rather than matched, and
wrappers (`env`, `timeout`, `nohup`, `sh -c`, `eval`, ...) are peeled to the program
they run, so `GH_TOKEN=x timeout 60 gh pr checks 1` arrives as one `gh` entry. Match on
it rather than on `command`: a pattern over the raw line cannot tell a pipe from one
inside quotes.

`magus\guard.once(key)`, `magus\guard.count(key)` and `magus\job\list()` work here as
they do in a [spawn rule](guard-spawn.md), and share its store: a key means one thing to
the whole policy.

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

When a `.buzz` file differs from the checked-out commit, the rule runs from both the
working tree and the committed sources, files it imports included, and the stricter
answer stands, as for [`magus\guard.spawn`](guard-spawn.md#tighten-live-loosen-on-approval).
An agent's edit can tighten the policy that grades it and cannot loosen it. Evaluating
the committed side loads the root magusfile a second time on every command while such
an edit is pending; running out of time there denies the command.

## Writing a repository policy

Keep the rules in their own file, import it from the root magusfile, and register one
function. magus's own repository does this in
[`tools/policy/guard.buzz`](https://github.com/egladman/magus/blob/main/tools/policy/guard.buzz):

```buzz
import "./tools/policy/guard" as agentpolicy;
magus\guard.command(agentpolicy\judge);
```

It holds two rules magus does not ship, because they are that repository's preference:

- **Deny waiting on CI.** `gh run watch`, the `--watch` form of `gh pr checks` and
  `gh run view`, and a CI or merge-queue read that a `while` loop or `watch` repeats are
  refused with the one command that answers every open pull request.
- **Advise batching, once per session.** Reading one pull request's checks is advised to
  ask for the whole board; reading the merge queue's runs or labels through GitHub is
  advised to ask `magus queue ls`.

Three habits keep a policy like it maintainable:

- **Test the rule without a guard session.** The file's `judge` copies the request into
  a local record and calls `decide`, which takes the once-per-session gate as an
  argument, so in-file `test` blocks run under `magus buzz -t --embedded` with a fake
  gate. A test cannot build the host's `CommandRequest`, so keep the logic on your own
  records.
- **Deny only what you can prove.** A deny that misfires stops an agent doing its job;
  an advisory that misfires is noise. Key a deny on the parsed program and its operands,
  never on prose.
- **Say what to do instead.** The reason is the agent's next command, so lead with it.
