# magus-run

Run a target for selected projects

## Synopsis

**magus** run \<target\> [flags] [project...]

## Description

Run a named target for the selected projects. With no project
arguments, selects the project containing the current directory, or all projects
if the current directory is not inside a project. Explicit project paths on the
command line select exactly those projects.

The target ci is an ordinary magusfile-defined target — magus does not hardcode
its steps; your magusfile composes them with magus.needs. magus keeps ci as
the anchor that the affected set keys off, and always runs it read-only; apply
the rw charm (e.g. 'magus run format:rw') to mutate files.

## Options

**--depth** *int*
: With --graph: cap displayed depth (0 = unlimited)

**--dry-run**
: Print what would run without executing

**--graph**
: Render the dependency graph for the selected scope instead of executing

**--upstream**
: With --graph: show dependents instead of dependencies

## Targets

**ls**
: Print selected projects without executing anything

**build**
: Build selected projects

**test**
: Test selected projects

**lint**
: Lint selected projects (read-only)

**format**
: Format source files in selected projects

**clean**
: Remove build artefacts from selected projects

**generate**
: Run code generation for selected projects

**ci**
: Run the magusfile's ci target read-only (affected-set anchor)

## Examples

*Build everything*

```
magus run build
```

*Test one project*

```
magus run test api/gateway
```

*Build two specific projects*

```
magus run build api/gateway web/studio
```

*Dry-run: show what would run*

```
magus run build --dry-run
```

*Full CI pipeline*

```
magus run ci
```

*Show dependency graph for build target*

```
magus run build --graph
```

*Graph in Mermaid format*

```
magus run build --graph -o mermaid
```

*Graph dependents of api/gateway*

```
magus run build api/gateway --graph --upstream
```

*Stream JSONL target events to a file*

```
magus run build -o jsonl --tee build.jsonl
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

