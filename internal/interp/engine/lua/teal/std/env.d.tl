-- Type declarations for the env host module.

global record Env
   -- get returns "" for an unset variable; use lookup to tell unset from
   -- set-but-empty.
   get: function(name: string): string
   -- lookup returns (value, found): found is false when the variable is unset
   -- or stripped by the sandbox. Mirrors Go's os.LookupEnv.
   lookup: function(name: string): string, boolean
   set: function(name: string, value: string)
   list: function(): {string:string}
end

-- Access as: local env = require("magus.extra.env")
