---
title: Run a proofread labeling round
description: Draw a seeded sample of proofread findings, freeze it to a rule commit, have two people label it, adjudicate, score it against the promotion gates, and apply the result to a rule's shipped default.
tags:
  [
    proofread,
    calibration,
    labeling,
    precision,
    wilson,
    krippendorff,
  ]
---

# Run a proofread labeling round

A proofread rule ships advise or deny only when fresh labeled firings support it. A
labeling round is how you get them: you draw firings the rule was never tuned on, fix the
rule's commit before anyone labels, have two labelers judge each firing alone, settle
their disagreements, and score the result. This guide runs one round end to end with
`proofread calibrate round`.

You need the `proofread` binary from this repository
(`magus run proofread-build libs/conventions` writes it to
`libs/conventions/gen/proofread`), `jq`, and a clean checkout of this repository.

## Where the data lives

Round data never goes into git. It sits under `$XDG_CACHE_HOME/proofread/calibration/`
(`~/.cache/proofread/calibration/` when `XDG_CACHE_HOME` is unset):

| Path            | Holds                                                                   |
| --------------- | ----------------------------------------------------------------------- |
| `aidev/`        | the AIDev export the sources are cut from                               |
| `sources/`      | the JSON-lines sources you build in step 1                              |
| `labels/roundN` | one round: candidates, counts, freeze record, guideline, labels, scores |
| `sealed/`       | held-out sets for confirmatory rounds that no tuner may read            |

Git holds what is not third-party text: the guideline at
`libs/conventions/proofread/testdata/guideline.md` and one record of counts and scores
per round under `libs/conventions/proofread/testdata/rounds/`.

The examples below use `C` for the base directory:

```sh
C="${XDG_CACHE_HOME:-$HOME/.cache}/proofread/calibration"
```

## 1. Build the sources

A source is a JSON-lines file with one text per line, in a `text` field, and its kind in
a `kind` field. A review comment posted on a diff line also carries `"anchored": true`,
and a reply its `thread_length`. Cut each source from the export with `jq`:

```sh
mkdir -p "$C/sources"
jq -c '{text: (.title + "\n\n" + (.body // "")), kind: "change-description"}' \
  "$C/aidev/jsonl/pull_requests.jsonl" > "$C/sources/aidev-pr.jsonl"
jq -c '{text: .message, kind: "commit-message"}' \
  "$C/aidev/jsonl/commits.jsonl" > "$C/sources/aidev-commit.jsonl"
jq -c 'select(.user_type == "User") | {text: .body, kind: "review-reply", anchored: true}' \
  "$C/aidev/jsonl/review_comments.jsonl" > "$C/sources/aidev-review-user.jsonl"
```

Keep each source to one population. The per-source gate in step 7 compares sources, so a
file that mixes bots and people hides the worse of the two.

## 2. Draw the sample

1. Pick the rules and a seed before you look at any firing. A seed you change after
   seeing a sample is a second look the freeze cannot account for.
2. Run `round sample` from the checkout, naming every earlier round's candidates as an
   exclusion:

   ```sh
   proofread calibrate round sample \
     -rules claim,absolute,issue-repro,ing-tail,participles \
     -seed 20261101 -per-rule 40 -min-per-source 10 \
     -exclude "$C/labels/candidates.jsonl" \
     -exclude "$C/labels/round2/candidates.jsonl" \
     -exclude "$C/labels/round3/candidates.jsonl" \
     -exclude "$C/labels/round4/candidates.jsonl" \
     -out "$C/labels/round5" \
     aidev-pr="$C/sources/aidev-pr.jsonl" \
     aidev-commit="$C/sources/aidev-commit.jsonl" \
     aidev-review-user="$C/sources/aidev-review-user.jsonl"
   ```

3. Read the table it prints and `counts.json` beside the candidates.

`round sample` judges every text with the named rules set to deny, so a rule that ships
off still fires. Before judging, it drops a text its source already held and replaces
anything that looks like an email, a token or a key with `[REDACTED]`. It then drops a
firing whose text, sentence or context an exclude file or a committed case already
showed, keeps at most two firings of a rule per text, and draws up to `-per-rule` firings
from the seed. A source with at least `-min-per-source` fresh firings gets that many
first, so its own gate can run. `counts.json` records, per rule and per source, the
firings seen, excluded, fresh and taken, with the sha256 of every source file.

