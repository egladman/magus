import {
  RAW_GO,
  RAW_NODE,
  RAW_PY,
  REGISTRY,
  STAGE_EVERYTHING,
  WHOLE_TREE_GIT,
} from "./constraints.mjs";
import { normalize } from "./normalize.mjs";

const patterns = {
  RAW_GO,
  RAW_NODE,
  RAW_PY,
  STAGE_EVERYTHING,
  WHOLE_TREE_GIT,
};

// constraint is the Promptfoo adapter. Task files name a deterministic predicate
// and its data; the provider response is normalized once before the predicate
// sees it, keeping the rest of the harness provider-agnostic.
export function constraint(_output, context) {
  const { fn, args = [] } = context.config ?? {};
  const predicate = REGISTRY[fn];
  if (!predicate) {
    throw new Error(`unknown constraint predicate ${fn}`);
  }
  const trace = normalize(context.providerResponse?.metadata);
  const result = predicate(trace, ...args.map((arg) => patterns[arg] ?? arg));
  return { pass: result.passed, score: result.passed ? 1 : 0, reason: result.evidence };
}
