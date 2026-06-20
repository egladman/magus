# magus-doctor

Validate the workspace

## Synopsis

**magus** doctor [flags]

## Description

Run a suite of diagnostic checks against the workspace and report the
results. Checks include:

- Project discoverability and language coverage
  - Dependency graph cycles
  - Required tools on PATH
  - Recognised MAGUS_\* environment variables (typo detection)
  - Magusfile form consistency
  - Binary provenance (signature and git-tree freshness)

Exits non-zero if any check fails. Warnings are informational and do not
affect the exit code.

## Options

**--fix**
: Apply fixable remediation in-place

**--strict**
: Exit non-zero on warnings as well as failures

## Examples

*Run all checks*

```
magus doctor
```

*JSON report*

```
magus doctor -o json
```

*Apply fixable remediation in-place*

```
magus doctor --fix
```

*Fail on warnings (useful in CI)*

```
magus doctor --strict
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

