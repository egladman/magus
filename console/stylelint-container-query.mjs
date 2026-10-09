import stylelint from "stylelint";

export const ruleName = "magus/container-query";

const message = stylelint.utils.ruleMessages(ruleName, {
  rejected: (params) =>
    `@media ${params} sizes an app against the window; the console tiles panes, so use @container`,
});

const appFile = /(?:^|\/)src\/apps\//;
// Both spellings of a width query: (min-width: 40rem) and the range form (400px <= width < 40rem).
// A pointer, hover, prefers-* or print query never names the feature, so it does not match.
const widthFeature = /(?:^|[(\s])(?:(?:min|max)-)?(?:device-)?width\s*(?:[:<>=)]|$)/i;

export default stylelint.createPlugin(ruleName, (enabled) => {
  return (root, result) => {
    if (!enabled) return;

    const file = (root.source?.input.file ?? "").replaceAll("\\", "/");
    if (!appFile.test(file)) return;

    root.walkAtRules(/^media$/i, (atRule) => {
      if (!widthFeature.test(atRule.params)) return;

      stylelint.utils.report({
        message: message.rejected(atRule.params),
        node: atRule,
        result,
        ruleName,
      });
    });
  };
});
