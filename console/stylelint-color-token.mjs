import stylelint from "stylelint";
import { isGlobalKeyword, parseVar, splitTopLevel } from "./stylelint-value.mjs";

export const ruleName = "magus/color-token";

const message = stylelint.utils.ruleMessages(ruleName, {
  literal: (prop, value) =>
    `${prop}: ${value} writes a colour literal; take it from a PatternFly or --console-* colour token`,
  named: (prop, value) =>
    `${prop}: ${value} writes a named colour; take it from a PatternFly or --console-* colour token`,
  shadow: (value) =>
    `box-shadow: ${value} is not a shadow token; take it from var(--pf-t--global--box-shadow--*) or none`,
  textShadow: (value) =>
    `text-shadow: ${value} has no token; PatternFly defines box shadows only, so write none`,
  dropShadow: (value) =>
    `filter: ${value} has no token; PatternFly defines box shadows only, so use a box-shadow token or none`,
});

// CSS Color 4 named colours. transparent, currentcolor and the system colours follow the theme or
// the user's contrast settings, which is what the rule is for, so they are not listed.
const namedColors = new Set(
  (
    "aliceblue antiquewhite aqua aquamarine azure beige bisque black blanchedalmond blue blueviolet brown " +
    "burlywood cadetblue chartreuse chocolate coral cornflowerblue cornsilk crimson cyan darkblue darkcyan " +
    "darkgoldenrod darkgray darkgreen darkgrey darkkhaki darkmagenta darkolivegreen darkorange darkorchid " +
    "darkred darksalmon darkseagreen darkslateblue darkslategray darkslategrey darkturquoise darkviolet " +
    "deeppink deepskyblue dimgray dimgrey dodgerblue firebrick floralwhite forestgreen fuchsia gainsboro " +
    "ghostwhite gold goldenrod gray green greenyellow grey honeydew hotpink indianred indigo ivory khaki " +
    "lavender lavenderblush lawngreen lemonchiffon lightblue lightcoral lightcyan lightgoldenrodyellow " +
    "lightgray lightgreen lightgrey lightpink lightsalmon lightseagreen lightskyblue lightslategray " +
    "lightslategrey lightsteelblue lightyellow lime limegreen linen magenta maroon mediumaquamarine " +
    "mediumblue mediumorchid mediumpurple mediumseagreen mediumslateblue mediumspringgreen mediumturquoise " +
    "mediumvioletred midnightblue mintcream mistyrose moccasin navajowhite navy oldlace olive olivedrab " +
    "orange orangered orchid palegoldenrod palegreen paleturquoise palevioletred papayawhip peachpuff peru " +
    "pink plum powderblue purple rebeccapurple red rosybrown royalblue saddlebrown salmon sandybrown " +
    "seagreen seashell sienna silver skyblue slateblue slategray slategrey snow springgreen steelblue tan " +
    "teal thistle tomato turquoise violet wheat white whitesmoke yellow yellowgreen"
  ).split(" "),
);

// Only properties that take a colour, and custom properties, which can carry one. A name in
// font-family, animation-name or a grid area is not a colour.
const colorProperty =
  /^(?:color|background(?:-color|-image)?|border(?:-[a-z]+)*|outline(?:-color)?|fill|stroke|caret-color|accent-color|text-decoration(?:-color)?|text-emphasis-color|column-rule(?:-color)?|scrollbar-color|--.*)$/;

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

function writesNamedColor(value) {
  const stripped = value
    .replace(/url\(\s*(?:"[^"]*"|'[^']*'|[^)]*)\s*\)/gi, " ")
    .replace(/"[^"]*"|'[^']*'/g, " ")
    .replace(/var\(\s*--[A-Za-z0-9_-]+/g, " ");
  return (stripped.match(/[a-z]+/gi) ?? []).some((word) => namedColors.has(word.toLowerCase()));
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
      const prop = declaration.prop.toLowerCase();
      const report = (text) =>
        stylelint.utils.report({ message: text, node: declaration, result, ruleName });

      if (prop === "box-shadow" && !shadowPasses(declaration.value)) {
        report(message.shadow(declaration.value));
        return;
      }
      if (prop === "text-shadow" && !/^(?:none|inherit|initial|unset|revert)$/i.test(declaration.value.trim())) {
        report(message.textShadow(declaration.value));
        return;
      }
      if (/^(?:backdrop-)?filter$/.test(prop) && /\bdrop-shadow\(/i.test(declaration.value)) {
        report(message.dropShadow(declaration.value));
        return;
      }
      if (declaration.prop.startsWith("--") && defines) return;

      if (writesColorLiteral(declaration.value)) {
        report(message.literal(declaration.prop, declaration.value));
      } else if (colorProperty.test(prop) && writesNamedColor(declaration.value)) {
        report(message.named(declaration.prop, declaration.value));
      }
    });
  };
});
