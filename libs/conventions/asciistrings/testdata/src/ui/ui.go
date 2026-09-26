package ui

// A comment may say “quoted” — comments are exempt.

var (
	plain  = "a -> b, done..."
	dashed = "cached — skipped" // want `string literal carries em dash: user-facing strings are plain ASCII`
	both   = "“quoted” and 3×4" // want `string literal carries left double quote, right double quote, multiplication sign`
	raw    = `waiting…`         // want `string literal carries ellipsis`
	drawn  = "⠋⠙⠹"              // drawing glyphs are not typographic substitutes
)
