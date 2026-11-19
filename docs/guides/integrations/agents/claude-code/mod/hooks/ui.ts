import type { EngineInterface, On, RenderInput } from 'claude-code'

import { fromPromise, ok, type Result } from './result'

export type { On }

// CopyTarget is the host's own type for where a copy lands, read off ui.copy.
type CopyTarget = Parameters<EngineInterface['ui']['copy']>[0]['surface']

// A transcript row a hook draws under: a reply or a tool call.
export type RowInput = RenderInput<'AssistantMessage'> | RenderInput<'ToolUse'>

// copyFix copies text and toasts the copy, or the command to run by hand
// when no clipboard took it.
export async function copyFix($: EngineInterface, text: string, surface: CopyTarget): Promise<Result<void>> {
  const copied = await fromPromise($.ui.copy({ text, surface }))
  if (copied.error) {
    $.ui.toast(`Not copied (${copied.error.message}). Run: ${text}`)
    return copied
  }
  $.ui.toast(copied.value.isCopied ? `Copied: ${text}` : `Not copied (${copied.value.reason}). Run: ${text}`)
  return ok(undefined)
}
