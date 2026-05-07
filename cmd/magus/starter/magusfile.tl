-- magusfile.tl — magus workspace definition.
--
-- Projects are declared explicitly with magus.project.register. A project names
-- the spell(s) that build it and, optionally, its regenerable outputs and any
-- cross-project dependency edges.
--
--   magus describe targets   -- show targets in this workspace
--   magus describe spells    -- show all available spells

-- Require the workspace-local "hello" spell (./spells/hello/spell.tl) and bind it to
-- this project. The handle exposes each spell target as a method (hello.build()),
-- so a magusfile target can delegate straight to it. Reach a built-in spell the
-- same way:
--
--   local go = require("magus.spell.go")
--   magus.project.register(".", { spells = {go} })
--   ... go["golangci-lint"]() / go["go-test"]() ...
-- Built-in ops are named after the CLI command (docs/spells.md) and are kebab-case,
-- so reach them by subscript: go["go-build"](), not go.build().
--
local hello = require("spells.hello")
magus.project.register(".", {
   spells = {hello},
   -- outputs = {"bin/*", "dist/**"},   -- regenerable build artifacts
   -- depends_on = {"../api"},           -- other PROJECTS this one depends on
})

-- ── targets ─────────────────────────────────────────────────────────────────
-- Each exported global function is a runnable target. Compose a stage by calling
-- a bound spell's tool-native op from its body — build below delegates to
-- hello.echo(); for a built-in you would write e.g.
--   local go = require("magus.spell.go")
--   global function build(_a) go["go-build"]() end
--   global function lint(_a) go["golangci-lint"]() end
-- Leave a stage as a no-op until you wire it.

global function preflight(_args: {string}) end
global function generate(_args: {string}) magus.depends_on({"preflight"}) end
global function format(_args: {string}) magus.depends_on({"generate"}) end
global function lint(_args: {string}) magus.depends_on({"format"}) end
global function build(_args: {string}) magus.depends_on({"format"}) hello.echo() end
global function test(_args: {string}) magus.depends_on({"format"}) end

-- 'ci' is the conventional anchor that `magus affected ci` keys off. magus does
-- not hardcode its steps — compose them here with magus.depends_on. magus runs
-- ci read-only. Each stage declares its own prerequisites (edges), so ci fans out
-- lint/build/test in parallel; they each wait for format, which waits for generate,
-- which waits for preflight. Shared deps (e.g. format) run exactly once.
global function ci(_args: {string})
   magus.depends_on({"lint", "build", "test"})
end
