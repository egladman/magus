# magus-churn

Rank projects by recent edit frequency (churn)

## Synopsis

**magus** churn [flags]

## Description

Rank projects by how often recent commits touched them — a proxy for
where a codebase's attention and risk concentrate — joined with each project's
author count (bus factor), recency, and blast radius (how many projects a change
here would affect).

The scan is contextual to the working directory: run from inside a subtree and
churn reflects only that subtree's history. The active VCS adapter must be able
to report per-commit files (git can); when it cannot, or VCS is disabled, churn
reports an error rather than guessing.

--since bounds the window by commit date (90d, 12w, 6mo, 1y). --files ranks
individual files instead of projects. --coupling reports projects that change
together (temporal coupling) — modules that co-evolve whether or not a dependency
edge connects them.

Text output is a ranked table. -o mermaid draws the dependency graph heat-
coloured by churn (or, with --coupling, the co-change graph); -o json/yaml emit
the structured nodes, files, and pairs.

## Options

**--commits** *int* (default: 500)
: Cap on how many recent commits to scan

**--coupling**
: Report projects that change together (temporal coupling)

**--files**
: Rank individual files instead of projects

**--since** *string*
: Only commits within this window (e.g. 90d, 12w, 6mo, 1y)

## Examples

*Rank the whole workspace by churn*

```
magus churn
```

*Only the last quarter*

```
magus churn --since 90d
```

*Find the hottest individual files*

```
magus churn --files
```

*Projects that change together*

```
magus churn --coupling
```

*Heatmap as Mermaid*

```
magus churn -o mermaid
```

*Churn of the subtree you're in*

```
cd web/studio && magus churn
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

