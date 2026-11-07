import type { EngineInterface, RenderElement } from 'claude-code'

import type { RefView } from '../types'
import { magusBin } from './bin'
import { runsMagus } from './budget'
import { refArgs, refCommand, refLinks, refOf, refsIn, refStrings, stripAnsi } from './refs'
import { fromPromise, isRecord } from './result'
import { copyFix } from './ui'
import type { On, RowInput } from './ui'

const REF_PANE = 'magus-ref'
const REF = { plugin: 'magus', key: 'ref' } as const
const READ_TIMEOUT_MS = 30_000
const MAX_DRAWN = 50_000

// settle writes the view unless a newer press has taken the pane for another
// ref, so a slow read never overwrites it.
async function settle($: EngineInterface, view: RefView): Promise<void> {
  const current = await fromPromise($.state.get(REF))
  if (current.error) return $.ui.toast(current.error.message)
  if (current.value.value?.ref !== view.ref) return
  const written = await fromPromise($.state.set(REF, view))
  if (written.error) $.ui.toast(written.error.message)
}

async function show($: EngineInterface, ref: string): Promise<void> {
  const loading: RefView = {
    ref,
    command: refCommand(ref),
    note: null,
    text: '',
    omitted: 0,
    error: null,
    isLoading: true,
  }
  const opened = await fromPromise(
    $.state.set(REF, loading).then(() => $.ui.open({ id: REF_PANE, title: ref, focus: true })),
  )
  if (opened.error) return $.ui.toast(opened.error.message)
  const done = { ...loading, isLoading: false }

  const root = await fromPromise($.session.root())
  if (root.error) return settle($, { ...done, error: root.error.message })
  const bin = await fromPromise(magusBin($, root.value))
  if (bin.error) return settle($, { ...done, error: bin.error.message })

  const argv = [bin.value.path, ...refArgs(ref)]
  const command = refCommand(ref, bin.value.isFallback ? 'magus' : './magus')
  const note = bin.value.isFallback ? `no ./magus at ${root.value}; ran magus from PATH` : null
  const ran = await fromPromise($.process.run(argv, { cwd: root.value, timeoutMs: READ_TIMEOUT_MS }))
  if (ran.error) return settle($, { ...done, command, note, error: ran.error.message })

  const r = ran.value
  const stdout = stripAnsi(r.stdout)
  const error = r.exitCode === 0 ? null : stripAnsi(r.stderr).trim() || `exit ${r.exitCode}`
  const text = stdout.slice(0, MAX_DRAWN)
  return settle($, { ...done, command, note, text, omitted: stdout.length - text.length, error })
}

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

const commandOf = (input: unknown): string =>
  isRecord(input) && typeof input.command === 'string' ? input.command : ''

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
    const { tool, input, output, isRunning } = e.props
    const isMagus = tool.startsWith('mcp__magus__') || (tool === 'Bash' && runsMagus(commandOf(input)))
    if (isRunning || !isMagus) return own
    const refs = refsIn(refStrings(output).join('\n'))
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
          <Button key="copy-command" label="Copy command" onPress={p => void copyFix($, view.command, p.surface)} />
        </Box>
        {view.note !== null ? <Text dimColor>{view.note}</Text> : null}
        {view.isLoading ? <Text dimColor>reading {view.ref}…</Text> : null}
        {view.error !== null ? <Text color="red">✖ {view.error}</Text> : null}
        {view.text !== '' ? <Code source={view.text} language="text" /> : null}
        {view.omitted > 0 ? <Text dimColor>{view.omitted} more characters not shown.</Text> : null}
      </Box>
    )
  })
}
