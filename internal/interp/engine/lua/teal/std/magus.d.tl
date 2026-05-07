-- Type declarations for the magus host module.

local record IgnorePattern
   glob: {string}
   regex: {string}
   literal: {string}
end

local record TargetPolicy
   -- cachable=false opts a target out of the cache: magus always runs it and
   -- never replays or snapshots it. Use for long-running targets (e.g. a
   -- blocking fs.watch loop) where a cached success would no-op a re-run.
   cachable: boolean
end

local record ProjectOpts
   -- depends_on declares dependency edges to other PROJECTS, named by path —
   -- repo-relative ("api", "internal/db") or relative to this project ("../api").
   -- Use it only for edges magus cannot infer from a language manifest (go.mod,
   -- Cargo.toml, package.json), e.g. a Go binary consuming a generated bundle.
   -- magus folds these into the build graph: they feed `magus affected` and the
   -- cache key. The same keyword on TargetPolicy names targets instead of
   -- projects; either way magus schedules dependencies in parallel.
   depends_on: {string}
   outputs: {string}
   exclusive: boolean
   -- spells binds one or more spells to this project. Reference each by value
   -- (declare-first), so a project can host several and a misspelled built-in
   -- is a compile error rather than a silent runtime nil:
   --   local go     = require("magus.spell.go")
   --   local docker = require("magus.spell.docker")
   --   magus.project.register(".", {spells = {go, docker}})
   -- {any} because the list mixes built-in handles (MagusSpell) and workspace-local
   -- ones (WorkspaceSpell); register only stores them, it does not call their ops.
   spells: {any}
   watch_ignore: IgnorePattern
   -- targets maps a target name to its per-target policy (e.g. cachable=false).
   targets: {string: TargetPolicy}
end

local record DispatchOpts
   ignore_missing: boolean
   fail_fast: boolean
end

-- Std modules are imported selectively, one per require, mirroring built-in
-- spells. Bind what a file uses at the top:
--   local os  = require("magus.extra.os")
--   local fs  = require("magus.extra.fs")
-- A misspelled module is a compile error. There is no magus.extra aggregate.
-- Available: os, platform, fs, vcs, crypto, env, http, json, log, charm.

-- SpellNamespace is magus.spell: fetch built-in spells and register
-- workspace-local ones. Registration is idempotent; only direct targets are
-- supported for locally-defined/required spells.
local record SpellNamespace
   -- get resolves a spell by name at runtime. Use it for spells the typed
   -- import form can't carry because they aren't known at compile time —
   -- host-registered spells (e.g. "magusfile") or plugin names computed at
   -- runtime. Returns nil for an unknown name:
   --   local mf = magus.spell.get("magusfile")
   -- For built-ins, prefer require("magus.spell.<name>") — a misspelled module
   -- is a compile error, not a runtime nil.
   get: function(name: string): WorkspaceSpell
   -- define registers a spell from an inline definition table and returns a
   -- handle, so it can be bound directly:
   --   local widget = magus.spell.define{name = "widget", ops = {...}}
   --   magus.project.register(".", {spells = {widget}})
   -- magus.spell.new is kept as a backward-compat alias for the same function.
   define: function(definition: MagusSpell): WorkspaceSpell
   new:    function(definition: MagusSpell): WorkspaceSpell
   -- load compiles a workspace-local spell source (relative to the magusfile),
   -- registers it, and returns it for binding. It is the explicit-path primitive;
   -- for a spell under cwd / spells/ / magusfiles/ prefer the require sugar, which
   -- resolves the same set and type-checks to WorkspaceSpell (ops reached by
   -- subscript, e.g. hello["echo"](), since a local spell's ops aren't a fixed set):
   --   local hello = require("spells.hello")   -- loads ./spells/hello.tl
   --   magus.project.register(".", {spells = {hello}})
   -- Reach for load when the path is non-conventional or absolute:
   --   local hello = magus.spell.load("build/tools/hello.tl")
   load: function(path: string): WorkspaceSpell
end

-- ProjectNamespace is magus.project: a namespace (like magus.spell) for
-- declaring projects.
local record ProjectNamespace
   -- register declares a project at path: pin its spell, list regenerable
   -- outputs, or add cross-project dependency edges. Registration is optional;
   -- magus auto-detects projects from marker files.
   register: function(path: string, opts: ProjectOpts)
end

-- TargetNamespace is magus.target: a namespace (like magus.spell) for
-- expanding target-name globs. Targets are declared as exported global
-- functions; use magus.target.expand_globs to resolve patterns to names.
local record TargetNamespace
   -- expand_globs returns the names of registered targets matching the
   -- glob/suffix patterns, sorted and deduped.
   expand_globs: function(patterns: string | {string}): {string}
end

-- CacheNamespace is magus.cache: wire cross-shard remote caching.
local record CacheNamespace
   -- remote records a function-op spell handle as the remote cache backend; the
   -- spell must expose get_artifact/put_artifact (and optionally enabled()) function-ops.
   -- Authored in either engine: magus.cache.remote(require("spells.mycache")).
   -- any because the handle may be a built-in (MagusSpell) or workspace-local one.
   -- The trust set that secures the backend (cache.remote.trusted_keys) lives in
   -- magus.yaml, not here — it is declarative config, not build logic.
   remote: function(spell: any)
end

global record Magus
   project: ProjectNamespace
   target: TargetNamespace
   cache: CacheNamespace
   -- depends_on runs named targets as dependencies of the calling target.
   -- Pass a string or a list: magus.depends_on("a") or
   -- magus.depends_on(magus.target.expand_globs("*-build")).
   depends_on: function(names: string | {string})
   spell: SpellNamespace
   cmd: function(args: {string}): integer
   pry: function()
   -- Escape hatch: clear cached build outputs. Prefer modeling missing inputs
   -- as Sources instead. No arg clears all; a project path clears one project.
   bust_cache: function(project_path: string)
   -- Structured logging into the process logger; mirrors the std `log` module so
   -- a magusfile can log without importing it. fields is an optional map.
   info:  function(msg: string, fields: {string:string})
   debug: function(msg: string, fields: {string:string})
   warn:  function(msg: string, fields: {string:string})
   error: function(msg: string, fields: {string:string})
   -- Advisory nudge: print an actionable note (deduped, honors the hints toggle)
   -- without affecting the exit code.
   hint:  function(msg: string)
   -- Log msg at error level, then abort the run with exit code 1.
   fatal: function(msg: string)
end

global magus: Magus
