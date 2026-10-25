---
title: Check writing in a commit hook
description: Judge each commit message with proofread from a git commit-msg hook, a Mercurial pretxncommit hook or a Jujutsu alias, choose what blocks a commit, and count which rules earn their place.
tags:
  [
    proofread,
    commit-message,
    hooks,
    git,
    mercurial,
    jujutsu,
    prose-linting,
  ]
---

# Check writing in a commit hook

`proofread` judges a commit message the way a reviewer reads it, and it can run at the
moment you write one. This guide wires it into the hook your version control system
offers, sets what blocks a commit, and shows how to count which rules help. It needs
only the `proofread` binary: no magus checkout, no network.

## 1. Install proofread

Download the `proofread` archive from the same release as magus and put the binary on
your `PATH`. The [verify guide](setup/verify.md) covers the download and both
checks.

Confirm it runs:

```sh
proofread explain filler
```

## 2. Judge one message by hand

Pipe a message to the `commit-message` kind and read the lines it prints:

```sh
printf 'Update the parser\n\nIt simply works.\n' | proofread commit-message --stdin -o text --minimum 20
```

`-o text` prints one line per finding, `path:line:col: CODE rule [decision]
message`, and a count on stderr when there are findings. `--minimum` is the grade the
message must reach; section 6 explains it. A run with neither `--minimum` nor a decisions
table that sets one exits 2 and names the kind.

## 3. Install a git commit-msg hook

1. Create `.git/hooks/commit-msg` with this body:

   ```sh
   #!/bin/sh
   exec proofread commit-message -o text --minimum 20 "$1"
   ```

2. Mark it executable:

   ```sh
   chmod +x .git/hooks/commit-msg
   ```

3. Commit with a message that breaks a rule, and read the output. git passes the path
   of the message file as `$1`. proofread drops the comment lines git adds (each line
   that starts with `#`, and the scissors line with everything after it), so a finding's
   line is its line in the file you edit.

To share the hook with a team, commit it to a directory such as `hooks/` and run
`git config core.hooksPath hooks` in each clone. `git commit --no-verify` skips the
hook, so repeat the check in CI for anything that must hold.

## 4. Install a Mercurial pretxncommit hook

Mercurial has no message file to hand a hook, so read the message back from the
transaction and pipe it to stdin.

1. Add this to `.hg/hgrc`, or to the `hgrc` you share:

   ```ini
   [hooks]
   pretxncommit.proofread = hg log -r $HG_NODE -T '{desc}' | proofread commit-message --stdin -o text --minimum 20
   ```

2. Commit with a message that breaks a rule. A non-zero exit from a `pretxncommit` hook
   rolls the commit back, and the findings print in the terminal.

## 5. Check messages with Jujutsu

Jujutsu has no commit hooks, so run the check where you can place it: in CI over the
messages a change adds, or by hand through an alias.

1. Add an alias to your Jujutsu config:

   ```toml
   [aliases]
   check-message = ["util", "exec", "--", "sh", "-c", "jj log --no-graph -r @ -T description | proofread commit-message --stdin -o text --minimum 20"]
   ```

2. Run `jj check-message` before you push, or call the same pipeline from CI for each
   revision in the range you are about to merge.

## 6. Choose what blocks a commit

proofread exits 1 when a `deny` finding remains, and 0 otherwise. A `deny` finding is
from a rule that is rarely wrong. An `advise` finding fails nothing alone. It lowers the
message's grade instead, out of 100:

```text
grade = 100 - 100 * (sum of the advise findings' weights) / (1 + min(words, 1000) / 100)
```

A finding's `weight` is its rule's measured lower bound on precision, so a rule right 4
times in 5 costs four times one right 1 time in 5. A grade under the minimum is a `grade`
finding, decided `deny`, that names the minimum and the rules that cost the most.

proofread ships no minimum; you pick one. `--minimum 20` lets one finding of the
strongest graded rule through on a message of any length and fails two on a message
under 100 words. A higher minimum is stricter. A decisions file sets one per kind
instead, as `"minimums": {"commit-message": 20}`; pass one or the other.

Exit 2 means proofread could not judge: a bad flag, a missing file, a malformed
decisions table, or no minimum for the kind. A hook that blocks on any non-zero exit
blocks on both. A finding silenced by a `proofread:ignore RULE reason` line in the
message, or covered by a baseline, neither fails nor lowers the grade. To change a rule's
decision for commit messages, pass `--decisions FILE`; each rule has a page in the
reference, such as [filler](../reference/proofread/filler.md).

## 7. Count which rules help

A rule that authors override more than they obey costs more than it saves. Add
`--record` to the hook to keep the evidence:

```sh
exec proofread commit-message -o text --minimum 20 --record "$1"
```

