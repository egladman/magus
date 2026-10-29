### Fixed

- **Every figure opens outside a browser page.** `magus/figure` wrote CSS `var()` into
  `fill`, `stroke` and `font-family` attributes, which is invalid SVG, so ImageMagick and
  Android refused the job graph and every other figure. Attributes now carry hex and font
  names. `Theme.page` themes through a `<style>` block of role classes that read the page's
  `--magus-diagram-*` properties and follow `prefers-color-scheme` when the page sets none.
