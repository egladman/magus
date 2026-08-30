import stylelint from "stylelint";

export const ruleName = "magus/control-size-token";

const message = stylelint.utils.ruleMessages(ruleName, {
  rejected:
    "shared controls must take block sizing from var(--console-control-block-size) and type from var(--console-control-font-size)",
});

const sizingProperty = /^(?:block-size|height|min-block-size|min-height|font-size|padding-block|padding-block-(?:start|end)|--pf-v6-c-.*PaddingBlock(?:Start|End))$/;
const sharedControlSelector = /\[data-control-size(?:[\]=]|$)/;
const sharedControlValues = new Set([
  "var(--console-control-block-size)",
  "var(--console-control-font-size)",
]);
// A control whose height comes from the token has to get its own padding out of the way, and zero is
// the only way to say that. Without this the rule is unsatisfiable: `block-size` wants the token,
// `padding-block` is watched, and no value of `padding-block` is both zero and the token. Only an
// explicit zero is allowed through - any other hand-picked padding is still the drift being policed.
const zeroPadding = /^0(?:px|rem|em|%)?$/;
// A COMPOSITE control - PatternFly's FormControl is a wrapper around a real input - inherits the
// tier by filling the wrapper the tier already sized. `100%` is not a hand-picked number, it is
// "whatever my token-sized parent is", so it is the one other satisfiable answer for block sizing.
const fillsParent = /^100%$/;
const paddingProperty = /padding-block|PaddingBlock/;
const blockSizeProperty = /^(?:block-size|height|min-block-size|min-height)$/;

export default stylelint.createPlugin(ruleName, (enabled) => {
  return (root, result) => {
    if (!enabled) return;

    root.walkRules((rule) => {
      if (!rule.selectors?.some((selector) => sharedControlSelector.test(selector))) return;

      rule.walkDecls((declaration) => {
        if (!sizingProperty.test(declaration.prop) || sharedControlValues.has(declaration.value)) return;
        if (paddingProperty.test(declaration.prop) && zeroPadding.test(declaration.value.trim())) return;
        if (blockSizeProperty.test(declaration.prop) && fillsParent.test(declaration.value.trim())) return;

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
