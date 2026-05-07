-- Type declarations for the MagusSpell record returned by
-- require('magus.spell.<name>').

-- TargetOpts is the uniform opts record passed when invoking a spell operation
-- as a method. args is the CLI escape hatch; cwd is the working directory.
global record TargetOpts
   args:    {string}
   cwd:     string
   -- charms is the active charm set as a lookup table: `if opts.charms.rw then`
   -- (the built-in read/write toggle), `if opts.charms.debug then`, etc.
   charms:  {string : boolean}
   -- env overlays the forked subprocess environment (KEY=value); later entries
   -- win. Use for cross-compiles and tool env: go["go-build"]({env={GOOS="linux"}}).
   env:     {string : string}
   -- stdin is fed to the forked process as standard input — pipe by passing a
   -- prior captured op's stdout: b({stdin = a().stdout}).
   stdin:   string
end

-- OpSpec declares a spell operation as data. cmd is the tool to fork, args is
-- the base argv, and charms maps charm names to Charm modifiers (see charm.d.tl).
-- All ops are direct — magus forks cmd without a script VM, so invocations are
-- deterministic and injection-safe. An empty cmd is a no-op. capture makes the
-- op's method return an ExecRecord instead of nil. See docs/charms.md.
global record OpSpec
   cmd:     string
   args:    {string}
   charms:  {string : any}
   capture: boolean
end

-- ExecRecord is the {stdout, stderr, code, ok} result a captured op returns —
-- the same shape os.exec returns. A non-captured op returns nil at runtime, so
-- only assign the result of an op declared capture=true.
global record ExecRecord
   stdout: string
   stderr: string
   code:   integer
   ok:     boolean
end

-- The MagusSpell record is generated at compile time (see spell.RegistryTypeDecl)
-- rather than declared here: its op-method fields are the union of every built-in
-- spell's tool-native operations, derived from spells.json so the typed surface
-- never drifts. A spell is a library of those operations — binding it to a project
-- (magus.project.register{spells={...}}) contributes its needs/claims/provides to
-- the project's cache key and affected set but runs nothing on its own. Compose
-- the operations into runnable targets from the magusfile by calling them as
-- methods. Op names follow the CLI command (docs/spells.md), reached by subscript:
-- global function build(_a) require("magus.spell.go")["go-build"]() end.
