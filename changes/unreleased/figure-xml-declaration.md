### Fixed

- **Standalone figure SVGs open with an XML declaration.** XML 1.0 section 2.8 says a
  document should begin with one, and strict viewers expect it. The light and dark files
  and `magus job graph` output carry it; the page copy a docs page inlines does not.