A directory that already holds a sample is refused: start a new round directory instead.

## 3. Freeze the round

Freeze before anyone labels:

```sh
proofread calibrate round freeze -dir "$C/labels/round5" \
  -guideline libs/conventions/proofread/testdata/guideline.md \
  -purpose "confirm claim and absolute after the round 4 guideline revisions"
```

`freeze` refuses a tree whose rule files are not committed, rule files that changed since
the sample, and source files whose sha256 differs from the sample's. It writes
`freeze.json`: the rule commit, the sha256 of the running `proofread` binary
(`proofread_sha256`), the proofread version, the seed, the rules, the gates, the
guideline's sha256 and every source's sha256. It copies the guideline beside the
candidates.

The rule commit and `proofread_sha256` identify the sampler. The version is build info
and informative only: magus caches build outputs by input across checkouts, so a cache
hit restores a binary another checkout built, and its stamp names that checkout. A
freeze from before `proofread_sha256` existed lacks it and still loads.

The default gates are the method's: advise needs a Wilson 95% lower bound of 0.70 over
30 or more firings, labels agree at no more than n/10 disagreements or alpha 0.80, and
the worst source with 10 or more firings must also reach 0.70. A rule with any
disagreement that either labeler left without a note fails the agreement gate, whatever
its rate. Override one with
`-gates`, for example `-gates z=2.24` on a rule's second attempt.

## 4. Label

1. Give each labeler `candidates.jsonl`, `guideline.md` and their own copy of the
   template:

   ```sh
   cd "$C/labels/round5"
   cp labels-template.jsonl labels-a.jsonl
   cp labels-template.jsonl labels-b.jsonl
   ```

2. Have each fill one sheet alone, by the guideline: `CORRECT`, `WRONG` or `UNSURE` per
   id, and a note with a cause tag on every `WRONG`.
3. Pick labelers who never tuned the rules being judged. Two labelers from one model
   family make correlated mistakes; agreement then measures the family, not the rule.
4. When both sheets are done, list the ids where they differ, and have each labeler add
   a note to their own row for every one of them, before adjudication. Labelers work
   alone, so this is the first time either sees a disagreement:

   ```sh
   jq -nr --slurpfile a labels-a.jsonl --slurpfile b labels-b.jsonl '
     ($b | map({(.id): (.label | ascii_upcase)}) | add) as $other |
     $a[] | select((.label | ascii_upcase) != $other[.id]) | .id'
   ```

   `score` fails the agreement gate of every rule that has a disagreement where either
   sheet's note is empty.

## 5. Adjudicate

Have someone who neither tuned the rules nor labeled this round write
`adjudicated.jsonl`: one row `{id, label, reason, adjudicated}` per candidate, deciding
each disagreement by the guideline. Every place the guideline left the call open becomes
a revision of `guideline.md` for the next round, never of this one.

## 6. Score

```sh
proofread calibrate round score -dir "$C/labels/round5"
```

`score` refuses a round with no freeze record, a round whose rule files changed since its
rule commit, and a guideline that changed since the freeze. It also refuses an
unlabeled candidate and a disagreement with no adjudicated row. It writes `scores.json`
and prints a table: per rule and per source, the firings, the correct count, precision,
the Wilson lower bound, disagreements, those missing a note, Krippendorff's alpha and
each gate. `scores.json` lists the ids of the disagreements missing a note as
`unnoted_ids`, so you can send them back to the labelers; any such id fails the rule's
agreement gate until both sheets carry a note. A rule that passes advise lists 10 agreed-correct ids to audit by hand before
you promote it.

## 7. Apply the result

1. Audit the spot-check ids of each rule that passed advise.
2. In `libs/conventions/proofread/evidence.go`, set each scored rule's `evidenceCeiling`
   entry to what the round supports, citing the round, the correct count over n, and the
   lower bound in its comment. A rule whose evidence supports its declared default leaves
   the table.
