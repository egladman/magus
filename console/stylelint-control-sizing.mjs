import stylelint from "stylelint";

export const ruleName = "magus/control-size-token";

const message = stylelint.utils.ruleMessages(ruleName, {
  rejected: "shared controls must use var(--console-control-block-size) for block sizing",
});

const sizingProperty = /^(?:block-size|height|min-block-size|min-height|padding-block|padding-block-(?:start|end)|--pf-v6-c-.*PaddingBlock(?:Start|End))$/;
const sharedControlSelector = /\[data-control-size(?:[\]=]|$)/;
const sharedControlValue = "var(--console-control-block-size)";
// A control whose height comes from the token has to get its own padding out of the way, and zero is
// the only way to say that. Without this the rule is unsatisfiable: `block-size` wants the token,
// `padding-block` is watched, and no value of `padding-block` is both zero and the token. Only an
// explicit zero is allowed through - any other hand-picked padding is still the drift being policed.
const zeroPadding = /^0(?:px|rem|em|%)?$/;
const paddingProperty = /padding-block|PaddingBlock/;

export default stylelint.createPlugin(ruleName, (enabled) => {
  return (root, result) => {
    if (!enabled) return;

    root.walkRules((rule) => {
      if (!rule.selectors?.some((selector) => sharedControlSelector.test(selector))) return;

      rule.walkDecls((declaration) => {
        if (!sizingProperty.test(declaration.prop) || declaration.value === sharedControlValue) return;
        if (paddingProperty.test(declaration.prop) && zeroPadding.test(declaration.value.trim())) return;

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
