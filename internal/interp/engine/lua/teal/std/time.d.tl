-- Type declarations for the time host module.
-- Times are Unix epoch milliseconds (float64, as os.time() returns); layouts are
-- Go reference-time strings. format/parse are anchored to UTC. Parsing errors on
-- a layout mismatch.

global record Time
   format:         function(layout: string, unix_millis: number): string
   parse:          function(layout: string, value: string): number
   parse_duration: function(duration: string): number
end

-- Access as: local time = require("magus.extra.time")
