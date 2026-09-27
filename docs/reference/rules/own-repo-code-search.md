---
title: "own-repo-code-search: a GitHub code search scoped to this workspace's own repository, which the graph answers locally"
description: "A deny rule: it refuses a GitHub code search scoped to this workspace's own repository, which the graph answers locally, and names what to run instead."
tags: [guard, rules, own-repo-code-search, deny]
---

# own-repo-code-search

A deny rule: it refuses a GitHub code search scoped to this workspace's own repository, which the graph answers locally, and names what to run instead.

## What it catches

A GitHub code search scoped to this workspace's own repository, which the graph answers locally.

## Why

It is scoped to the workspace's OWN repository: `gh search code` with a `repo:` qualifier or `--repo`/`-R` naming it, or `gh api` against the search/code endpoint with such a query, in any spelling (a URL query, `-f q=`, `-X GET`, `--jq`, a pipe, `bash -c`). The repository is read from the checkout's remote (origin, else the first by name), never hard-coded, and matched without regard to case. GitHub's code index holds only the pushed default branch and lags it, so it never sees this checkout's edits, and it matches text where `magus refs <symbol>` resolves every definition and use, generated and cross-language ones included, and `magus query <text>` relates projects, targets, spells and docs. The deny serves both as remedies, built from the words searched for. Remote code search stays legitimate for code outside this workspace: another repository, GitHub's own docs, or a search naming no repository at all runs as typed and draws nothing. So does every other gh search (`gh search issues`, `gh search prs`) and any other API path, `repos/<owner>/<repo>/contents/...` included. A workspace with no remote a file read can find has nothing to match against and is never judged.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [own-repo-code-search]: ...
```

`magus describe rule own-repo-code-search` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
