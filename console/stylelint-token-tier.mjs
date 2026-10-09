import stylelint from "stylelint";
import { referencedProperties } from "./stylelint-value.mjs";

export const ruleName = "magus/token-tier";

const message = stylelint.utils.ruleMessages(ruleName, {
  numbered: (name, hint) =>
    `${name} is a base token (it ends in a number); take the semantic token${hint ? `, ${hint}` : ""}`,
  palette: (name) =>
    `${name} is a palette token; take a semantic token for the role it plays`,
});

// PatternFly: "never use a token that ends in a number", "do not use palette tokens". Base and
// palette tokens exist to feed the semantic ones, and only the semantic ones follow the theme.
const numbered = /^--pf-t--.*--\d+$/;
const palette = /^--pf-t--color--/;

const hints = [
  [/--z-index--\d+$/, "--pf-t--global--z-index--xs .. --2xl"],
  [/--border--width--\d+$/, "--pf-t--global--border--width--regular, --strong or --extra-strong"],
  [/--border--radius--\d+$/, "--pf-t--global--border--radius--tiny .. --large"],
  [/--spacer--\d+$/, "--pf-t--global--spacer--xs .. --4xl"],
  [/--font--size--\d+$/, "--pf-t--global--font--size--body--* or --heading--*"],
  [/--font--weight--\d+$/, "--pf-t--global--font--weight--body--* or --heading--*"],
  [/--color--status--[a-z]+--\d+$/, "--pf-t--global--color--status--*--default or --hover"],
];

const hintFor = (name) => hints.find(([pattern]) => pattern.test(name))?.[1] ?? "";

// A definition allowance names the file that is allowed to touch the base layer, and why. It is
// the file that adapts PatternFly's palette; it never covers a stylesheet that merely consumes it.
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
    if ((options?.definitions ?? []).some((entry) => entry.file.test(file))) return;

    const check = (name, node) => {
      let text = null;
      if (palette.test(name)) text = message.palette(name);
      else if (numbered.test(name)) text = message.numbered(name, hintFor(name));
      if (text === null) return;

      stylelint.utils.report({ message: text, node, result, ruleName });
    };

    root.walkDecls((declaration) => {
      check(declaration.prop, declaration);
      for (const name of new Set(referencedProperties(declaration.value))) check(name, declaration);
    });
  };
});
