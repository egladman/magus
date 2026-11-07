import type { EngineInterface, Register, RenderElement, RenderInput, RenderSurface } from 'claude-code'

import { magusBin } from './bin'
import { refArgs, refCommand, refLinks, refOf, refsIn, stripAnsi } from './refs'

type On = Parameters<Register>[0]

const REF_PANE = 'magus-ref'
const REF = { plugin: 'magus', key: 'ref' } as const
const READ_TIMEOUT_MS = 30_000

async function show($: EngineInterface, ref: string): Promise<void> {
  const command = refCommand(ref)
  await $.state.set(REF, { ref, command, text: '', error: null, isLoading: true })
  await $.ui.open({ id: REF_PANE, title: ref, focus: true })
  const root = await $.session.root()
  const argv = [await magusBin($, root), ...refArgs(ref)]
  try {
    const r = await $.process.run(argv, { cwd: root, timeoutMs: READ_TIMEOUT_MS })
    const error = r.exitCode === 0 ? null : stripAnsi(r.stderr).trim() || `exit ${r.exitCode}`
    await $.state.set(REF, { ref, command, text: stripAnsi(r.stdout), error, isLoading: false })
  } catch (e) {
    const error = e instanceof Error ? e.message : String(e)
    await $.state.set(REF, { ref, command, text: '', error, isLoading: false })
  }
}

async function copy($: EngineInterface, text: string, surface: RenderSurface | undefined): Promise<void> {
  const copied = await $.ui.copy({ text, surface })
  $.ui.toast(copied.isCopied ? `Copied: ${text}` : `Not copied (${copied.reason}). Run: ${text}`)
}

type RowInput = RenderInput<'AssistantMessage'> | RenderInput<'ToolUse'>

function withLinks($: EngineInterface, e: RowInput, own: RenderElement, refs: string[]): RenderElement {
  const { Box, Markdown } = $.ui.resolve(e)
  return (
    <Box flexDirection="column">
      {own}
      <Markdown
        key={`refs-${e.requestId}`}
        text={`↳ ${refLinks(refs)}`}
        dimColor
        onLinkPress={link => {
          const ref = refOf(link.href)
          if (ref !== null) void show($, ref)
        }}
      />
    </Box>
  )
}

function isMagusCall(tool: string, input: unknown): boolean {
  if (tool.startsWith('mcp__magus__')) return true
  if (tool !== 'Bash' || typeof input !== 'object' || input === null || !('command' in input)) return false
  return typeof input.command === 'string' && /(^|[\s;&|(])(\.\/)?magus\s/.test(input.command)
}

// registerRefLinks makes every output ref, guard detail and invocation id a
// reply or a magus call prints a link. A press opens its content in a pane.
export function registerRefLinks(on: On): void {
  on('ui.render', { component: 'AssistantMessage' }, async ($, e, next) => {
    const own = await next(e)
    const refs = refsIn(e.props.text)
    return refs.length === 0 ? own : withLinks($, e, own, refs)
  })

  on('ui.render', { component: 'ToolUse' }, async ($, e, next) => {
    const own = await next(e)
    if (e.props.isRunning || !isMagusCall(e.props.tool, e.props.input)) return own
    const refs = refsIn(JSON.stringify(e.props.output ?? ''))
    return refs.length === 0 ? own : withLinks($, e, own, refs)
  })

  on('ui.render', { component: 'Pane', requestId: REF_PANE }, async ($, e) => {
    const { Box, Button, Code, Text } = $.ui.resolve(e)
    const { value: view = null } = await $.state.get(REF)
    if (view === null) return <Text dimColor>Press a ref in the transcript to show it here.</Text>
    return (
      <Box flexDirection="column" gap={1}>
        <Box gap={1}>
          <Box flexShrink={1}>
            <Text wrap="truncate-end">{view.command}</Text>
          </Box>
          <Button key="copy-command" label="Copy command" onPress={p => void copy($, view.command, p.surface)} />
        </Box>
        {view.isLoading ? <Text dimColor>reading {view.ref}…</Text> : null}
        {view.error !== null ? <Text color="red">✖ {view.error}</Text> : null}
        {view.text !== '' ? <Code source={view.text} language="text" /> : null}
      </Box>
    )
  })
}
