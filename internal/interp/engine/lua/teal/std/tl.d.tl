-- Type declarations for the embedded Teal compiler (tl global).
-- The tl global is loaded by the host runtime via loadTealCompiler.

local record TlEnv end

local record Tl
   init_env: function(boolean, boolean, string): TlEnv
   gen: function(string, TlEnv): string, {string}
   loader: function()
end

global tl: Tl
