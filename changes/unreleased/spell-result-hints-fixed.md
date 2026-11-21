### Fixed

- **Spell Results retain diagnostic hints.** The provider reader accepts `hint`
  from the shared diagnostic, so failed provider and cache operations report their
  original error. The retired `fix` key is refused.