3. Deny needs two frozen rounds on different sources, pooled to 60 firings with at most
   one wrong. One round never promotes a rule to deny. Pool two scored rounds:

   ```sh
   proofread calibrate round pool -dir "$C/labels/round5" -dir "$C/labels/round6"
   ```

   `pool` scores each round the way `score` does, so it refuses a round that is not
   frozen or whose rule files or guideline changed since the freeze. It also refuses two
   rounds that share a source name or a source sha256, and two freezes with different
   deny gates or Wilson z. Per rule it prints, as JSON, the pooled firings, wrong count,
   precision, Wilson lower bound and `deny_gate`, true when the pooled firings reach the
   gate's 60, wrong stays within 1, and both rounds passed their own agreement gate. A
   rule that fired in only one round gets a null `deny_gate`. Each round's agreement gate
   and unnoted count ride along per rule, and `why` names whatever fell short.
   Pass `-out FILE` to write the JSON there and print a table instead. `pool` never
   writes into a round directory, and refuses an `-out` inside one.

## 8. Record the round

Commit a record of counts and scores, with no text, as `round<N>.json` in
`libs/conventions/proofread/testdata/rounds/`, N the round's number:

```sh
cd "$C/labels/round5"
jq -n --slurpfile f freeze.json --slurpfile c counts.json --slurpfile s scores.json '
  $f[0] as $f | $c[0] as $c | $s[0] as $s |
  {round: $f.round, date: $f.frozen, frozen: true, purpose: $f.purpose,
   rule_commit: $f.rule_commit, seed: $f.sampler_seed, candidates: $c.total_candidates,
   sources: ($c.sources | map({key: .name, value: {texts: .texts, sha256: .sha256}}) | from_entries),
   gates: $f.gates, promoted: [],
   rules: ($c.rules | with_entries(.value = {code: .value.code,
     counts: {taken: .value.candidates, seen: .value.firings_seen_all_sources,
              excluded: .value.excluded_already_used, fresh: .value.fresh_unique_in_pool,
              by_source_seen: .value.by_source_firings, by_source_taken: .value.by_source_selected},
     score: $s.rules[.key]}))}'
```

Fill `promoted` with the rules you promoted. `TestRoundRecordsCarryNoText` fails a record
with a field that could hold a candidate's text, label or note.

## What is sealed, and why

A round is evidence only while nobody tuned a rule on its texts. Once a tuner sees a
round's text or labels, or the round decides a promotion, it is spent: it can still show
a rule's failures, but it never again supports a promotion.

- The guard denies any shell line that reads, lists, copies or searches `sealed/`. Keep a
  held-out set there while it waits for a confirmatory round.
- Nothing guards `labels/`. Keep the session that tunes the rules out of a round's
  directory until the round is scored.
- `score` voids a round whose rule files changed after the freeze, since labels of the
  old rule say nothing about the new one.

## Reproduce a past round

Each record under `libs/conventions/proofread/testdata/rounds/` names its rule commit, its seed and its sources, or
says `unrecorded` where the round kept no such fact.

1. Check out the rule commit in a worktree and build `proofread` there.
2. Re-score the labels. For a round scored after its rules moved on, `-replay` skips the
   void check and marks the result as deciding nothing:

   ```sh
   cp -R "$C/labels/round4" /tmp/round4
   proofread calibrate round score -dir /tmp/round4 -replay -out /tmp/round4/scores-replay.json
   ```

3. For a round drawn with `round sample`, re-draw it at the rule commit with the seed,
   rules, flags and sources that `counts.json` records, into a fresh directory, and compare
   its `candidates.jsonl` with the original. The sha256 of each source in the record tells
   you whether you hold the same data.

Rounds 1 to 4 were drawn by one-off scripts before `round sample` existed, so only their
scores reproduce. Re-scoring round 4 with `-replay` gives its recorded counts and rates. Its
agreement gate now also needs a note on every disagreement, and three of its rules have
one disagreement without one, so `round4.json` records those gates as failing, and
`participles` no longer passes advise. Round 4's freeze records no sources, so it cannot
be pooled.
