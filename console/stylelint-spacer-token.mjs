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

const watched = /^(?:padding|margin|inset|grid-gap|grid-row-gap|grid-column-gap|gap|row-gap|column-gap)(?:-[a-z-]+)?$/;

// Semantic spacers only. The numbered base steps (spacer--100 .. --800) are what the semantic names
// are built from, so a stylesheet that reaches for one has skipped the vocabulary.
const spacerToken =
  /^--pf-t--global--spacer--(?:(?:xs|sm|md|lg|xl|[234]xl)|(?:control|action)--(?:horizontal|vertical)--[a-z-]+|gap--[a-z-]+|gutter--default|inset--page-chrome)$/;
// --log-pad and --notes-pad are the re-scaling seams the README defines as var(--console-pad).
const consoleToken = /^--(?:console-(?:pad|gap|shell-gutter)|log-pad|notes-pad)$/;
const zero = /^[+-]?0*\.?0+(?:[a-z]+)?$/i;
const percentage = /^[+-]?(?:\d+\.?\d*|\.\d+)%$/;

function componentPasses(component) {
  if (zero.test(component) || percentage.test(component) || /^(?:auto|normal)$/i.test(component)) return true;
  if (isGlobalKeyword(component)) return true;

  const variable = parseVar(component);
  if (variable) {
    if (!spacerToken.test(variable.name) && !consoleToken.test(variable.name)) return false;
    return variable.fallback === null || valuePasses(variable.fallback);
  }

  const fn = parseFunction(component);
  if (fn && isMathFunction(fn.name)) return mathOperandsPass(fn.args, componentPasses);
  return false;
}

function valuePasses(value) {
  const components = splitTopLevel(value);
  return components.length > 0 && components.every(componentPasses);
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
      if (valuePasses(declaration.value)) return;

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
