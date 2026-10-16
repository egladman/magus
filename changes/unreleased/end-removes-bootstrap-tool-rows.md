### Fixed

- **`magus job exit` removes a checkout whose only changes are the tool rows a wired
  harness's install wrote.** Those files sit in the harness's tool rows directory (the
  `models`, `tiers`, capability and `version` files), which `magus describe file` leaves
  unclaimed, so the checkout was kept as holding uncommitted work. They are now restored
  like any other file magus writes; a change to any other file still keeps the checkout.
