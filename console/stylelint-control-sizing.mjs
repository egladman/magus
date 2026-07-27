import stylelint from "stylelint";

export const ruleName = "magus/control-size-token";

const message = stylelint.utils.ruleMessages(ruleName, {
  rejected: "shared controls must use var(--console-control-block-size) for block sizing",
});

const sizingProperty = /^(?:block-size|height|min-block-size|min-height|padding-block|padding-block-(?:start|end)|--pf-v6-c-(?:button|form-control).*PaddingBlock(?:Start|End))$/;
const sharedControlSelector = /\[data-control-size(?:[\]=]|$)/;
const sharedControlValue = "var(--console-control-block-size)";

export default stylelint.createPlugin(ruleName, (enabled) => {
  return (root, result) => {
    if (!enabled) return;

    root.walkRules((rule) => {
      if (!rule.selectors?.some((selector) => sharedControlSelector.test(selector))) return;

      rule.walkDecls((declaration) => {
        if (!sizingProperty.test(declaration.prop) || declaration.value === sharedControlValue) return;

        stylelint.utils.report({
          message: message.rejected,
          node: declaration,
          result,
          ruleName,
        });
      });
    });
  };
});
