// normalize turns provider assertion context into the stable trace consumed by
// constraints. Keep provider-specific field names here so predicates stay plain.
export function normalize(context = {}) {
  return {
    toolCalls: context.toolCalls ?? context.tool_calls ?? [],
    skillCalls: context.skillCalls ?? context.skill_calls ?? [],
  };
}
