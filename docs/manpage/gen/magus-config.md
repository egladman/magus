# magus-config

View or update magus configuration

## Synopsis

**magus** config \<view|set|init\> [flags]

## Description

Inspect or modify the magus configuration. Configuration is loaded in
priority order: built-in defaults → user-global file → workspace file →
project-local file → MAGUS_\* environment variables → CLI flags.

The view sub-command prints the effective merged configuration. The set
sub-command writes a key-value pair to the local (or global) config file.
The init sub-command materialises the built-in defaults to a magus.yaml so
they can be edited by hand.

Configuration is stored in magus.yaml (or .magus.yaml). The canonical
locations are the workspace root and $XDG_CONFIG_HOME/magus/.

## Subcommands

**view**
: Print the effective configuration (defaults + file + env)

**set**
: Write a key to the local (or global) config file

**init**
: Materialise built-in defaults to magus.yaml

**cache**
: Manage the build cache (prune --older-than)

## Examples

*Show effective config*

```sh
magus config view
```

*Show config as JSON*

```sh
magus config view -o json
```

*Set cache to read-only*

```sh
magus config set cache.immutable true
```

*Initialise magus.yaml from defaults*

```sh
magus config init
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

