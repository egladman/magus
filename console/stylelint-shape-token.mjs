import stylelint from "stylelint";
import {
  isGlobalKeyword,
  isMathFunction,
  mathOperandsPass,
  parseFunction,
  parseVar,
  splitTopLevel,
} from "./stylelint-value.mjs";

export const ruleName = "magus/shape-token";

const message = stylelint.utils.ruleMessages(ruleName, {
  radius: (prop, value) =>
    `${prop}: ${value} is not a radius token; take var(--pf-t--global--border--radius--*) (tiny, small, medium, large, pill, or a control or action role), or 0 or a percentage`,
  width: (prop, value) =>
    `${prop}: ${value} writes a border width by hand; take var(--pf-t--global--border--width--regular), --strong or --extra-strong, or 0`,
  duration: (prop, value) =>
    `${prop}: ${value} writes a duration by hand; take var(--pf-t--global--motion--duration--*) or a --console-motion* token`,
});

const radiusProperty = /^border(?:-(?:top|bottom)-(?:left|right)|-(?:start|end)-(?:start|end))?-radius$/;
const widthLonghand = /^(?:border(?:-(?:top|right|bottom|left|block|inline)(?:-(?:start|end))?)?-width|outline-width)$/;
const widthShorthand = /^(?:border(?:-(?:top|right|bottom|left|block|inline)(?:-(?:start|end))?)?|outline)$/;
const timeProperty = /^(?:transition|animation)(?:-(?:duration|delay))?$/;

// Semantic names only; the numbered steps are what they are built from. 'tiny' to 'large' and
// 'pill' are the scale, and the control and action roles are where the console sets its 6px tier.
const radiusToken =
  /^--pf-t--global--border--radius--(?:tiny|small|medium|large|pill|sharp|glass--default|(?:action|action--plain|control)--default|control--form-element)$/;
const consoleRadiusToken = /^--console-radius(?:-[a-z0-9]+)*$/;
const widthToken = /^--pf-t--global--(?:border--width--[a-z][a-z-]*|focus-ring--width--(?:offset|inset))$/;
const consoleWidthToken = /^--console-[a-z0-9-]*(?:border-width|-bar)$/;
const motionToken = /^--(?:pf-t--global--motion--duration--[a-z0-9-]+|console-motion(?:-[a-z]+)*)$/;

const percentage = /^[+-]?(?:\d+\.?\d*|\.\d+)%$/;
const zero = /^[+-]?0*\.?0+(?:[a-z]+)?$/i;
const length = /^[+-]?(?:\d+\.?\d*|\.\d+)(?:px|r?em|ch|ex|vw|vh|vmin|vmax|pt|cm|mm|in|lh|rlh)$/i;
const namedWidth = /^(?:thin|medium|thick)$/i;
const time = /(?<![\w.-])([+-]?(?:\d+\.?\d*|\.\d+))(ms|s)(?![\w-])/gi;

// A width component of a shorthand is recognisable by what it is made of. A var() that is not
// named like a width is a colour, and the colour rules own it.
const widthVariable = /--(?:border--width|focus-ring--width)--|-border-width$|^--console-[a-z0-9-]*-bar$/;

function radiusComponentPasses(component) {
  if (zero.test(component) || percentage.test(component) || isGlobalKeyword(component)) return true;

  const variable = parseVar(component);
  if (variable) {
    if (!radiusToken.test(variable.name) && !consoleRadiusToken.test(variable.name)) return false;
    return variable.fallback === null || radiusValuePasses(variable.fallback);
  }

  const fn = parseFunction(component);
  return fn !== null && isMathFunction(fn.name) && mathOperandsPass(fn.args, radiusComponentPasses);
}

// "a b / c d" is the horizontal and vertical radii; every piece is judged alone.
function radiusValuePasses(value) {
  const components = splitTopLevel(value.replace(/\s*\/\s*/g, " "));
  return components.length > 0 && components.every(radiusComponentPasses);
}

function widthComponentPasses(component) {
  if (zero.test(component) || isGlobalKeyword(component)) return true;
  if (length.test(component) || namedWidth.test(component)) return false;

  const variable = parseVar(component);
  if (variable) {
    if (!widthToken.test(variable.name) && !consoleWidthToken.test(variable.name)) return false;
    return variable.fallback === null || widthComponentPasses(variable.fallback);
  }

  const fn = parseFunction(component);
  return fn === null || !isMathFunction(fn.name) || mathOperandsPass(fn.args, widthComponentPasses);
}

// Longhands are all width. In a shorthand the width is whichever pieces read as a length, a width
// token or a math function around one; style keywords and colours are somebody else's.
function shorthandWidthPasses(value) {
  return splitTopLevel(value).every((component) => {
    if (length.test(component) && !zero.test(component)) return false;
    if (namedWidth.test(component)) return false;
    const variable = parseVar(component);
    if (variable && !widthVariable.test(variable.name)) return true;
    const fn = parseFunction(component);
    if (variable || (fn !== null && isMathFunction(fn.name))) return widthComponentPasses(component);
    return true;
  });
}

// Anything at or under 1ms is "motion off": PatternFly's own reduced-motion override is 1ms, and a
// non-zero value keeps transitionend and animationend firing. Every other duration is a token.
function durationsPass(value) {
  for (const match of value.matchAll(time)) {
    const milliseconds = Number(match[1]) * (match[2].toLowerCase() === "s" ? 1000 : 1);
    if (milliseconds > 1) return false;
  }
  return true;
}

export default stylelint.createPlugin(ruleName, (enabled) => {
  return (root, result) => {
    if (!enabled) return;

    const report = (declaration, text) =>
      stylelint.utils.report({ message: text, node: declaration, result, ruleName });

    root.walkDecls((declaration) => {
      const { prop, value } = declaration;
      if (radiusProperty.test(prop)) {
        if (!radiusValuePasses(value)) report(declaration, message.radius(prop, value));
      } else if (widthLonghand.test(prop)) {
        if (!splitTopLevel(value).every(widthComponentPasses)) report(declaration, message.width(prop, value));
      } else if (widthShorthand.test(prop)) {
        if (!shorthandWidthPasses(value)) report(declaration, message.width(prop, value));
      } else if (timeProperty.test(prop)) {
        if (!durationsPass(value)) report(declaration, message.duration(prop, value));
      }
    });
  };
});
