---
name: idiomatic-pr-descriptions
description: "Write pull request descriptions and review comments that lead with intent and sound like their author. Use before gh pr create, gh pr edit, gh pr comment, gh pr review or glab mr create. Use when drafting a PR body or review comment for someone to paste. Proofread runs the mechanical checks; this covers judgment."
metadata:
  source: workspace
---

# Pull request descriptions and review comments

What this produces is a draft. If you're the author, tweak it and put it in your own
words before you post it. A reader should hear you, not a template.

## Run proofread first

```bash
printf '%s\n%s\n' "$TITLE" "$BODY" | proofread change-description
printf '%s\n' "$REPLY" | proofread review-reply
```

Fix every `deny`. Read each `advise` and decide. Proofread catches the patterns it can
count, so the rest of this page is what it can't.

## Description shape

Open with intent, no heading: a sentence or two on what you set out to do and why.

Then what you implemented, one bullet per change, present tense, the code as subject.
"`Evict` drops the oldest entry first."

Put the evidence on the bullet's own line: an output ref, a test name or a path. A
bullet you can't back that way is a task, not a claim.

Considerations is optional and holds three bullets at most: the trade-off taken, a
constraint, the alternative set aside, a risk. Never a reading order ("Start with X");
name a file only with why it matters.

When this change lays groundwork, a `## Later` section names the follow-up pull requests,
one line each. It's the one section that may speak in the future tense, and it holds no
tasks.

Add `## Tested` only when someone checked the change by hand, beyond the test targets.
List the commands they ran in a fenced block, and what they saw. Leave it out otherwise;
it is never a default section.

End with `## Next` when work is left: `- [ ]` tasks and nothing else, one per thing not
done or not checked. A person or an agent ticks them without touching the prose.

The lead is the one place for an opinion ("I'd rather pin it now"). Your own words carry
your voice, so draft the lead from what you said about the change.

Keep it short. A reviewer reads the diff anyway, so don't restate it. Say what the diff
can't: why, and what's missing.

After the description, load self-review-comments to draft the inline comments the author
may leave on their own draft.

## Voice

Write the way you'd explain it to a teammate at your desk, in plain words, and be candid
about what's rough. Contractions are fine. Watch for these tells:

- lists of exactly three
- stacked colons in one sentence
- "This PR"
- "not X but Y" framing
- signposts like "Honestly," or "The thing is"
- em dashes
- puffery ("robust", "seamless", "significantly")
- bullets that repeat the diff line by line

A claim covers what you measured. If you ran it, give the number. If you didn't, make it
a task under Next instead of writing "should fix".

## Review comments

Name what you expected against what the code does, then ask. "The doc says `Close`
is idempotent; a second call here panics. Can it return nil?" If you don't know why
something is the way it is, ask.

When you reply to a requested change, say whether you made it.

| Kind               | Words |
| ------------------ | ----: |
| Description intent |    60 |
| Inline comment     |    60 |
| Reply              |    80 |
| Review summary     |   100 |

## Before you post

Read it as someone outside the team would, say a future employer. Take out private
names, internal jokes, profanity, workplace references, how the session went, and any
credit to the tool that helped write it.
