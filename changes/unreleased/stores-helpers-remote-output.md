### Fixed

- **A remote output fetched by ref is filed by rename.** A concurrent reader never sees
  it half written, and a review's seen threads never come back as new after two
  processes save them.
