-- spells/hello/spell.tl — a workspace-local spell.
--
-- A spell is a module that exposes its contract as mgs_-prefixed functions: a
-- required mgs_getName, plus optional mgs_listRequiredGlobs / mgs_listProvidedGlobs / mgs_listTargets
-- and friends. Imported from the magusfile with require("spells.hello") and bound
-- to a project with magus.project.register(".", { spells = {hello} }).
--
-- Each op in mgs_listTargets names a command and its argv, which magus forks directly
-- — no shell, no variable expansion, so invocations are deterministic and
-- injection-safe. For an op that needs a shell or a Lua body, write a global
-- function in the magusfile instead.
--
-- Ops are named after the CLI command they run (see docs/spells.md), so this op
-- is "echo", not a lifecycle verb like "build". The handle require returns exposes
-- each op as a method, so the magusfile can call hello.echo() directly (see
-- magusfile.tl).
return {
   mgs_getName = function(): string return "hello" end,
   mgs_listTargets = function(): any
      return {
         echo = { cmd = "echo", args = {"Hello from magus"} },
      }
   end,
}
