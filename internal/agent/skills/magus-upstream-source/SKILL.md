{{if .Full}}# Reading magus's own source at your build{{else}}# magus source at your build{{end}}

{{if .Full}}This is a last resort. Assume the workspace is wrong before you assume magus is:
most behavior that looks like a magus bug is a magusfile, a config key or an
input that magus rejects as documented. Reading magus's code is how you find
the line that rejects it, not a hunt for upstream defects.{{else}}Last resort; assume workspace wrong before magus.{{end}}

{{if .Full}}Read magus's source only when both hold:
{{else}}Read magus source only when both hold:{{end}}
{{if .Full}}- magus crashed (a Go panic and trace), or the {{skill "upstream-docs"}} skill could
  not reconcile the docs with what the binary does.{{else}}- magus crashed (Go panic, trace), or {{skill "upstream-docs"}} could not reconcile docs with binary.{{end}}
{{if .Full}}- You have a reproduction: one command and the output that shows the problem.{{else}}- Reproduction: one command, output showing problem.{{end}}

{{if .Full}}## Pin the source to the binary{{else}}## Pin source to binary{{end}}

```sh
{{cmd "version"}} -o json
```
{{if .Full}}
Read `commit`, `dirty` and `repository`. Use the full `commit`: a forge
refuses a shallow fetch by an abbreviated revision.
{{end}}
{{if .Full}}Stop when `dirty` is true or `commit` is `unknown`. That build's source exists
nowhere you can fetch, so say so and work from the reproduction alone.

Never read main or the latest release instead. Another version explains
behavior your binary lacks, and it reads as authoritative while doing
it.{{else}}Read `commit`, `dirty`, `repository`; use full `commit`.
Stop when `dirty` true or `commit` is `unknown`: source unfetchable; say so, use reproduction alone.
Never read main or latest release: other version explains behavior binary lacks.{{end}}

{{if .Full}}## Fetch it outside the workspace{{else}}## Fetch outside workspace{{end}}

{{if .Full}}Keep one clone per commit in the user cache. Never clone into the workspace:
it would show up in the workspace's VCS status and in its knowledge graph.{{else}}One clone per commit, in user cache.
Never clone into workspace: VCS status and graph pick it up.{{end}}

```sh
dir="${XDG_CACHE_HOME:-$HOME/.cache}/magus-upstream/<commit>"
git init -q "$dir"
git -C "$dir" remote add origin <repository>
git -C "$dir" fetch --depth 1 origin <commit>
git -C "$dir" checkout -q FETCH_HEAD
```

{{if .Full}}Reuse the directory when it already exists. `couldn't find remote ref` means the
commit was never pushed: a local build of an unpublished commit, whose
source lives only on the machine that built it. Stop, as for a dirty build.{{else}}Reuse directory when it exists.
`couldn't find remote ref` -> commit never pushed -> stop, as dirty.{{end}}

{{if .Full}}## Read it without changing it{{else}}## Read, never change{{end}}

{{if .Full}}Point every command at the clone with `--root "$dir"`, placed before the verb.

- `magus --root "$dir" query "<terms>"` finds doc sections, spells, targets and
  Buzz functions. It needs no build step.
- `magus --root "$dir" refs --text "<pattern>"` searches the clone's files.
- `magus --root "$dir" refs <symbol>` needs a symbol index. Run
  `{{cmd "graph build"}}` with the same `--root` once. It runs each
  language's indexer, so it needs that language's toolchain. When refs
  still answers "unknown, not absent", use `refs --text`.
- Read files with your ordinary file tools.{{else}}Every command: `--root "$dir"` before verb.
- `magus --root "$dir" query "<terms>"`: doc sections, spells, targets, Buzz functions; no build step.
- `magus --root "$dir" refs --text "<pattern>"`: searches files.
- `magus --root "$dir" refs <symbol>`: needs symbol index -> run `{{cmd "graph build"}}` with same `--root` once.
- When refs still answers "unknown, not absent" -> `refs --text`.
- Read files with ordinary tools.{{end}}

{{if .Full}}Never run targets in the clone, build it, test it or edit it. Each of
those executes upstream code on your machine for no gain. The question is
what the code says, and reading answers it.{{else}}Never run targets in clone, build, test or edit it; reading suffices.{{end}}

{{if .Full}}## Report what the code says{{else}}## Report{{end}}

{{if .Full}}Cite each claim as `<repository>/blob/<commit>/<path>#L<line>`.

Conclude one of two things:
{{else}}Cite each claim: `<repository>/blob/<commit>/<path>#L<line>`.
Conclude one:{{end}}
{{if .Full}}- The workspace does X and the code requires Y. Fix the workspace. This is the usual result.{{else}}- Workspace does X, code requires Y -> fix workspace; usual.{{end}}
{{if .Full}}- magus has a defect. Give the reproduction and the code path that produces it.{{else}}- magus defect -> give reproduction, code path.{{end}}

{{if .Full}}Never patch the clone to work around a defect. Never file an issue or post
anything. Hand the finding to the person: whether and where to report
it is their decision, and the reproduction and citations are what they need to
make it.{{else}}Never patch clone around defect.
Never file issue or post anything.
Hand finding to person.{{end}}
