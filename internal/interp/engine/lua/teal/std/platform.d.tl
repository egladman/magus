-- Type declarations for the platform host module.

global record Platform
   -- Normalize an architecture identifier to canonical Go GOARCH; with style,
   -- render it in a convention (go|uname). "" if unrecognized.
   arch: function(name: string, style?: string): string
   -- Normalize an OS identifier to canonical Go GOOS; with style, render it in
   -- a convention (go|uname). "" if unrecognized.
   os: function(name: string, style?: string): string
end

-- Access as: local platform = require("magus.extra.platform")
