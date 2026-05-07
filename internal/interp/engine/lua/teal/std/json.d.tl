-- Type declarations for the json host module.

global record Json
   -- Decode a JSON string. Returns a table, string, number, or boolean.
   parse: function(s: string): any
   -- Encode a Lua value as a JSON string.
   stringify: function(value: any): string
end

-- Access as: local json = require("magus.extra.json")
