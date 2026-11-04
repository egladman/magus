### Fixed

- **A child job forked from elsewhere binds to its parent's checkout.** The fork takes the
  checkout the parent's fork made or adopted, and refuses a parent only taken in another
  checkout, naming it.
