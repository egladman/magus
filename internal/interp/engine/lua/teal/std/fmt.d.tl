-- Type declarations for the fmt host module.

global record Fmt
   -- Format string args into the template using Go printf verbs (%s, %q, …).
   sprintf: function(format: string, ...: string): string
end

-- Access as: local fmt = require("magus.extra.fmt")
