# analyzer

The record every analyzer is declared with, the finding it reports, the runner that
applies a workspace's plan, and the reader for each project's `analyzers.yaml`. None of it
is in the magus binary: a workspace imports it.

| file | holds |
| --- | --- |
| `analyzer.buzz` | `Analyzer`, its `Tier` and `Reach`, `Input`, `Plan`, `Walk`, `check` and `declined` |
| `finding.buzz` | `Finding` and `render` |
| `runner.buzz` | `lint`, the walk, selection by path or name, and the firings a run records |
| `policy.buzz` | `parse` and `read` for `analyzers.yaml`, and `owner` |
| `adapter.buzz` | `rule`, which declares a rule written to the `magus/lint` shape |

## A workspace's plan

`analyzers.buzz` at the workspace root lists what runs and calls the runner:

```buzz
import "os";
import "libs/analyzer/analyzer" as analyzer;
import "libs/analyzer/runner" as runner;

fun main(args: [str]) > void !> any {
    final code = runner\lint(args, plan: analyzer\Plan{ own = [...], walk = analyzer\Walk{} });
    if (code.error -> e) {
        throw e.message;
    }
    if (code.value! != 0) {
        os\exit(code.value!);
    }
}
```

Import the library's modules under an alias. A plain file import merges their names
into the importer, and `Finding` would then shadow the one `magus` and `magus/lint`
declare.

`magus buzz analyzers.buzz -- <path>...` keeps the findings at those files, and
`-- --only <name>` runs one analyzer.

## What decides a finding

1. A plan that lists an analyzer twice, or both runs and declines it, is a finding.
2. A glob in an analyzer's `files` that matches no file is a finding, and the analyzer
   does not run.
3. The analyzer's `check` runs once per project, or once over the workspace when its
   reach is `workspace`. It gets that project's `settings` block.
4. A finding with no message, or with no fix from it or its analyzer, is refused.
5. A finding of a `judgment` analyzer prints as advice and never fails the run.

## `analyzers.yaml`

One top-level key per analyzer, each holding `settings`, `baseline` and `exits`, plus
`zones`. A person types it. A project with none gets the strict defaults. The runner hands
an analyzer its `settings`; each analyzer still reads its own `baseline` and `exits` rows.

## Catalog

The library ships no analyzer yet. This repository's rules live in `hack/lint` and run
through the adapter.
