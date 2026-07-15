---
title: Editor setup
description: Wire your editor to the magus language server (magus buzz lsp) for completion, hover, and signature help in magusfiles and spells.
tags: [editor, lsp, language-server, completion, hover, neovim, vscode, helix]
---

# Editor setup

magus ships a language server for the Buzz files you author: magusfiles and
spells. `magus buzz lsp` speaks the Language Server Protocol over stdio, so any editor
with a generic LSP client can offer, for a `*.buzz` file:

- **Completion** - module names on `import "..."`, module members after a `.`
  (`fs.`, `os.`, `charm.`), and bare identifiers, each with its signature and doc.
- **Hover** - the signature and documentation of the symbol under the cursor.
- **Signature help** - the callee's parameter list while you type inside a call.

The analysis is the same engine the [interactive playground](playground.html)
uses; `magus buzz lsp` is just the transport that hands it to your editor. It reads the
document text the editor sends and needs no workspace, config, or daemon.

## Prerequisites

- `magus` on your `PATH` (see the [Download guide](download.md)). Confirm with
  `magus version`.
- Confirm the server starts (it waits for LSP input, so this just checks the
  subcommand resolves):

  ```sh
  echo "" | magus buzz lsp    # exits immediately on empty input; no error means it is wired
  ```

The server is registered for the `buzz` language and the `.buzz` file extension.
A magusfile is `magusfile.buzz`; a spell is `spells/<name>/spell.buzz`.

## Neovim

Using the built-in LSP client (Neovim 0.8+), no plugin required. Add to your
config:

```lua
vim.filetype.add({ extension = { buzz = "buzz" } })

vim.api.nvim_create_autocmd("FileType", {
  pattern = "buzz",
  callback = function(args)
    vim.lsp.start({
      name = "magus",
      cmd = { "magus", "buzz", "lsp" },
      root_dir = vim.fs.root(args.buf, { "magus.yaml", "magusfile.buzz", ".git" }),
    })
  end,
})
```

Completion is then available through `vim.lsp.completion` (or your completion
plugin), `K` triggers hover, and signature help fires inside a call.

## Helix

Helix has a built-in LSP client. Add to `~/.config/helix/languages.toml`:

```toml
[language-server.magus]
command = "magus"
args = ["buzz", "lsp"]

[[language]]
name = "buzz"
scope = "source.buzz"
file-types = ["buzz"]
roots = ["magus.yaml", "magusfile.buzz"]
language-servers = ["magus"]
```

Restart Helix; open a `.buzz` file and completion, hover (`space k`), and
signature help work immediately.

## VS Code

VS Code has no built-in generic LSP client, so a thin extension is the usual
route: register the `buzz` language for `.buzz` files and start
`magus buzz lsp` as a `LanguageClient` with a stdio transport. The essential glue in an
extension's `activate`:

```ts
import { LanguageClient, TransportKind } from "vscode-languageclient/node";

const client = new LanguageClient(
  "magus",
  "magus language server",
  { command: "magus", args: ["buzz", "lsp"], transport: TransportKind.stdio },
  { documentSelector: [{ scheme: "file", language: "buzz" }] },
);
client.start();
```

Pair it with a `languages` contribution in `package.json` mapping the `.buzz`
extension to a `buzz` language id. Any editor that speaks generic LSP (Sublime via
LSP, Emacs via `eglot`/`lsp-mode`, Zed via an extension) wires up the same way:
run `magus buzz lsp`, stdio transport, `buzz` documents.

## What it does not do (yet)

The server is scoped to the three edit-time features above. It does not publish
diagnostics, format on save, or resolve cross-file go-to-definition. For those,
reach for the run-time surfaces: `magus doctor` for workspace health, `magus
describe` to preview a resolved target or command, and `magus buzz -t` to run a
spell's test blocks (see [spells.md](spells.md)).

## See also

- [download.md](download.md): install and update the `magus` binary.
- [spells.md](spells.md): authoring spells, and testing them with `magus buzz -t`.
- [targets.md](targets.md): the magusfile targets the server completes.
- [playground.html](playground.html): the same analysis engine, in the browser.
