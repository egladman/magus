# Reading magus's own source at your build

This is a last resort. Assume the workspace is wrong before you assume magus is{{if .Full}}:
most behavior that looks like a magus bug is a magusfile, a config key or an
input that magus rejects as documented. Reading magus's code is how you find
the line that rejects it, not a hunt for upstream defects{{end}}.

Read magus's source only when both hold:

- magus crashed (a Go panic and trace), or the {{skill "upstream-docs"}} skill could
  not reconcile the docs with what the binary does.
- You have a reproduction: one command and the output that shows the problem.

## Pin the source to the binary

```sh
{{cmd "version"}} -o json
```

Read `commit`, `dirty` and `repository`. Use the full `commit`{{if .Full}}: a forge
refuses a shallow fetch by an abbreviated revision{{end}}.

Stop when `dirty` is true or `commit` is `unknown`. That build's source exists
nowhere you can fetch, so say so and work from the reproduction alone.

Never read main or the latest release instead. Another version explains
behavior your binary lacks{{if .Full}}, and it reads as authoritative while doing
it{{end}}.

## Fetch it outside the workspace

Keep one clone per commit in the user cache. Never clone into the workspace{{if .Full}}:
it would show up in the workspace's VCS status and in its knowledge graph{{else}},
where its VCS status and graph would pick it up{{end}}.

```sh
dir="${XDG_CACHE_HOME:-$HOME/.cache}/magus-upstream/<commit>"
git init -q "$dir"
git -C "$dir" remote add origin <repository>
git -C "$dir" fetch --depth 1 origin <commit>
git -C "$dir" checkout -q FETCH_HEAD
```

Reuse the directory when it already exists. `couldn't find remote ref` means the
commit was never pushed{{if .Full}}: a local build of an unpublished commit, whose
source lives only on the machine that built it{{end}}. Stop, as for a dirty build.

## Read it without changing it

Point every command at the clone with `--root "$dir"`, placed before the verb.

- `magus --root "$dir" query "<terms>"` finds doc sections, spells, targets and
  Buzz functions. It needs no build step.
- `magus --root "$dir" refs --text "<pattern>"` searches the clone's files.
- `magus --root "$dir" refs <symbol>` needs a symbol index. Run
  `{{cmd "graph build"}}` with the same `--root` once{{if .Full}}. It runs each
  language's indexer, so it needs that language's toolchain{{end}}. When refs
  still answers "unknown, not absent", use `refs --text`.
- Read files with your ordinary file tools.

Never run targets in the clone, build it, test it or edit it{{if .Full}}. Each of
those executes upstream code on your machine for no gain{{end}}. The question is
what the code says, and reading answers it.

## Report what the code says

Cite each claim as `<repository>/blob/<commit>/<path>#L<line>`.

Conclude one of two things:

- The workspace does X and the code requires Y. Fix the workspace. This is the usual result.
- magus has a defect. Give the reproduction and the code path that produces it.

Never patch the clone to work around a defect. Never file an issue or post
anything. Hand the finding to the person{{if .Full}}: whether and where to report
it is their decision, and the reproduction and citations are what they need to
make it{{end}}.
