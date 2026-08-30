import stylelint from "stylelint";

export const ruleName = "magus/label-token";

const message = stylelint.utils.ruleMessages(ruleName, {
  rejected: (prop) =>
    `an uppercase run takes ${prop} from the label or chip tokens (--console-label-* / --console-chip-*)`,
});

// The three properties that decide how an uppercase run reads. Its color, margin and any border it
// sits inside stay the adopter's own business.
const watched = /^(?:font-size|font-weight|letter-spacing)$/;
const allowed = /var\(--console-(?:label|chip)-(?:size|weight|tracking)\)/;

export default stylelint.createPlugin(ruleName, (enabled) => {
  return (root, result) => {
    if (!enabled) return;

    root.walkRules((rule) => {
      let uppercase = false;
      rule.walkDecls("text-transform", (declaration) => {
        if (declaration.value.trim() === "uppercase") uppercase = true;
      });
      if (!uppercase) return;

      rule.walkDecls((declaration) => {
        if (!watched.test(declaration.prop)) return;
        if (allowed.test(declaration.value)) return;

        stylelint.utils.report({
          message: message.rejected(declaration.prop),
          node: declaration,
          result,
          ruleName,
        });
      });
    });
  };
});
