-- Type declarations for the http host module.
-- All methods return (status_code, body); retry on transient errors is built in.

global record Http
   get:     function(url: string, headers: {string:string}): integer, string
   post:    function(url: string, body: string, headers: {string:string}): integer, string
   request: function(method: string, url: string, body: string, headers: {string:string}): integer, string
end

-- Access as: local http = require("magus.extra.http")
