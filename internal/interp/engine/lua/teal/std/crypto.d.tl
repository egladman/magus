-- Type declarations for the crypto host module.

global record Crypto
   sha256_hex: function(data: string): string
   sha256_file: function(path: string): string
   sha512_hex: function(data: string): string
   sha512_file: function(path: string): string
   -- SHA-1 / MD5: legacy-checksum interop only, not collision-resistant.
   sha1_hex: function(data: string): string
   sha1_file: function(path: string): string
   md5_hex: function(data: string): string
   md5_file: function(path: string): string
end

-- Access as: local crypto = require("magus.extra.crypto")
