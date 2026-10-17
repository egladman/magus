---
title: "bare-lgtm"
description: "A rule that is off by default: it reports a review summary that only approves: \"LGTM\", \"ship it\", \"looks good\" once a decisions table turns it on."
tags: [proofread, rules, bare-lgtm, off]
aliases: [reference/prose/bare-lgtm]
---

# bare-lgtm

A rule that is off by default: it reports a review summary that only approves: "LGTM", "ship it", "looks good" once a decisions table turns it on.

## What it catches

A review summary that only approves: "LGTM", "ship it", "looks good".

## Dimension

`evidence`: a claim says more or less than what was shown. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Ebert et al. (SANER 2019) found confusion can end in an approval given without checking, and Bosu et al. (MSR 2015) found the share of useful comments falls as a change grows. A bare approval says nothing of what the reviewer checked. A small change legitimately takes a bare approval, and the rule cannot see the diff's size, so it advises on any size; a caller that knows the size gates it.

## Default decision

The code is `PRF8034`. The decision depends on the kind of text judged:

| Kind             | Default |
| ---------------- | ------- |
| `review-summary` | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"bare-lgtm": "deny"},
  "paths": {"blog/**": {"bare-lgtm": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "bare-lgtm", "code": "PRF8034", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/bare-lgtm/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
