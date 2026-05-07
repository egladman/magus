-- Type declarations for the fs host module.

global record Fs
   glob: function(pattern: string): {string}
   dirname: function(path: string): string
   basename: function(path: string): string
   exists: function(path: string): boolean
   read_file: function(path: string): string
   write_file: function(path: string, content: string)
   mkdirall: function(path: string)
   join: function(...: string): string
   remove_all: function(path: string)
   list_dir: function(path: string): {string}
   watch: function(paths: {string}, callback: function(changed: {string}): boolean)
end

-- Access as: local fs = require("magus.extra.fs")
