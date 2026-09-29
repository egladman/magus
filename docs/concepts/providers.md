---
title: Providers
description: How a magusfile hands a job to a spell (the cache backend, CI, secrets, review, the project set, agent harnesses, end-of-life data), and the lifecycle provider that tells `magus describe tools` when a toolchain's release cycle ends.
tags:
  [
    provider,
    providers,
    lifecycle,
    end-of-life,
    endoflife-date,
    toolchain,
    spells,
    extension,
  ]
---

# Providers

A provider is a spell a magusfile selects to do one job magus knows nothing about.
magus calls a few reserved function names on it (a contract) and reads what comes
back. Every provider is wired the same way, at the top level of the root magusfile:

```buzz
import "spells/endoflife-date" as eol;
magus\lifecycle.provider(eol);
```

| Provider                             | Selected with              | How many | Contract                                     |
| ------------------------------------ | -------------------------- | -------- | -------------------------------------------- |
| [Remote cache](cache/remote.md)      | `magus\cache.remote`       | one      | `get_artifact`, `put_artifact`               |
| CI                                   | `magus\ci.provider`        | one      | `enabled`, `annotate`, `last_green_run`, ... |
| [Secrets](secrets.md)                | `magus\secret.provider`    | one      | `resolve_secret`                             |
| [Review](review.md)                  | `magus\review.provider`    | one      | `find_review`, `review_threads`, ...         |
| [Workspace](workspace/providers.md)  | `magus\workspace.provider` | many     | `list_projects`                              |
| Agent harness                        | `magus\harness.provider`   | many     | `harness_config`, `harness_skills`, ...      |
| [Lifecycle](#the-lifecycle-provider) | `magus\lifecycle.provider` | one      | `list_lifecycles`                            |

There is one provider where two would disagree, and many where each answers a
separate part. How a failure reads follows what is at stake: a workspace provider
that fails stops the load, because the project set would be wrong, while CI, review
and lifecycle answers are extra information and degrade to "unknown".

## The lifecycle provider

A spell's tool names the product that publishes its release cycles:
`Tool{probe = ..., lifecycle = "go"}`. The go, typescript, python and rust spells
declare `go`, `nodejs`, `python` and `rust`. That name carries no dates. The
lifecycle provider supplies them.

With one wired, `magus describe tools` asks it for every product the workspace's
spells name, in one call, and adds three columns:

```text
lifecycle: endoflife-date, GET https://endoflife.date/api/v1/products/{go,nodejs} (as of 2026-09-24T00:07:56Z)

  PROJECT/TOOL   INSTALLED   WINDOW        DECLARED BY  VERDICT  CYCLE  EOL         SUPPORT
  ./go           v1.26.6     >= 1.26       spell+ws     inside   1.26   -           unannounced
  console/node   v24.19.0    >= 22, < 25   workspace    inside   24     2028-04-30  supported
```

- `cycle` is the longest release line that prefixes the installed version.
- `eol` is that line's end-of-life date.
- `support` is `supported`, `eol` (on or after the date), `unannounced` (upstream
  has named no date) or `unknown` (nothing to place the version with).

The header line names every URL the provider read, so the network use is never
silent. `-o json` carries the same facts as a `lifecycle` object: `provider`,
`state`, `sources`, `as_of` and `fetched_at`. The console's Toolchain tile shows
the same columns, and a script reads them with `magus\tools()`, which returns a
`ToolReport`.

Nothing here fails a build. The gate is the `tools` window a project declares;
end of life is a column in a report and a doctor line.

### When the provider is not asked

`describe tools` stores each answer under the cache directory. The stored answer is
what the other readers use:

| State       | Meaning                                                                    |
| ----------- | -------------------------------------------------------------------------- |
| `live`      | asked on this call                                                         |
| `offline`   | `MAGUS_OFFLINE` is set, so the spell never ran; the stored answer, or none |
| `unreached` | the spell threw or ran past 15 seconds; the stored answer, or none         |
| `unwired`   | no provider is wired; the columns read `-`                                 |
| `cached`    | read from the store without asking (doctor)                                |

With no stored answer, `support` reads `unknown (offline)` or
`unknown (unreached)`. A stored answer is used only while the question is the same:
the same provider, the same products, the same built-in spells and the same
workspace spell sources.

`magus doctor` runs the `toolchain-lifecycle` check against the stored answer and
never fetches. It gives advice when an installed version, or a project's `tools`
floor, sits in a cycle past its end of life. With nothing stored it says so and
names `magus describe tools` as the fix.

A malformed answer is an error that names the spell, the product and the field:
an answer for a product nobody asked for, a missing `source`, an `asOf` that is not
RFC 3339, or a date that is not `YYYY-MM-DD`.

### Using endoflife.date in another workspace

The provider this repository wires is `spells/endoflife-date`. It is not compiled
into the magus binary: it imports `http`, and the binary carries no release feed
(see [Scope](../scope.md)). Until remote spells publish, another workspace uses it
by copying it:

1. Copy
   [`spells/endoflife-date/spell.buzz`](https://github.com/egladman/magus/blob/main/spells/endoflife-date/spell.buzz)
   to `spells/endoflife-date/spell.buzz` in your workspace.
2. Wire it in the root magusfile:

   ```buzz
   import "spells/endoflife-date" as eol;
   magus\lifecycle.provider(eol);
   ```

3. Run `magus describe tools`.

The copy is yours to read and change. It makes one GET per product to
`https://endoflife.date/api/v1/products/<product>`, with no credential.

### Writing your own

A lifecycle provider exports one function:

```buzz
import "magus/spell";

export fun mgs_getName() > str { return "my-lifecycles"; }

export fun list_lifecycles(target: Target, cb: fun(any)) > [Lifecycle] !> any {
    final io = {<str: any>};
    cb(io); // io["keys"] is every product the workspace's spells name
    return [
        Lifecycle{
            key = "go",
            source = "https://example.internal/lifecycles/go.json",
            asOf = "2026-09-24T07:44:41Z", // upstream's last change, never the clock
            cycles = [ReleaseCycle{cycle = "1.26", released = "2026-02-10", eol = "", lts = false, latest = "1.26.6"}],
        },
    ];
}
```

Answer only the keys you were asked for, and leave out a product you do not know.
Throw when the source cannot be reached: magus reads that as `unreached`, never as
a failure. A workspace wires one lifecycle provider, because a product name has one
meaning; wiring a second is an error.
