# large-monorepo dependency graph

The project dependency graph magus sees for the benchmark workspace: 5 Next.js
apps (`apps/*`), each with a `build` target depending on its 20 feature
libraries (`packages/<app>/important-feature-*`, bound to the `tslib` spell with
a no-op `build`). The `packages/shared/*` packages are leaf nodes: no app's
`package.json` lists them, so, like every other tool, magus sees no edge to them.

This is the same set of nodes and edges turbo/nx/lage derive from `package.json`;
see `README.md` ("How magus is wired") for why the edges are declared twice
(`depends_on` for affected, `magus.needs` for ordering + caching).

`setup.sh` adds four more nodes - `packages/platform/{logging,config,http,metrics}` -
plus an edge from each of the ten feature libraries listed in `enrich/bridges.tsv` to
the one platform package its bridge module imports; see README.md ("The enrichment,
and who it is for"). Reach that part of the graph with `magus explain
packages/platform/config`.

Print the graph (after `./setup.sh`):

```sh
cd gen/repo && magus graph deps -o dot
```
