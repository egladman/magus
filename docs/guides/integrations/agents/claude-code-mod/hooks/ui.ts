import type { EngineInterface, On, RenderInput, RenderSurface } from 'claude-code'

export type { On }

// A transcript row a hook draws under: a reply or a tool call.
export type RowInput = RenderInput<'AssistantMessage'> | RenderInput<'ToolUse'>

// copyFix copies text and toasts the copy, or the command to run by hand
// when no clipboard took it.
export async function copyFix($: EngineInterface, text: string, surface: RenderSurface | undefined): Promise<void> {
  const copied = await $.ui.copy({ text, surface })
  $.ui.toast(copied.isCopied ? `Copied: ${text}` : `Not copied (${copied.reason}). Run: ${text}`)
}
