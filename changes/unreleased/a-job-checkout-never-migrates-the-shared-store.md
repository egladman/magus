### Fixed

- **A job's checkout never migrates the shared job store.** Inside the checkout a live
  job's fork made or adopted, a magus with newer migrations refuses the store before
  reading or writing it, naming both levels. The root migrates it once from its own
  checkout. `hack/dev/bootstrap-checkout.buzz` stops on the same refusal before any
  step writes the store.
  [State](https://eli.gladman.cc/magus/concepts/state/#store-migrations).
