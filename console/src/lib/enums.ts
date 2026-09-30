// enums.ts - the word for a protobuf enum value, derived from the value's name so no surface keeps
// its own verdict/support/state table. The server owns the vocabulary; a value added there reads
// correctly here without an edit.

/** A protobuf-es enum object: numeric values with the reverse name lookup TypeScript generates. */
export type EnumType = { readonly [value: number]: string };

/**
 * Turns an enum value into lowercase words: TOO_OLD reads "too old". The generated names drop the
 * proto prefix (VERDICT_TOO_OLD is TOO_OLD), so what remains is the word itself.
 *
 * unspecified is what the zero value reads as, and what a value this build does not know reads as.
 * The caller picks it because the honest word differs: a verdict nobody set is "unknown", a
 * lifecycle state nobody set is "unwired", and a support value nobody set is empty.
 */
export function enumWord(type: EnumType, value: number, unspecified: string): string {
  if (value === 0) return unspecified;
  const name = type[value];
  return name === undefined ? unspecified : name.toLowerCase().replace(/_/g, " ");
}
