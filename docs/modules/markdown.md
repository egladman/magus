# `markdown`

GitHub-Flavored Markdown to semantic HTML.

> **Naming convention:** Buzz reaches modules off the `import "magus/extra"` aggregate in `camelCase` (`extra.markdown.someMethod`).

## Methods

### `to_html`

Render GitHub-Flavored Markdown to semantic HTML. Auto-IDs every heading so #fragment links resolve, and rewrites relative .md links (incl. README.md → index.html) to their generated .html equivalents.

**Signature:** `extra.markdown.toHtml(source) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `source` | `string` |  | |

**Returns:** string