`--record` stores, per message file and rule, the fingerprint of each finding: a hash of
the rule, the matched text and the path. On the next run over the same file, a
fingerprint that disappeared counts as `fixed`, one that a suppression now covers counts
as `suppressed`, and one still present counts as `reported`. Git reuses
`.git/COMMIT_EDITMSG`, so an author who fixes a message and commits again produces a
`fixed`.

Read the counts with `stats`:

```sh
proofread stats -o text
```

Each rule's `not_useful_rate` is one minus precision over its recorded findings:
(suppressed + reported) / (suppressed + reported + fixed), printed beside those three
counts. A rule at or over 10% (precision 90% or less) is flagged `probation`, and one at
or over 25% (precision 75% or less) is flagged `ship-off`: turn it off in the decisions
table, or fix the rule.

Nothing leaves the machine. The counts live in `$XDG_STATE_HOME/proofread/outcomes.json`
(`~/.local/state/proofread/outcomes.json` when the variable is unset), hold fingerprints
and numbers but no message text, and proofread makes no network call. Delete the
directory to start over.

## 8. Judge drafts against your own voice

A voice file records how you write each kind of text: how long your texts run, how
long your sentences are, how much punctuation, code and formatting they carry, and how
often you contract. With it, the `voice-drift` rule advises when a draft measures
outside your own range on two or more of those features, so a draft written with an
assistant still reads as yours. The file holds numbers alone: no text, no word you
chose and no path. It never leaves your machine, and you never commit it.

1. Collect texts you wrote by hand, at least 30 of each kind. Leave out anything an
   assistant drafted, or the file measures the assistant.
2. Build the file from your commit messages:

   ```sh
   git log --author="$(git config user.email)" --format=%B%x00 |
     proofread voice build --stdin --kind commit-message
   ```

   Each text on stdin ends at a NUL byte. To measure files instead, pass one text per
   file: `proofread voice build --kind review-reply reply1.md reply2.md`. For a forge
   export, pass JSON lines with `--field body`, and `--kind-field kind` when each line
   names its own kind.
3. Read the summary on stderr. A kind under 30 texts keeps its counts, and
   `voice-drift` does not judge it.
4. Judge a draft with the file, at the path the summary's `wrote` line names:

   ```sh
   proofread review-reply --stdin --voice <voice file> --minimum 0 -o text < draft.md
   ```

   A `voice-drift` finding names each feature that drifted, whether the draft runs
   above or below your range, and the range itself. It is advice: rewrite the draft the
   way you would have written it.

`proofread voice build` writes to `$XDG_CONFIG_HOME/proofread/voice.json`
(the OS config directory when the variable is unset: `~/.config` on Linux,
`~/Library/Application Support` on macOS), readable by you alone.
It refuses that default path when it falls inside a git work tree, such as a config
directory kept in a dotfiles repository, since a commit there would publish the file.
Name another path with `-o`.

The file measures style, never spelling or casing, and it never suggests an error to
make a draft look hand-typed. Semicolons are not measured, since this repository's
commit subjects join clauses with one by rule. One measurement changes a decision: when
your change descriptions use "I" at 10 or more per 1000 words and "will" at 2 or more,
`tense` advises rather than denies on them, since writing about your own change in the
first person is how you write.

## 9. Judge a coding agent's replies

`proofread agent-reply` judges what a coding agent writes back to you: a reply that opens
on the result, names what changed, and ends without an offer. The template
`proofread-reply.buzz`, in `docs/guides/integrations/agents/` of the magus repository, runs it
from your host's stop event and hands the findings back to the agent as context. It
only advises: it never blocks a stop, prints nothing when proofread is missing, and
judges nothing while the agent is already continuing, so it cannot loop.

1. Install proofread as in step 1, and copy
   `proofread-reply.buzz` into your repository.
2. Wire it to the stop event. In Claude Code, add a `Stop` entry to
   `.claude/settings.json`:

   ```json
   {
     "hooks": {
       "Stop": [
         {
           "hooks": [
             {
               "type": "command",
               "command": "magus buzz -C \"$CLAUDE_PROJECT_DIR\" -s docs/guides/integrations/agents/proofread-reply.buzz -- --agent-name claude-code",
               "timeout": 10
             }
           ]
         }
       ]
     }
   }
   ```

3. Point the hook at your binary when it is not on `PATH`: set `__PROOFREAD_BIN`, or
   append `-- --proofread PATH` to the command.
4. Choose your own decisions. Write a JSON file of `{"rules": {"<rule>": "deny"}}`
   and append `--decisions FILE` after `--`. Keep it to `rules`: a reply is read from
   stdin, and proofread refuses a `paths` glob that matches no file argument.
5. End a turn and read the next one. The agent sees at most five findings, each with
   its line in the reply it just wrote.
