-- Type declarations for the markdown host module.

global record Markdown
   -- Render GitHub-Flavored Markdown to semantic HTML.
   to_html: function(source: string): string
end

-- Access as: local markdown = require("magus.extra.markdown")
