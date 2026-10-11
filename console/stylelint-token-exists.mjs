import stylelint from "stylelint";
import { patternflyNames } from "./stylelint-pf.mjs";
import { referencedProperties } from "./stylelint-value.mjs";

export const ruleName = "magus/token-exists";

const message = stylelint.utils.ruleMessages(ruleName, {
  missing: (name, hint) =>
    `${name} is not defined by the installed PatternFly${hint ? `; did you mean ${hint}` : ""}`,
});

const token = /^--pf-t--/;
const componentProperty = /^--pf-v6-[cl]-/;

// A misspelt token resolves to nothing, so the declaration silently falls back to the inherited
// or initial value. Suggestions are the real names that share the last two segments.
function suggest(name, known) {
  const tail = name.split("--").slice(-2).join("--");
  if (tail === "") return "";
  const near = [...known].filter((candidate) => candidate.endsWith(`--${tail}`)).slice(0, 3);
  return near.join(" or ");
}

export default stylelint.createPlugin(ruleName, (enabled) => {
  return (root, result) => {
    if (!enabled) return;

    const { tokens, properties } = patternflyNames();

    const check = (name, node) => {
      const known = token.test(name) ? tokens : componentProperty.test(name) ? properties : null;
      if (known === null || known.has(name)) return;

      stylelint.utils.report({
        message: message.missing(name, suggest(name, known)),
        node,
        result,
        ruleName,
      });
    };

    root.walkDecls((declaration) => {
      if (declaration.prop.startsWith("--")) check(declaration.prop, declaration);
      for (const name of new Set(referencedProperties(declaration.value))) check(name, declaration);
    });
  };
});
