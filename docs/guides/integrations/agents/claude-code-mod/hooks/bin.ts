import type { EngineInterface } from 'claude-code'

import { fromPromise } from './result'

// Bin is the magus a call runs. isFallback: the workspace has no ./magus,
// so the call runs the one on PATH, which the guard does not judge.
export type Bin = { path: string; isFallback: boolean }

// magusBin prefers the workspace's own ./magus, the one the guard runs.
export async function magusBin($: EngineInterface, root: string): Promise<Bin> {
  const local = await fromPromise($.fs.exists(`${root}/magus`))
  return local.value === true ? { path: `${root}/magus`, isFallback: false } : { path: 'magus', isFallback: true }
}
