import stylelint from "stylelint";
import {
  isGlobalKeyword,
  isMathFunction,
  mathOperandsPass,
  parseFunction,
  parseVar,
  splitTopLevel,
} from "./stylelint-value.mjs";

export const ruleName = "magus/spacer-token";

const message = stylelint.utils.ruleMessages(ruleName, {
  rejected: (prop, value) =>
    `${prop}: ${value} is not a spacer; take it from var(--pf-t--global--spacer--*) or a console spacing token, or 0, auto or a percentage`,
});

const watched = /^(?:(?:padding|margin|inset|grid-gap|grid-row-gap|grid-column-gap|gap|row-gap|column-gap)(?:-[a-z-]+)?|top|right|bottom|left)$/;
// Where a box sits, as opposed to how much room it leaves around its content.
const placement = /^(?:inset(?:-[a-z-]+)?|top|right|bottom|left)$/;

// Semantic spacers only. The numbered base steps (spacer--100 .. --800) are what the semantic names
// are built from, so a stylesheet that reaches for one has skipped the vocabulary.
const spacerToken =
  /^--pf-t--global--spacer--(?:(?:xs|sm|md|lg|xl|[234]xl)|(?:control|action)--(?:horizontal|vertical)--[a-z-]+|gap--[a-z-]+|gutter--default|inset--page-chrome)$/;
// --log-pad and --notes-pad are the re-scaling seams the README defines as var(--console-pad).
const consoleToken = /^--(?:console-(?:pad|gap|shell-gutter)|log-pad|notes-pad)$/;
// A fixed shell bar or column is placed against by its own size: a sticky pane sits under the top
// chrome, a floating note sits inside the launcher column. That is where it is, not how much room
// it leaves, so only top, right, bottom, left and inset take these.
const placementToken = /^--console-(?:topchrome-h|titlebar-h|statusbar-h|launcher-w)$/;
const zero = /^[+-]?0*\.?0+(?:[a-z]+)?$/i;
const percentage = /^[+-]?(?:\d+\.?\d*|\.\d+)%$/;

function componentPasses(component, prop) {
  if (zero.test(component) || percentage.test(component) || /^(?:auto|normal)$/i.test(component)) return true;
  if (isGlobalKeyword(component)) return true;

  const variable = parseVar(component);
  if (variable) {
    const named = spacerToken.test(variable.name) || consoleToken.test(variable.name) ||
      (placement.test(prop) && placementToken.test(variable.name));
    if (!named) return false;
    return variable.fallback === null || valuePasses(variable.fallback, prop);
  }

  const fn = parseFunction(component);
  if (fn && isMathFunction(fn.name)) return mathOperandsPass(fn.args, (atom) => componentPasses(atom, prop));
  return false;
}

function valuePasses(value, prop) {
  const components = splitTopLevel(value);
  return components.length > 0 && components.every((component) => componentPasses(component, prop));
}

// An allowance names the selectors whose spacing is data, optionally only for some properties, and
// why. A missing reason is rejected so the list cannot grow into a silent ignore.
const isGeometry = (entry) =>
  entry !== null &&
  typeof entry === "object" &&
  entry.selector instanceof RegExp &&
  (entry.property === undefined || entry.property instanceof RegExp) &&
  typeof entry.reason === "string" &&
  entry.reason.trim() !== "";

export default stylelint.createPlugin(ruleName, (enabled, options) => {
  return (root, result) => {
    const valid = stylelint.utils.validateOptions(
      result,
      ruleName,
      { actual: enabled, possible: [true] },
      { actual: options, possible: { geometry: [isGeometry] }, optional: true },
    );
    if (!valid || !enabled) return;

    const geometry = options?.geometry ?? [];

    root.walkDecls((declaration) => {
      if (!watched.test(declaration.prop)) return;
      if (valuePasses(declaration.value, declaration.prop)) return;

      const selectors = declaration.parent?.selectors ?? [];
      const exempt = selectors.length > 0 &&
        selectors.every((selector) =>
          geometry.some((entry) =>
            entry.selector.test(selector) && (entry.property?.test(declaration.prop) ?? true)));
      if (exempt) return;

      stylelint.utils.report({
        message: message.rejected(declaration.prop, declaration.value),
        node: declaration,
        result,
        ruleName,
      });
    });
  };
});
