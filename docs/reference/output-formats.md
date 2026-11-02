---
title: Output formats
description: The -o formats every magus command with a record takes, how a Go template reads that record, and what happens on a command with no record.
tags: [cli, output, json, yaml, jsonl, template, go template, scripting]
---

# Output formats

Every magus command that prints a record takes the same `-o` values:

| `-o`                     | prints                                                               |
| ------------------------ | -------------------------------------------------------------------- |
| `text` (the default)     | the human form, which is allowed to change                           |
| `json`                   | the record, indented                                                 |
| `yaml`                   | the record as YAML                                                   |
| `jsonl`                  | the record's list, one JSON value per line                           |
| `name`                   | each entry's identifier, one per line                                |
| `template=<go-template>` | the record through a [Go template](https://pkg.go.dev/text/template) |
| `template`               | the fields a template can read, with their types                     |

A few commands add a format of their own, such as `-o dot` on `graph deps`. A format never
falls back to another one.

A command that reports an outcome, such as `server stop` or `config set`, prints it as a
record: what it did, where, and how much. Under `--dry-run` the record says what would
happen.

A command whose stdout is not a record refuses `-o` with exit 2 before it does anything,
wherever the flag is written. These are the ones whose stdout is a document or a stream
(`completion`, `agent starter`, `config cache export`, `queue plan`, `watch`), a
server that runs until stopped (`server start`, `broker`, `mcp`), or a secret that `--tee`
would copy into a file (`config token print`). `magus <command> -o json` tells you which
kind you have: it either prints a record or exits 2.

A few commands take `-o` only in some forms. `graph pull` needs `--out`, since stdout is the
graph otherwise. `agent install --tar`, `init --dry-run` and `server stop --pools` refuse it.

`--tee <file>` also writes this invocation's structured output to a file, replacing
previous contents on the first write. Streaming output keeps every record from the
current invocation.
`-s` with `--tee` leaves stdout empty.

## Templates

A template runs against the record `-o json` prints, so its fields are the JSON names:
`{{.file_count}}`, never `{{.FileCount}}`. Numbers arrive as floats, and `printf "%.0f"`
prints one as an integer. A field the record does not hold is an error that lists the fields
it does hold. A bare `-o template` lists them for any command:

```sh
magus refs Open -o template
```

The body is ordinary `text/template`, with `range`, `if`, `else`, `with` and pipelines, plus
the [Sprig](https://masterminds.github.io/sprig/) functions that give the same answer on
every run: no clock, no environment and no cryptography. `join` takes the list first,
`{{join .names ","}}`.

This prints each file that references a symbol, and flags the ones that use it most:

```sh
magus refs Open -o template='{{range .refs}}{{if gt .count 3.0}}HOT {{end}}{{.file}}: {{printf "%.0f" .count}}
{{else}}no references
{{end}}'
```

## Exit status

A format changes how an answer prints, never what it means: a command exits as it does under
`text`, and its diagnostics stay on stderr, so stdout parses. Prefer a format to a pipe into a
text filter, since a pipeline takes its status from the last stage.

A command that streams (`events`, `run -o jsonl`) renders each record as it arrives.
