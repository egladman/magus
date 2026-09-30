### Fixed

- **Breaking: a Buzz annotation naming an undeclared type is BZZ1002.** `fun f(r: Nope)`
  and `fun f(r: magus\Nope)` accepted any argument; now they fail, as upstream Buzz
  does. `ns\T` resolves through the namespace `ns`, host types reach aliased imports,
  and `serialize\Boxed` is declared.
