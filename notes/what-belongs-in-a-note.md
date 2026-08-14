---
magus:
  id: what-belongs-in-a-note
  title: What belongs in a note, and what belongs in memory
  tags:
    - conventions
    - knowledge
  anchors:
    - kind: project
      target: .
---

Two stores, and the difference is provenance rather than importance.

A note is the only node class the graph does not derive from the workspace. A doc comes
from markdown, a rationale from a comment, a symbol from an index, an author from git -
delete the graph, rebuild, and every one of them comes back. A note's content originates
with a person, nothing in the repository corroborates it later, and no rebuild recovers it.
That is the whole reason agents may read notes and never write them: a note of uncertain
authorship is not a weaker note, it is a worthless one.

So the test for what goes here is not "is this important" but "would anyone be able to
check it later". Write a note when the reason lives in someone's head: why an approach was
rejected, what a constraint really is, what bit us and is not visible in the code that
resulted. Reach for `magus memory put` instead when an agent derived the claim and can cite
a ref a later reader re-runs - that store exists precisely so a derived claim never has to
pretend to be a human one.

Anchor as narrowly as the knowledge allows, and expect the anchor to be checked. `magus
notes verify` reports a note whose subject was renamed or deleted, and separately one whose
subject still exists but has quietly stopped meaning what the note says. Nothing re-records
that for you; clearing the flag is a person re-reading the prose against the code.

**Provenance of this entry:** drafted by an agent at Eli's explicit direction, to seed an
empty store, and committed under his name. It is the one note here that does not meet the
bar the rest of this file describes, and it is recorded rather than hidden so the store's
first entry is not a silent exception to its own rule.
