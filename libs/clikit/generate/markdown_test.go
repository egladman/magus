package generate

import "testing"

func TestMarkdown(t *testing.T) {
	rendered, err := Markdown(tool, toolConfig, "registry.go")
	noError(t, err)
	pages := pagesByName(rendered)
	equal(t, 1+len(tool.Children), len(pages))

	main := pages["tool.md"]
	contains(t, main, "---\ntitle: tool command\ngenerated_from: registry.go\n")
	contains(t, main, "**tool** [flags] \\<subcommand\\> [args]")
	contains(t, main, "**--root** *path*\n: Workspace path\n")
	contains(t, main, "**TOOL_DIR**\n: Where the cache lives (default: .tool). Config file key: **dir**.")
	contains(t, main, "### Set by tool for the processes it spawns, never by hand\n\n**TOOL_LEVEL**\n: The nesting depth")
	contains(t, main, "[**tool-run**(1)](tool-run.md)")

	run := pages["tool-run.md"]
	contains(t, run, "tags: [cli, tool run, run]")
	contains(t, run, "**--jobs** *N* (default: 4)\n: Run at most N at once")
	contains(t, run, "## Operands")
	contains(t, run, "```sh\ntool run build\n```")
	contains(t, pages["tool-config.md"], "### config cache prune options")
}
