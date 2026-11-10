### Changed

- **A Buzz fiber's error reaches the resuming code as the value thrown.** A typed
  `catch (e: MyErr)` around `resume`, `resolve` or a `foreach` over a fiber now
  matches, and a thrown string arrives without the `buzz: uncaught error:` prefix, as
  upstream does. Uncaught, the program ends with the same message as before.
- **Breaking: `resume` on a fiber that is over raises `Fiber is over`**, as upstream
  does, where it returned null. `Session.ResumeFiber` does the same; `resolve` still
  answers a finished fiber's return value or re-raises its error.
