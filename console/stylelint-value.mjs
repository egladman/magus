// Shared value reading for the magus/* token rules. Stylelint hands a plugin the raw declaration
// string and the console cannot import postcss-value-parser (pnpm does not hoist it), so these
// helpers split only what the rules need: top-level words, top-level comma lists, var() and the
// math functions.

const globalKeyword = /^(?:inherit|initial|unset|revert|revert-layer)$/i;
const mathFunction = /^(?:calc|min|max|clamp)$/i;

// Index of the paren that closes the one opened at `open`, or -1 when the value is unbalanced.
function closingParen(value, open) {
  let depth = 0;
  for (let i = open; i < value.length; i++) {
    if (value[i] === "(") depth++;
    else if (value[i] === ")" && --depth === 0) return i;
  }
  return -1;
}

// Splits at depth 0 on whitespace, or on commas when `byComma` is set. Parenthesised groups stay whole.
export function splitTopLevel(value, { byComma = false } = {}) {
  const parts = [];
  let current = "";
  for (let i = 0; i < value.length; i++) {
    const char = value[i];
    if (char === "(") {
      const close = closingParen(value, i);
      const end = close === -1 ? value.length - 1 : close;
      current += value.slice(i, end + 1);
      i = end;
    } else if (byComma ? char === "," : /\s/.test(char)) {
      if (current.trim() !== "") parts.push(current.trim());
      current = "";
    } else {
      current += char;
    }
  }
  if (current.trim() !== "") parts.push(current.trim());
  return parts;
}

// Reads `name(args)` when the whole string is one function call, else null.
export function parseFunction(text) {
  const match = /^([a-z-]+)\(/i.exec(text);
  if (!match) return null;
  const open = match[0].length - 1;
  if (closingParen(text, open) !== text.length - 1) return null;
  return { name: match[1], args: text.slice(open + 1, -1) };
}

export function isGlobalKeyword(text) {
  return globalKeyword.test(text);
}

export function isMathFunction(name) {
  return mathFunction.test(name);
}

// `var(--name)` or `var(--name, fallback)`. The fallback is returned verbatim for the caller to judge.
export function parseVar(text) {
  const fn = parseFunction(text);
  if (!fn || fn.name.toLowerCase() !== "var") return null;
  const comma = fn.args.indexOf(",");
  if (comma === -1) return { name: fn.args.trim(), fallback: null };
  return { name: fn.args.slice(0, comma).trim(), fallback: fn.args.slice(comma + 1).trim() };
}

// Every custom property a value reads through var(), fallbacks included.
export function referencedProperties(value) {
  return [...value.matchAll(/var\(\s*(--[A-Za-z0-9_-]+)/g)].map((match) => match[1]);
}

// Walks a calc()/min()/max()/clamp() body and judges every operand with `operand`. Operators and
// commas are skipped; a bare number is a multiplier or divisor, never a length, so it passes.
// Returns false at the first operand that fails.
export function mathOperandsPass(body, operand) {
  const atoms = [];
  let current = "";
  const flush = () => {
    if (current !== "") atoms.push(current);
    current = "";
  };
  for (let i = 0; i < body.length; i++) {
    const char = body[i];
    if (char === "(") {
      const close = closingParen(body, i);
      const end = close === -1 ? body.length - 1 : close;
      current += body.slice(i, end + 1);
      i = end;
    } else if (/\s/.test(char) || char === "," || char === "*" || char === "/") {
      flush();
    } else {
      current += char;
    }
  }
  flush();

  return atoms.every((atom) => {
    if (atom === "+" || atom === "-") return true;
    if (/^[+-]?(?:\d+\.?\d*|\.\d+)$/.test(atom)) return true;
    if (atom.startsWith("(")) return mathOperandsPass(atom.slice(1, -1), operand);
    return operand(atom);
  });
}
