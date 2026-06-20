# magus-describe

Explain why a project is in the affected set

## Synopsis

**magus** describe \<project\> [flags]

## Description

Show the changed files and dependency chains that cause a given project
to appear in the affected set computed by magus affected.

Each path section shows the seed project whose files changed, the chain of
dependency edges leading from that seed to the target, and the list of changed
files under the seed.

## Examples

*Describe why api/gateway is affected*

```
magus describe api/gateway
```

*JSON output*

```
magus describe api/gateway -o json
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

