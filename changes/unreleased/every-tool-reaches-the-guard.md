### Changed

- **Every tool call reaches the guard.** The Claude Code, Codex and Cursor harnesses wire one
  catch-all `PreToolUse` entry (`*`) to `magus-command.buzz`, and the OpenCode plugin hands
  every tool to `magus shell`. The glue forwards the whole event, and the guard judges a call
  by its tool and the shape of its input: a command, a file read or write, a search, a spawn,
  a skill load, an MCP call. The Edit/Write `magus-path.buzz` entries are gone; a write
  reaches the catch-all whole, content included. Re-run `magus describe harness` and merge.
- **A read is judged the same whichever tool performs it.** magus restates a read tool's
  call as the `cat` or `sed -n` line it stands for, and `read-paging` no longer records a
  refused window as read, so retrying a refused window through another tool is refused too.
- **The agents magus ships run on an exact model id.** A harness spell's `harness_models`
  rows may name the model tier each id runs (`"tier": "economy"`). A workspace that declares
  no tiers gets the shipped ones mapped to those ids, so a spawn's model resolves to a tier
  by equality, and Claude Code's `magus-scout` runs on `claude-haiku-5-5` again. A harness
  that declares tiers and lacks a row for the cheapest one is an error naming the row to add.

### Added

- **The `unjudged-tool` rule** names a call whose input carries nothing any rule reads, from
  a tool the host's tool rows do not list, such as another MCP server's tool. It
  advises once per session by default; set it to `deny` in `magus\guard.builtins` to have
  every call judged. A tool the rows list as touching nothing (a todo list, a plan, a web
  lookup) passes before any rule loads.
- **Each harness spell declares its host's tool rows** (`harness_tools`): every tool by the
  host's own name and what it does (runs, reads, writes, spawns, network, inert).
  `magus agent harness install` writes them beside the host's config, one file per
  capability with one tool name per line and a version file, and the hook names the
  directory to `magus shell --tool-rows`. With no rows to read, no tool is known and the
  refusal names the install; `magus doctor` reports rows that differ from the spell.
