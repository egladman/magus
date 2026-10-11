import stylelint from "stylelint";
import { isGlobalKeyword, parseVar, splitTopLevel } from "./stylelint-value.mjs";

export const ruleName = "magus/type-token";

const message = stylelint.utils.ruleMessages(ruleName, {
  rejected: (prop, value) => {
    switch (prop) {
      case "font-size":
        return `font-size: ${value} is not a type token; take it from var(--pf-t--global--font--size--*), or inherit, or an em or % that cannot shrink below the parent`;
      case "font-weight":
        return `font-weight: ${value} is not a type token; take it from var(--pf-t--global--font--weight--*)`;
      case "line-height":
        return `line-height: ${value} is not a type token; take var(--pf-t--global--font--line-height--body) or --heading, or 0 or 1 for a glyph box`;
      case "font-family":
        return `font-family: ${value} is not a type token; take var(--pf-t--global--font--family--body), --heading or --mono`;
      default:
        return `font: ${value} is not a type shorthand; write the longhands from the type tokens, or inherit`;
    }
  },
});

// Named steps only. The numbered primitives (font--size--100 .. --800, font--weight--100 .. --400,
// line-height--100 .. --200, family--100 .. --300) are what the names are built from. Every named size
// step is 12px or larger, which is what holds the floor.
const sizeToken =
  /^--pf-t--global--font--size--(?:body--(?:sm|default|lg)|heading--(?:h[1-6]|xs|sm|md|lg|xl|2xl)|xs|sm|md|lg|xl|[234]xl)$/;
const weightToken =
  /^--pf-t--global--font--weight--(?:(?:body|heading)--(?:default|bold)|(?:body|heading)--legacy|(?:body|heading)--bold--legacy)$/;
const lineHeightToken = /^--pf-t--global--font--line-height--(?:body|heading)$/;
const familyToken = /^--pf-t--global--font--family--(?:body|heading|mono)(?:--legacy)?$/;
// The label and chip tokens are the uppercase recipes magus/label-token requires.
const consoleSizeToken = /^--console-(?:label-size|chip-size|[a-z0-9-]*font-size)$/;
const consoleWeightToken = /^--console-(?:label-weight|chip-weight|[a-z0-9-]*font-weight)$/;
const consoleLineHeightToken = /^--console-[a-z0-9-]*line-height$/;
const noToken = /(?!)/;

// These keep the parent's size or grow it. `initial` is excluded: it resets to medium, which can be smaller.
const sameOrLarger = /^(?:inherit|unset|larger)$/i;
const relative = /^(?:(\d*\.?\d+)(em)|(\d*\.?\d+)(%))$/i;
// 0 collapses a line box and 1 makes the box the glyph's em; PatternFly's own sheets use both
// for icon and glyph boxes. Neither is body leading.
const glyphBox = /^[01]$/;

function relativeCannotShrink(value) {
  const match = relative.exec(value);
  if (!match) return false;
  return match[2] ? Number(match[1]) >= 1 : Number(match[3]) >= 100;
}

function tokenPasses(value, token, consoleToken, accepts) {
  const components = splitTopLevel(value);
  if (components.length !== 1) return false;
  const [component] = components;
  if (accepts(component)) return true;

  const variable = parseVar(component);
  if (!variable) return false;
  if (!token.test(variable.name) && !consoleToken.test(variable.name)) return false;
  return variable.fallback === null || tokenPasses(variable.fallback, token, consoleToken, accepts);
}

const sizePasses = (value) =>
  tokenPasses(value, sizeToken, consoleSizeToken, (component) =>
    sameOrLarger.test(component) || relativeCannotShrink(component));
const weightPasses = (value) =>
  tokenPasses(value, weightToken, consoleWeightToken, (component) => /^(?:inherit|unset)$/i.test(component));
const lineHeightPasses = (value) =>
  tokenPasses(value, lineHeightToken, consoleLineHeightToken, (component) =>
    /^(?:inherit|unset|normal)$/i.test(component) || glyphBox.test(component));
const familyPasses = (value) =>
  tokenPasses(value, familyToken, noToken, (component) => /^(?:inherit|unset)$/i.test(component));

// The shorthand resets every type longhand, so each piece has to be a type token of some kind.
function fontPasses(value) {
  const trimmed = value.trim();
  if (isGlobalKeyword(trimmed)) return true;
  return splitTopLevel(trimmed.replace(/\s*\/\s*/g, " ")).every((component) => {
    const variable = parseVar(component);
    if (!variable) return /^(?:italic|normal)$/i.test(component);
    return [sizeToken, weightToken, lineHeightToken, familyToken].some((token) => token.test(variable.name)) ||
      [consoleSizeToken, consoleWeightToken, consoleLineHeightToken].some((token) => token.test(variable.name));
  });
}

const judges = {
  "font-size": sizePasses,
  "font-weight": weightPasses,
  "line-height": lineHeightPasses,
  "font-family": familyPasses,
  font: fontPasses,
};

export default stylelint.createPlugin(ruleName, (enabled) => {
  return (root, result) => {
    if (!enabled) return;

    root.walkRules((rule) => {
      // An uppercase run's size and weight are magus/label-token's, which pins them to the label and
      // chip tokens. Its leading and family are still checked here.
      let uppercase = false;
      rule.walkDecls("text-transform", (declaration) => {
        if (declaration.value.trim() === "uppercase") uppercase = true;
      });

      rule.walkDecls(/^(?:font(?:-(?:size|weight|family))?|line-height)$/, (declaration) => {
        if (declaration.parent !== rule) return;
        if (uppercase && /^font-(?:size|weight)$/.test(declaration.prop)) return;
        if (judges[declaration.prop](declaration.value)) return;

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
