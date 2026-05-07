# `markdown`

GitHub-Flavored Markdown to semantic HTML.

> **Naming convention:** Teal/Lua binds each module per-import in `snake_case` (`local markdown = require("magus.extra.markdown")`, then `markdown.some_method`). Buzz reaches them off the `import "magus/extra"` aggregate in `camelCase` (`extra.markdown.someMethod`).

## Methods

### `to_html`

Render GitHub-Flavored Markdown to semantic HTML. Auto-IDs every heading so #fragment links resolve, and rewrites relative .md links (incl. README.md → index.html) to their generated .html equivalents.

**Signature (Teal):** `markdown.to_html(source) → string`

**Signature (Buzz):** `extra.markdown.toHtml(source) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `source` | `string` |  | |

**Returns:** string

