-- Type declarations for charm values and the magus.extra.charm constructors.

-- PatchOp is one RFC 6902 operation over a target's argv (a JSON array of
-- strings). path/from are JSON Pointers: "/N" for an index, "/-" for the end
-- (add only). value carries the string element for add/replace/test.
global record PatchOp
   op:    string
   path:  string
   value: string
   from:  string
end

-- Charm is the value a spell lists under `charms`: an RFC 6902 JSON Patch
-- applied over the target's base argv. Build it with the CharmTool constructors.
global record Charm
   ops: {PatchOp}
end

-- CharmTool is magus.extra.charm: constructors that build a Charm, resolving any
-- value/predicate anchor to a numeric JSON Pointer at author time so the stored
-- patch is pure positional RFC 6902 (see docs/charms.md).
global record CharmTool
   append:      function(vals: {string}): Charm
   prepend:     function(vals: {string}): Charm
   after:       function(argv: {string}, anchor: string, vals: {string}): Charm
   before:      function(argv: {string}, anchor: string, vals: {string}): Charm
   set:         function(argv: {string}, anchor: string, val: string): Charm
   remove:      function(argv: {string}, anchor: string): Charm
   after_func:  function(argv: {string}, fn: function(string): boolean, vals: {string}): Charm
   before_func: function(argv: {string}, fn: function(string): boolean, vals: {string}): Charm
   set_func:    function(argv: {string}, fn: function(string): boolean, val: string): Charm
   remove_func: function(argv: {string}, fn: function(string): boolean): Charm
   -- path resolves an anchor to its JSON Pointer ("/N"), the index auto-calculated.
   path:        function(argv: {string}, anchor: string): string
   path_func:   function(argv: {string}, fn: function(string): boolean): string
   -- move/copy relocate the anchored element to a destination pointer `to`
   -- ("/-" end, "/0" front, or charm.path(...)); test guards that the anchored
   -- element is still in place when the patch applies (else the run errors).
   move:        function(argv: {string}, anchor: string, to: string): Charm
   move_func:   function(argv: {string}, fn: function(string): boolean, to: string): Charm
   copy:        function(argv: {string}, anchor: string, to: string): Charm
   copy_func:   function(argv: {string}, fn: function(string): boolean, to: string): Charm
   test:        function(argv: {string}, anchor: string): Charm
   test_func:   function(argv: {string}, fn: function(string): boolean): Charm
end

-- Access as: local charm = require("magus.extra.charm")
