import stylelint from "stylelint";
import { parseVar, splitTopLevel } from "./stylelint-value.mjs";

export const ruleName = "magus/type-token";

const message = stylelint.utils.ruleMessages(ruleName, {
  rejected: (prop, value) =>
    prop === "font-size"
      ? `font-size: ${value} is not a type token; take it from var(--pf-t--global--font--size--*), or inherit, or an em or % that cannot shrink below the parent`
      : `font-weight: ${value} is not a type token; take it from var(--pf-t--global--font--weight--*)`,
});

// Named steps only. The numbered primitives (font--size--100 .. --800, font--weight--100 .. --400)
// are what the names are built from. Every named step is 12px or larger, which is what holds the floor.
const sizeToken =
  /^--pf-t--global--font--size--(?:body--(?:sm|default|lg)|heading--[a-z0-9]+|xs|sm|md|lg|xl|[234]xl)$/;
const weightToken =
  /^--pf-t--global--font--weight--(?:body|heading)--(?:default|bold)(?:--legacy)?$/;
// The label and chip tokens are the uppercase recipes magus/label-token requires.
const consoleSizeToken = /^--console-(?:label-size|chip-size|[a-z0-9-]*font-size)$/;
const consoleWeightToken = /^--console-(?:label-weight|chip-weight|[a-z0-9-]*font-weight)$/;

// These keep the parent's size or grow it. `initial` is excluded: it resets to medium, which can be smaller.
const sameOrLarger = /^(?:inherit|unset|larger)$/i;
const relative = /^(?:(\d*\.?\d+)(em)|(\d*\.?\d+)(%))$/i;

function relativeCannotShrink(value) {
  const match = relative.exec(value);
  if (!match) return false;
  return match[2] ? Number(match[1]) >= 1 : Number(match[3]) >= 100;
}

function tokenPasses(value, token, consoleToken, relativeOk) {
  const components = splitTopLevel(value);
  if (components.length !== 1) return false;
  const [component] = components;
  if (sameOrLarger.test(component)) return true;
  if (relativeOk && relativeCannotShrink(component)) return true;

  const variable = parseVar(component);
  if (!variable) return false;
  if (!token.test(variable.name) && !consoleToken.test(variable.name)) return false;
  return variable.fallback === null || tokenPasses(variable.fallback, token, consoleToken, relativeOk);
}

const sizePasses = (value) => tokenPasses(value, sizeToken, consoleSizeToken, true);
const weightPasses = (value) =>
  /^(?:inherit|unset)$/i.test(value.trim()) ||
  tokenPasses(value, weightToken, consoleWeightToken, false);

export default stylelint.createPlugin(ruleName, (enabled) => {
  return (root, result) => {
    if (!enabled) return;

    root.walkRules((rule) => {
      // An uppercase run is magus/label-token's, which pins both properties to the label and chip tokens.
      let uppercase = false;
      rule.walkDecls("text-transform", (declaration) => {
        if (declaration.value.trim() === "uppercase") uppercase = true;
      });
      if (uppercase) return;

      rule.walkDecls(/^font-(?:size|weight)$/, (declaration) => {
        if (declaration.parent !== rule) return;
        const passes = declaration.prop === "font-size" ? sizePasses : weightPasses;
        if (passes(declaration.value)) return;

        stylelint.utils.report({
          message: message.rejected(declaration.prop, declaration.value),
          node: declaration,
          result,
          ruleName,
        });
      });
    });
  };
});
