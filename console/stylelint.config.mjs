import controlSizing from "./stylelint-control-sizing.mjs";
import labelToken from "./stylelint-label-token.mjs";

export default {
  extends: ["stylelint-config-standard"],
  plugins: [controlSizing, labelToken],
  rules: {
    // Existing console CSS intentionally uses BEM names, PatternFly custom properties,
    // dense declaration groups, and legacy-compatible color/media syntax. Keep the
    // correctness rules from the standard baseline without turning this into a mass
    // reformat/rename migration.
    "alpha-value-notation": null,
    "at-rule-empty-line-before": null,
    "color-function-alias-notation": null,
    "color-function-notation": null,
    "comment-empty-line-before": null,
    "custom-property-empty-line-before": null,
    "custom-property-pattern": null,
    "declaration-block-no-redundant-longhand-properties": null,
    "declaration-block-single-line-max-declarations": null,
    "declaration-empty-line-before": null,
    "declaration-property-value-keyword-no-deprecated": null,
    "color-hex-length": null,
    "import-notation": null,
    "media-feature-range-notation": null,
    "no-duplicate-selectors": null,
    "no-descending-specificity": null,
    "property-no-vendor-prefix": null,
    "rule-empty-line-before": null,
    "selector-class-pattern": null,
    "value-keyword-case": null,
    // Keep raw dimensions available for layout, illustrations, and data visualizations.
    // A component author opts a shared control group into the stronger invariant with
    // data-control-size; the local rule then makes its block sizing use one token.
    "magus/control-size-token": true,
    // The console has ONE uppercase label and ONE uppercase chip. Written out by hand it came to 35
    // near-misses across seven sheets - six font sizes, six tracking values, three weights - which is
    // the drift a reader sees as "every surface looks slightly different".
    "magus/label-token": true,
  },
};
