import stylelint from "stylelint";
import { isGlobalKeyword, parseVar, splitTopLevel } from "./stylelint-value.mjs";

export const ruleName = "magus/color-token";

const message = stylelint.utils.ruleMessages(ruleName, {
  literal: (prop, value) =>
    `${prop}: ${value} writes a colour literal; take it from a PatternFly or --console-* colour token`,
  shadow: (value) =>
    `box-shadow: ${value} is not a shadow token; take it from var(--pf-t--global--box-shadow--*) or none`,
});

const hex = /#(?:[0-9a-f]{8}|[0-9a-f]{6}|[0-9a-f]{3,4})(?![0-9a-z_-])/i;
const colorFunction = /\b(?:rgba?|hsla?)\(/i;
// A data: URI cannot take a var(), so its colours are written percent-encoded (%23 is #).
const encodedHex = /%23(?:[0-9a-f]{8}|[0-9a-f]{6}|[0-9a-f]{3,4})(?![0-9a-z_-])/i;

// Complete shadows only. The blur, spread and colour pieces they are assembled from are not.
const shadowToken =
  /^--pf-t--global--box-shadow--(?:(?:sm|md|lg)(?:--(?:top|right|bottom|left))?|glass--default)$/;

function writesColorLiteral(value) {
  // A url() is not a colour unless it is a data: URI carrying one; quoted text never is.
  const stripped = value
    .replace(/url\(\s*(?:"[^"]*"|'[^']*'|[^)]*)\s*\)/gi, (url) =>
      /data:/i.test(url) && (encodedHex.test(url) || colorFunction.test(url)) ? " #000 " : " ")
    .replace(/"[^"]*"|'[^']*'/g, " ");
  return hex.test(stripped) || colorFunction.test(stripped);
}

function shadowPasses(value) {
  const trimmed = value.trim();
  if (/^none$/i.test(trimmed) || isGlobalKeyword(trimmed)) return true;
  const layers = splitTopLevel(trimmed, { byComma: true });
  return layers.length > 0 && layers.every((layer) => {
    const variable = parseVar(layer);
    if (!variable || !shadowToken.test(variable.name)) return false;
    return variable.fallback === null || shadowPasses(variable.fallback);
  });
}

// A definition allowance names the file that is allowed to DEFINE colours in custom properties, and
// why. It never covers a colour written into a real property.
const isDefinition = (entry) =>
  entry !== null &&
  typeof entry === "object" &&
  entry.file instanceof RegExp &&
  typeof entry.reason === "string" &&
  entry.reason.trim() !== "";

export default stylelint.createPlugin(ruleName, (enabled, options) => {
  return (root, result) => {
    const valid = stylelint.utils.validateOptions(
      result,
      ruleName,
      { actual: enabled, possible: [true] },
      { actual: options, possible: { definitions: [isDefinition] }, optional: true },
    );
    if (!valid || !enabled) return;

    const file = (root.source?.input.file ?? "").replaceAll("\\", "/");
    const defines = (options?.definitions ?? []).some((entry) => entry.file.test(file));

    root.walkDecls((declaration) => {
      if (declaration.prop.toLowerCase() === "box-shadow" && !shadowPasses(declaration.value)) {
        stylelint.utils.report({
          message: message.shadow(declaration.value),
          node: declaration,
          result,
          ruleName,
        });
        return;
      }
      if (declaration.prop.startsWith("--") && defines) return;
      if (!writesColorLiteral(declaration.value)) return;

      stylelint.utils.report({
        message: message.literal(declaration.prop, declaration.value),
        node: declaration,
        result,
        ruleName,
      });
    });
  };
});
