import colorToken from "./stylelint-color-token.mjs";
import containerQuery from "./stylelint-container-query.mjs";
import controlSizing from "./stylelint-control-sizing.mjs";
import labelToken from "./stylelint-label-token.mjs";
import shapeToken from "./stylelint-shape-token.mjs";
import spacerToken from "./stylelint-spacer-token.mjs";
import tokenExists from "./stylelint-token-exists.mjs";
import tokenTier from "./stylelint-token-tier.mjs";
import typeToken from "./stylelint-type-token.mjs";

export default {
  extends: ["stylelint-config-standard"],
  plugins: [
    colorToken,
    containerQuery,
    controlSizing,
    labelToken,
    shapeToken,
    spacerToken,
    tokenExists,
    tokenTier,
    typeToken,
  ],
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
    // Raw width and height stay available for layout, illustrations, and data visualizations.
    // A component author opts a shared control group into the stronger invariant with
    // data-control-size; the local rule then makes its block sizing use one token.
    "magus/control-size-token": true,
    // The console has ONE uppercase label and ONE uppercase chip. Written out by hand it came to 35
    // near-misses across seven sheets - six font sizes, six tracking values, three weights - which is
    // the drift a reader sees as "every app looks slightly different".
    "magus/label-token": true,
    // Lint stayed green while the apps used between ten and twenty-one distinct font sizes each,
    // many under 12px, and picked their weights per file. Type comes from the PatternFly size and
    // weight tokens (every named size step is 12px or larger) or the console's own; an uppercase run
    // is label-token's. A relative em or % is accepted only where it cannot shrink below the parent.
    // Leading and family come from the same ramp as size: the two line-height tokens, the three
    // family tokens, and the font shorthand only when each piece is one of those.
    "magus/type-token": true,
    // PatternFly: "never use a token that ends in a number" and "do not use palette tokens". Base
    // and palette tokens feed the semantic ones; consuming one pins a value the theme cannot move.
    // tokens.css is the one file that adapts the base layer, so it is the one that may name it.
    "magus/token-tier": [true, {
      definitions: [
        {
          file: /(?:^|\/)src\/styles\/tokens\.css$/,
          reason: "tokens.css adapts PatternFly's base and palette tokens into the console's semantic and --console-* slots",
        },
      ],
    }],
    // A mistyped token resolves to nothing and the declaration quietly falls back to the inherited
    // value. Every --pf-t-- token and every --pf-v6-c-* or --pf-v6-l-* property the console declares
    // or reads must exist in the installed PatternFly, which the rule reads from node_modules.
    "magus/token-exists": true,
    // Radius, border width and duration were written as 2px, 1px solid and 0.18s in hand-picked
    // values that do not follow the console's two radius tiers or its motion scale. They take the
    // PatternFly radius, border-width and motion-duration tokens, or the --console-motion* ones.
    "magus/shape-token": true,
    // Spacing was invented per file in rem, so two panels that should line up differ by a fraction of
    // a rem that nobody chose. padding, margin, gap and inset take 0, auto, a percentage, a spacer
    // token or a calc() of those; so do top, right, bottom and left. The one allowance is geometry
    // that is data rather than spacing: a selector listed here (and the properties it covers, when
    // only some are data), with the reason it is exempt, and nothing else. There is no file-wide ignore.
    "magus/spacer-token": [true, {
      geometry: [
        {
          selector: /^\.(?:uplot|u-[a-z-]+)(?![a-z-])/,
          reason: "uPlot's vendored sheet lays out chart axes, legends and cursors in pixels the chart library measures",
        },
        {
          selector: /^\.console-diff-row--(?:comment|touch|quote|outline)(?![a-z-])/,
          property: /^padding-inline-start$/,
          reason: "a virtualized diff row indents its remark under the code column in ch, the width of the line-number gutter, which is data",
        },
        {
          selector: /^\.console-plan-list__(?:goal|item)(?![a-z_-])/,
          property: /^padding-inline(?:-start)?$/,
          reason: "a plan row indents by --console-plan-depth, the depth of the job in the tree, which is data",
        },
      ],
    }],
    // Hex, rgb() and hsl() literals meant a colour that ignores the theme: it is right in light and
    // wrong in dark, and no token revision can move it. Colour comes from a PatternFly or --console-*
    // token, and a shadow from a PatternFly box-shadow token or none. tokens.css is the one file that
    // may define a palette in custom properties.
    "magus/color-token": [true, {
      definitions: [
        {
          file: /(?:^|\/)src\/styles\/tokens\.css$/,
          reason: "tokens.css is the one file that adapts PatternFly's palette and defines the console's colours",
        },
      ],
    }],
    // The console tiles panes, so a pane's width has nothing to do with the window's. A viewport
    // @media width query in an app stylesheet answers the wrong question and breaks the moment the
    // pane is tiled narrow in a wide window; @container asks the pane. Pointer, hover, prefers-* and
    // print queries are about the device, not the layout, and stay.
    "magus/container-query": true,
  },
};
